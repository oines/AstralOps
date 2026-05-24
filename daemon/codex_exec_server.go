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
	app       *app
	ws        Workspace
	proxy     *proxyClient
	socket    *websocket.Conn
	sessionID string

	mu        sync.Mutex
	processes map[string]*execServerProcess
}

type execServerProcess struct {
	id       string
	mu       sync.Mutex
	cond     *sync.Cond
	nextSeq  int64
	chunks   []execServerChunk
	exited   bool
	exitCode int
	closed   bool
	failure  string
	pty      bool
}

type execServerChunk struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
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
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	socket, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &execServerConn{
		app:       a,
		ws:        ws,
		proxy:     proxy,
		socket:    socket,
		sessionID: "exec_" + randomID(12),
		processes: map[string]*execServerProcess{},
	}
	conn.serve()
}

func (c *execServerConn) serve() {
	defer c.socket.Close()
	for {
		_, body, err := c.socket.ReadMessage()
		if err != nil {
			return
		}
		var req execServerRequest
		if err := json.Unmarshal(body, &req); err != nil {
			_ = c.writeResponse(nil, nil, err)
			continue
		}
		result, err := c.dispatch(req)
		_ = c.writeResponse(req.ID, result, err)
	}
}

func (c *execServerConn) writeResponse(id any, result any, err error) error {
	message := map[string]any{"jsonrpc": "2.0", "id": id}
	if err != nil {
		message["error"] = map[string]any{"code": -32000, "message": err.Error()}
	} else {
		if result == nil {
			result = map[string]any{}
		}
		message["result"] = result
	}
	return c.socket.WriteJSON(message)
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
		return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString([]byte(stringValue(out["content"])))}, nil
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
		return map[string]any{}, c.remoteCall(ctx, "write", map[string]any{"path": p.Path, "content": string(body)}, nil)
	case "fs/createDirectory":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, c.remoteCall(ctx, "mkdir", map[string]any{"path": p.Path}, nil)
	case "fs/getMetadata":
		return c.getMetadata(ctx, req.Params)
	case "fs/readDirectory":
		return c.readDirectory(ctx, req.Params)
	case "fs/remove":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, c.remoteCall(ctx, "remove", map[string]any{"path": p.Path}, nil)
	case "fs/copy":
		var p struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, c.remoteCall(ctx, "copy", map[string]any{"source": p.Source, "destination": p.Destination}, nil)
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
		return nil, err
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
		return nil, err
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
	proc := newExecServerProcess(p.ProcessID)
	proc.pty = p.TTY
	c.mu.Lock()
	c.processes[p.ProcessID] = proc
	c.mu.Unlock()

	if p.TTY {
		return c.startTTYProcess(p, proc)
	}
	command := argvToShellCommand(p.Argv)
	if command == "" {
		proc.finish(1, "empty argv")
		return nil, errors.New("empty argv")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()
		var out map[string]any
		err := c.remoteCall(ctx, "exec", map[string]any{"cwd": p.CWD, "command": command, "env": p.Env, "timeout_ms": int((24 * time.Hour).Milliseconds())}, &out)
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

func (c *execServerConn) startTTYProcess(p struct {
	ProcessID string            `json:"processId"`
	Argv      []string          `json:"argv"`
	CWD       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	TTY       bool              `json:"tty"`
}, proc *execServerProcess) (any, error) {
	proxy, events, unsubscribe, _, err := c.app.ssh.startPTY(context.Background(), c.ws, p.ProcessID, map[string]any{"cwd": p.CWD})
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
				proc.finish(0, "")
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
	if proc.pty {
		_ = c.remoteCall(context.Background(), "pty_kill", map[string]any{"id": p.ProcessID}, nil)
	}
	proc.finish(143, "")
	return map[string]any{"running": false}, nil
}

func (c *execServerConn) lookupProcess(id string) *execServerProcess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processes[id]
}

func newExecServerProcess(id string) *execServerProcess {
	p := &execServerProcess{id: id}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *execServerProcess) addChunk(stream string, body []byte) {
	p.mu.Lock()
	p.nextSeq++
	p.chunks = append(p.chunks, execServerChunk{Seq: p.nextSeq, Stream: stream, Chunk: base64.StdEncoding.EncodeToString(body)})
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *execServerProcess) finish(exitCode int, failure string) {
	p.mu.Lock()
	p.exited = true
	p.exitCode = exitCode
	p.closed = true
	p.failure = failure
	p.cond.Broadcast()
	p.mu.Unlock()
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
	nextSeq := afterSeq
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
		nextSeq = chunk.Seq
	}
	out := map[string]any{
		"chunks":   chunks,
		"nextSeq":  nextSeq,
		"exited":   p.exited,
		"exitCode": p.exitCode,
		"closed":   p.closed,
		"failure":  p.failure,
	}
	p.mu.Unlock()
	return out
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
	if len(argv) >= 3 && (filepath.Base(argv[0]) == "sh" || filepath.Base(argv[0]) == "bash" || filepath.Base(argv[0]) == "zsh") && argv[1] == "-lc" {
		return argv[2]
	}
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}
