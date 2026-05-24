package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type execServerRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type execServerConn struct {
	app         *app
	ws          Workspace
	proxy       *proxyClient
	socket      *websocket.Conn
	sessionID   string
	remoteShell string

	mu        sync.Mutex
	writeMu   sync.Mutex
	processes map[string]*execServerProcess
	remote    func(context.Context, string, any, any) error
}

type execServerJSONRPCError struct {
	Code    int
	Message string
}

func (e *execServerJSONRPCError) Error() string {
	return e.Message
}

type execServerProcess struct {
	id       string
	mu       sync.Mutex
	cond     *sync.Cond
	cancel   context.CancelFunc
	nextSeq  int64
	chunks   []execServerChunk
	exited   bool
	exitCode int
	closed   bool
	failure  string
	pty      bool
	notify   func(method string, params any) error
}

type execServerChunk struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}

type codexExecCommand struct {
	NativeCommand    string
	EffectiveCommand string
}

func (a *app) handleCodexExecServerWS(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/codex-exec/"), "/")
	if workspaceID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	ws, ok := a.store.getWorkspace(workspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if ws.Target != "ssh" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "codex exec-server is only used for ssh workspaces"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	proxy, state, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	socket, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &execServerConn{
		app:         a,
		ws:          ws,
		proxy:       proxy,
		socket:      socket,
		sessionID:   "exec_" + randomID(12),
		remoteShell: strings.TrimSpace(state.RemoteShell),
		processes:   map[string]*execServerProcess{},
	}
	conn.serve()
}

func (c *execServerConn) serve() {
	defer c.shutdown()
	for {
		_, body, err := c.socket.ReadMessage()
		if err != nil {
			return
		}
		id, result, dispatchErr, respond := c.handleMessage(body)
		if !respond {
			continue
		}
		_ = c.writeResponse(id, result, dispatchErr)
	}
}

func (c *execServerConn) shutdown() {
	c.mu.Lock()
	processes := make([]*execServerProcess, 0, len(c.processes))
	for _, proc := range c.processes {
		processes = append(processes, proc)
	}
	c.mu.Unlock()
	for _, proc := range processes {
		proc.cancelProcess()
		if proc.pty {
			_ = c.remoteCall(context.Background(), "pty_kill", map[string]any{"id": proc.id}, nil)
		} else if c.remote != nil || c.app != nil {
			_ = c.remoteCall(context.Background(), "exec_kill", map[string]any{"id": proc.id}, nil)
		}
		proc.finish(143, "")
	}
	if c.socket != nil {
		_ = c.socket.Close()
	}
}

func (c *execServerConn) handleMessage(body []byte) (any, any, error, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, err, true
	}
	var req execServerRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, err, true
	}
	_, hasID := envelope["id"]
	if !hasID {
		if req.Method == "initialized" {
			return nil, nil, nil, false
		}
		return float64(-1), nil, fmt.Errorf("unexpected exec-server notification %s", req.Method), true
	}
	result, err := c.dispatch(req)
	return req.ID, result, err, true
}

func (c *execServerConn) writeResponse(id any, result any, err error) error {
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if err != nil {
		message["error"] = execServerErrorPayload(err)
	} else {
		if result == nil {
			result = map[string]any{}
		}
		message["result"] = result
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.socket.WriteJSON(message)
}

func execServerErrorPayload(err error) map[string]any {
	payload := map[string]any{"code": -32000, "message": err.Error()}
	var rpcErr *execServerJSONRPCError
	if errors.As(err, &rpcErr) {
		payload["code"] = rpcErr.Code
		payload["message"] = rpcErr.Message
	}
	return payload
}

func execServerNotFound(err error) error {
	if err == nil {
		return nil
	}
	var transport proxyTransportError
	if errors.As(err, &transport) {
		return err
	}
	return &execServerJSONRPCError{Code: -32004, Message: err.Error()}
}

func (c *execServerConn) writeNotification(method string, params any) error {
	if c.socket == nil {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.socket.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *execServerConn) dispatch(req execServerRequest) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch req.Method {
	case "initialize":
		return map[string]any{"sessionId": c.sessionID}, nil
	case "fs/readFile":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		var out map[string]any
		if err := c.remoteCall(ctx, "read", map[string]any{"path": p.Path}, &out); err != nil {
			return nil, err
		}
		data := stringValue(out["dataBase64"])
		if data == "" {
			data = base64.StdEncoding.EncodeToString([]byte(stringValue(out["content"])))
		}
		return map[string]any{"dataBase64": data}, nil
	case "fs/writeFile":
		var p struct {
			Path       string `json:"path"`
			DataBase64 string `json:"dataBase64"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		body, err := base64.StdEncoding.DecodeString(p.DataBase64)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, c.remoteCall(ctx, "write", map[string]any{"path": p.Path, "dataBase64": base64.StdEncoding.EncodeToString(body)}, nil)
	case "fs/createDirectory":
		var p struct {
			Path      string `json:"path"`
			Recursive *bool  `json:"recursive"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		recursive := true
		if p.Recursive != nil {
			recursive = *p.Recursive
		}
		return map[string]any{}, c.remoteCall(ctx, "mkdir", map[string]any{"path": p.Path, "recursive": recursive}, nil)
	case "fs/getMetadata":
		return c.getMetadata(ctx, req.Params)
	case "fs/readDirectory":
		return c.readDirectory(ctx, req.Params)
	case "fs/remove":
		var p struct {
			Path      string `json:"path"`
			Recursive *bool  `json:"recursive"`
			Force     *bool  `json:"force"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		recursive := true
		if p.Recursive != nil {
			recursive = *p.Recursive
		}
		force := true
		if p.Force != nil {
			force = *p.Force
		}
		return map[string]any{}, c.remoteCall(ctx, "remove", map[string]any{"path": p.Path, "recursive": recursive, "force": force}, nil)
	case "fs/copy":
		var p struct {
			Source      string `json:"sourcePath"`
			Destination string `json:"destinationPath"`
			Recursive   bool   `json:"recursive"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, c.remoteCall(ctx, "copy", map[string]any{"source": p.Source, "destination": p.Destination, "recursive": p.Recursive}, nil)
	case "process/start":
		return c.processStart(req.Params)
	case "process/read":
		return c.processRead(req.Params)
	case "process/write":
		return c.processWrite(req.Params)
	case "process/terminate":
		return c.processTerminate(req.Params)
	default:
		return nil, fmt.Errorf("unsupported exec-server method %s", req.Method)
	}
}

func (c *execServerConn) remoteCall(ctx context.Context, method string, params any, out any) error {
	if c.remote != nil {
		return c.remote(ctx, method, params, out)
	}
	return c.app.ssh.call(ctx, c.ws, method, params, out)
}

func (c *execServerConn) getMetadata(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.remoteCall(ctx, "stat", map[string]any{"path": p.Path}, &out); err != nil {
		return nil, execServerNotFound(err)
	}
	modified, _ := time.Parse(time.RFC3339Nano, stringValue(out["modified"]))
	return map[string]any{
		"isDirectory":  boolValue(out["is_dir"]),
		"isFile":       !boolValue(out["is_dir"]),
		"isSymlink":    false,
		"createdAtMs":  0,
		"modifiedAtMs": modified.UnixMilli(),
	}, nil
}

func (c *execServerConn) readDirectory(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	var rawEntries []map[string]any
	if err := c.remoteCall(ctx, "list", map[string]any{"path": p.Path}, &rawEntries); err != nil {
		return nil, execServerNotFound(err)
	}
	entries := make([]map[string]any, 0, len(rawEntries))
	for _, entry := range rawEntries {
		isDir := boolValue(entry["is_dir"])
		entries = append(entries, map[string]any{
			"fileName":    stringValue(entry["name"]),
			"isDirectory": isDir,
			"isFile":      !isDir,
		})
	}
	return map[string]any{"entries": entries}, nil
}

func (c *execServerConn) processStart(raw json.RawMessage) (any, error) {
	var p struct {
		ProcessID string            `json:"processId"`
		Argv      []string          `json:"argv"`
		CWD       string            `json:"cwd"`
		Env       map[string]string `json:"env"`
		TTY       bool              `json:"tty"`
		Arg0      string            `json:"arg0"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ProcessID == "" {
		return nil, errors.New("processId is required")
	}
	if p.CWD == "" {
		p.CWD = c.ws.SSH.RemoteCWD
	}
	if len(p.Argv) == 0 {
		return nil, errors.New("argv must not be empty")
	}
	nativeArgv := append([]string(nil), p.Argv...)
	p.Argv = normalizeCodexArgvForRemote(p.Argv, c.remoteShell)
	if p.Arg0 != "" && len(p.Argv) > 0 {
		p.Arg0 = p.Argv[0]
	}
	if c.app != nil {
		c.app.recordCodexExecCommand(c.ws.ID, p.ProcessID, nativeArgv, p.Argv)
	}
	proc := newExecServerProcess(p.ProcessID, c.writeNotification)
	proc.pty = p.TTY
	c.mu.Lock()
	if _, exists := c.processes[p.ProcessID]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("process %s already exists", p.ProcessID)
	}
	c.processes[p.ProcessID] = proc
	c.mu.Unlock()

	if p.TTY {
		result, err := c.startTTYProcess(p, proc)
		if err != nil {
			c.removeProcess(p.ProcessID)
		}
		return result, err
	}
	if c.remote == nil && c.app != nil {
		result, err := c.startRemoteExecProcess(p, proc)
		if err != nil {
			c.removeProcess(p.ProcessID)
		}
		return result, err
	}
	command := argvToShellCommand(p.Argv)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		proc.setCancel(cancel)
		defer cancel()
		var out map[string]any
		err := c.remoteCall(ctx, "exec", map[string]any{"cwd": p.CWD, "command": command, "argv": p.Argv, "arg0": p.Arg0, "env": p.Env, "timeout_ms": int((24 * time.Hour).Milliseconds())}, &out)
		if stdout := stringValue(out["stdout"]); stdout != "" {
			proc.addChunk("stdout", []byte(stdout))
		}
		if stderr := stringValue(out["stderr"]); stderr != "" {
			proc.addChunk("stderr", []byte(stderr))
		}
		exitCode := int(numberValue(out["exit_code"]))
		if err != nil {
			proc.finish(1, err.Error())
			return
		}
		proc.finish(exitCode, "")
	}()
	return map[string]any{"processId": p.ProcessID}, nil
}

func (c *execServerConn) startRemoteExecProcess(p struct {
	ProcessID string            `json:"processId"`
	Argv      []string          `json:"argv"`
	CWD       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	TTY       bool              `json:"tty"`
	Arg0      string            `json:"arg0"`
}, proc *execServerProcess) (any, error) {
	command := argvToShellCommand(p.Argv)
	proxy, events, unsubscribe, started, err := c.app.ssh.startExec(context.Background(), c.ws, p.ProcessID, map[string]any{"cwd": p.CWD, "command": command, "argv": p.Argv, "env": p.Env, "arg0": p.Arg0, "timeout_ms": int((24 * time.Hour).Milliseconds())})
	if err != nil {
		proc.finish(1, err.Error())
		return nil, err
	}
	c.proxy = proxy
	go func() {
		defer unsubscribe()
		for event := range events {
			if event.Event != "exit" {
				continue
			}
			if stdout := stringValue(event.Result["stdout"]); stdout != "" {
				proc.addChunk("stdout", []byte(stdout))
			}
			if stderr := stringValue(event.Result["stderr"]); stderr != "" {
				proc.addChunk("stderr", []byte(stderr))
			}
			failure := stringValue(event.Result["failure"])
			proc.finish(int(numberValue(event.Result["exit_code"])), failure)
			return
		}
	}()
	return map[string]any{"processId": p.ProcessID, "id": stringValue(started["id"])}, nil
}

func (c *execServerConn) startTTYProcess(p struct {
	ProcessID string            `json:"processId"`
	Argv      []string          `json:"argv"`
	CWD       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	TTY       bool              `json:"tty"`
	Arg0      string            `json:"arg0"`
}, proc *execServerProcess) (any, error) {
	proxy, events, unsubscribe, _, err := c.app.ssh.startPTY(context.Background(), c.ws, p.ProcessID, map[string]any{"cwd": p.CWD, "argv": p.Argv, "env": p.Env, "arg0": p.Arg0})
	if err != nil {
		proc.finish(1, err.Error())
		return nil, err
	}
	c.proxy = proxy
	go func() {
		defer unsubscribe()
		for event := range events {
			switch event.Event {
			case "output":
				proc.addChunk("pty", []byte(stringValue(event.Result["data"])))
			case "exit":
				proc.finish(int(numberValue(event.Result["exit_code"])), "")
				return
			}
		}
	}()
	return map[string]any{"processId": p.ProcessID}, nil
}

func (c *execServerConn) processRead(raw json.RawMessage) (any, error) {
	var p struct {
		ProcessID string `json:"processId"`
		AfterSeq  int64  `json:"afterSeq"`
		MaxBytes  int    `json:"maxBytes"`
		WaitMs    int    `json:"waitMs"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	proc := c.lookupProcess(p.ProcessID)
	if proc == nil {
		return nil, fmt.Errorf("unknown process %s", p.ProcessID)
	}
	return proc.readAfter(p.AfterSeq, p.MaxBytes, p.WaitMs), nil
}

func (c *execServerConn) processWrite(raw json.RawMessage) (any, error) {
	var p struct {
		ProcessID string `json:"processId"`
		Chunk     string `json:"chunk"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	proc := c.lookupProcess(p.ProcessID)
	if proc == nil {
		return map[string]any{"status": "unknownProcess"}, nil
	}
	if !proc.pty {
		return map[string]any{"status": "stdinClosed"}, nil
	}
	body, err := base64.StdEncoding.DecodeString(p.Chunk)
	if err != nil {
		return nil, err
	}
	err = c.remoteCall(context.Background(), "pty_write", map[string]any{"id": p.ProcessID, "data": string(body)}, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "accepted"}, nil
}

func (c *execServerConn) processTerminate(raw json.RawMessage) (any, error) {
	var p struct {
		ProcessID string `json:"processId"`
	}
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	proc := c.lookupProcess(p.ProcessID)
	if proc == nil {
		return map[string]any{"running": false}, nil
	}
	running := !proc.isClosed()
	proc.cancelProcess()
	if proc.pty {
		_ = c.remoteCall(context.Background(), "pty_kill", map[string]any{"id": p.ProcessID}, nil)
	} else if c.remote != nil || c.app != nil {
		_ = c.remoteCall(context.Background(), "exec_kill", map[string]any{"id": p.ProcessID}, nil)
	}
	proc.finish(143, "")
	return map[string]any{"running": running}, nil
}

func (c *execServerConn) lookupProcess(id string) *execServerProcess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processes[id]
}

func (c *execServerConn) removeProcess(id string) {
	c.mu.Lock()
	delete(c.processes, id)
	c.mu.Unlock()
}

func newExecServerProcess(id string, notify func(method string, params any) error) *execServerProcess {
	p := &execServerProcess{id: id, notify: notify}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *execServerProcess) addChunk(stream string, body []byte) {
	encoded := base64.StdEncoding.EncodeToString(body)
	p.mu.Lock()
	p.nextSeq++
	seq := p.nextSeq
	p.chunks = append(p.chunks, execServerChunk{Seq: seq, Stream: stream, Chunk: encoded})
	p.cond.Broadcast()
	p.mu.Unlock()
	p.notifyProcess("process/output", map[string]any{"processId": p.id, "seq": seq, "stream": stream, "chunk": encoded})
}

func (p *execServerProcess) finish(exitCode int, failure string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.nextSeq++
	exitSeq := p.nextSeq
	p.exited = true
	p.exitCode = exitCode
	p.nextSeq++
	closeSeq := p.nextSeq
	p.closed = true
	p.failure = failure
	p.cond.Broadcast()
	p.mu.Unlock()
	p.notifyProcess("process/exited", map[string]any{"processId": p.id, "seq": exitSeq, "exitCode": exitCode})
	p.notifyProcess("process/closed", map[string]any{"processId": p.id, "seq": closeSeq})
}

func (p *execServerProcess) setCancel(cancel context.CancelFunc) {
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()
}

func (p *execServerProcess) cancelProcess() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *execServerProcess) notifyProcess(method string, params any) {
	if p.notify != nil {
		_ = p.notify(method, params)
	}
}

func (p *execServerProcess) readAfter(afterSeq int64, maxBytes int, waitMs int) map[string]any {
	deadline := time.Now().Add(time.Duration(waitMs) * time.Millisecond)
	p.mu.Lock()
	for !p.exited && !p.hasChunksAfter(afterSeq) && waitMs > 0 && time.Now().Before(deadline) {
		timer := time.AfterFunc(time.Until(deadline), func() {
			p.cond.Broadcast()
		})
		p.cond.Wait()
		timer.Stop()
	}
	chunks := []execServerChunk{}
	total := 0
	nextSeq := p.nextSeq
	if nextSeq == 0 {
		nextSeq = 1
	}
	for _, chunk := range p.chunks {
		if chunk.Seq <= afterSeq {
			continue
		}
		size := len(chunk.Chunk)
		if maxBytes > 0 && total > 0 && total+size > maxBytes {
			break
		}
		chunks = append(chunks, chunk)
		total += size
		nextSeq = chunk.Seq + 1
	}
	var exitCode any
	if p.exited {
		exitCode = p.exitCode
	}
	var failure any
	if p.failure != "" {
		failure = p.failure
	}
	out := map[string]any{
		"chunks":   chunks,
		"nextSeq":  nextSeq,
		"exited":   p.exited,
		"exitCode": exitCode,
		"closed":   p.closed,
		"failure":  failure,
	}
	p.mu.Unlock()
	return out
}

func (p *execServerProcess) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *execServerProcess) hasChunksAfter(seq int64) bool {
	for _, chunk := range p.chunks {
		if chunk.Seq > seq {
			return true
		}
	}
	return false
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func argvToShellCommand(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if len(argv) >= 3 && isPOSIXShellName(filepath.Base(argv[0])) && argv[1] == "-lc" {
		return argv[2]
	}
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func argvToDisplayCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuoteIfNeeded(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuoteIfNeeded(value string) string {
	if value == "" {
		return shellQuote(value)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '-', ':', '=', '+', ',':
			continue
		default:
			return shellQuote(value)
		}
	}
	return value
}

func (a *app) recordCodexExecCommand(workspaceID, processID string, nativeArgv, effectiveArgv []string) {
	if a == nil || workspaceID == "" || processID == "" {
		return
	}
	nativeCommand := argvToDisplayCommand(nativeArgv)
	effectiveCommand := argvToDisplayCommand(effectiveArgv)
	if nativeCommand == "" || effectiveCommand == "" || nativeCommand == effectiveCommand {
		return
	}
	a.codexExecMu.Lock()
	if a.codexExec == nil {
		a.codexExec = map[string]codexExecCommand{}
	}
	a.codexExec[codexExecCommandKey(workspaceID, processID)] = codexExecCommand{
		NativeCommand:    nativeCommand,
		EffectiveCommand: effectiveCommand,
	}
	a.codexExecMu.Unlock()
}

func (a *app) codexExecCommand(workspaceID, processID string) (codexExecCommand, bool) {
	if a == nil || workspaceID == "" || processID == "" {
		return codexExecCommand{}, false
	}
	a.codexExecMu.Lock()
	defer a.codexExecMu.Unlock()
	value, ok := a.codexExec[codexExecCommandKey(workspaceID, processID)]
	return value, ok
}

func codexExecCommandKey(workspaceID, processID string) string {
	return workspaceID + "/" + processID
}

func normalizeCodexArgvForRemote(argv []string, remoteShell string) []string {
	argv = stripLocalSandboxWrapper(argv)
	remoteShell = strings.TrimSpace(remoteShell)
	if remoteShell == "" || !filepath.IsAbs(remoteShell) || len(argv) < 3 {
		return argv
	}
	if argv[1] != "-c" && argv[1] != "-lc" {
		return argv
	}
	if !isPOSIXShellName(filepath.Base(argv[0])) || !isPOSIXShellName(filepath.Base(remoteShell)) {
		return argv
	}
	next := append([]string(nil), argv...)
	next[0] = remoteShell
	return next
}

func stripLocalSandboxWrapper(argv []string) []string {
	if len(argv) == 0 || filepath.Base(argv[0]) != "sandbox-exec" {
		return argv
	}
	for i, arg := range argv {
		if arg == "--" && i+1 < len(argv) {
			return append([]string(nil), argv[i+1:]...)
		}
	}
	return argv
}

func isPOSIXShellName(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh":
		return true
	default:
		return false
	}
}
