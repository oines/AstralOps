package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const terminalLocalSocketWriteTimeout = 2 * time.Second

func (a *app) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") == a.token {
			next(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+a.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  version,
		"data_dir": a.store.dataDir,
		"agents":   a.agents,
		"platform": currentHostPlatform(),
		"features": currentHostFeatures(),
	})
}

type createWorkspaceRequest struct {
	Name     string     `json:"name"`
	Target   string     `json:"target"`
	Agent    AgentKind  `json:"agent"`
	LocalCWD string     `json:"local_cwd"`
	SSH      *SSHConfig `json:"ssh"`
}

func (a *app) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.store.listWorkspaces())
	case http.MethodPost:
		var req createWorkspaceRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ws, err := a.createWorkspace(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, ws)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) createWorkspace(req createWorkspaceRequest) (Workspace, error) {
	ws, err := a.store.createWorkspace(req)
	if err != nil {
		return Workspace{}, err
	}
	a.emit(AstralEvent{WorkspaceID: ws.ID, SessionID: "", Agent: ws.Agent, Kind: "workspace.created", Normalized: ws})
	return ws, nil
}

func (a *app) deleteWorkspace(workspaceID string) (map[string]any, error) {
	ws, ok := a.store.getWorkspace(workspaceID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	a.stopWorkspaceSessions(ws.ID, "workspace deleted")
	if terminals := a.terminalManager(); terminals != nil {
		terminals.closeWorkspace(context.Background(), ws.ID, "workspace_deleted")
	}
	if ws.Target == "ssh" {
		a.ssh.disconnect(ws)
	}
	a.store.deleteWorkspace(workspaceID)
	a.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.removed", Normalized: map[string]any{"workspace_id": ws.ID}})
	return map[string]any{"ok": true}, nil
}

func (a *app) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/workspaces/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		result, err := a.deleteWorkspace(parts[0])
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if len(parts) == 2 && parts[1] == "files" && r.Method == http.MethodGet {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		a.handleWorkspaceFiles(w, ws, r.URL.Query().Get("path"))
		return
	}
	if len(parts) == 2 && parts[1] == "terminal" && r.Method == http.MethodPost {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		open, err := a.terminalManager().open(r.Context(), a.store.hostInfo().Identity.DeviceID, terminalOpenParams{WorkspaceID: ws.ID, Cols: defaultTerminalCols, Rows: defaultTerminalRows})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, open)
		return
	}
	if len(parts) == 3 && parts[1] == "terminals" && r.Method == http.MethodDelete {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		open, ok := a.terminalManager().openTerminalResult(parts[2])
		if !ok || open.WorkspaceID != ws.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "terminal not found"})
			return
		}
		closed, err := a.terminalManager().close(r.Context(), a.store.hostInfo().Identity.DeviceID, terminalCloseParams{TerminalID: parts[2]})
		if err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, closed)
		return
	}
	if len(parts) == 2 && parts[1] == "exec" && r.Method == http.MethodPost {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		var req struct {
			Command string `json:"command"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.handleWorkspaceExec(r.Context(), w, ws, req.Command)
		return
	}
	if len(parts) == 2 && parts[1] == "pty" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		a.handleWorkspacePTY(w, r, ws)
		return
	}
	if len(parts) == 2 && parts[1] == "connection" && r.Method == http.MethodGet {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		writeJSON(w, http.StatusOK, a.ssh.getConnection(ws))
		return
	}
	if len(parts) == 2 && parts[1] == "connect" && r.Method == http.MethodPost {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		state, err := a.ssh.connect(ctx, ws)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "connection": state})
			return
		}
		writeJSON(w, http.StatusOK, state)
		return
	}
	if len(parts) == 2 && parts[1] == "disconnect" && r.Method == http.MethodPost {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		writeJSON(w, http.StatusOK, a.ssh.disconnect(ws))
		return
	}
	if len(parts) == 2 && parts[1] == "claude-remote-tool" && r.Method == http.MethodPost {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		a.handleClaudeRemoteTool(w, r, ws)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (a *app) handleWorkspaceFiles(w http.ResponseWriter, ws Workspace, queryPath string) {
	if ws.Target == "ssh" {
		a.handleRemoteWorkspaceFiles(w, ws, queryPath)
		return
	}
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, queryPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	type fileEntry struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Kind    string `json:"kind"`
		Size    int64  `json:"size,omitempty"`
		ModTime string `json:"mod_time,omitempty"`
	}
	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		}
		entryRel := entry.Name()
		if rel != "" {
			entryRel = filepath.ToSlash(filepath.Join(rel, entry.Name()))
		}
		out = append(out, fileEntry{
			Name:    entry.Name(),
			Path:    entryRel,
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "dir"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 300 {
		out = out[:300]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    rel,
		"root":    root,
		"entries": out,
	})
}

func (a *app) handleRemoteWorkspaceFiles(w http.ResponseWriter, ws Workspace, queryPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, queryPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var raw []map[string]any
	if err := a.ssh.call(ctx, ws, "list", map[string]any{"path": target}, &raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	type fileEntry struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Kind    string `json:"kind"`
		Size    int64  `json:"size,omitempty"`
		ModTime string `json:"mod_time,omitempty"`
	}
	out := make([]fileEntry, 0, len(raw))
	for _, entry := range raw {
		name := stringValue(entry["name"])
		path := stringValue(entry["path"])
		entryRel, err := remotePathRel(root, path)
		if err != nil || pathEscapesRoot(entryRel) {
			entryRel = remotePathBase(path)
		}
		kind := "file"
		if boolValue(entry["is_dir"]) {
			kind = "dir"
		}
		out = append(out, fileEntry{
			Name:    name,
			Path:    entryRel,
			Kind:    kind,
			Size:    int64(numberValue(entry["size"])),
			ModTime: stringValue(entry["modified"]),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "dir"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 300 {
		out = out[:300]
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "root": root, "entries": out})
}

func (a *app) handleWorkspaceExec(parent context.Context, w http.ResponseWriter, ws Workspace, command string) {
	if ws.Target == "ssh" {
		a.handleRemoteWorkspaceExec(parent, w, ws, command)
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	cmd := localShellCommand(ctx, command)
	cmd.Dir = ws.LocalCWD
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"command":     command,
		"cwd":         ws.LocalCWD,
		"exit_code":   exitCode,
		"stdout":      stdout.String(),
		"stderr":      stderr.String(),
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

func (a *app) handleRemoteWorkspaceExec(parent context.Context, w http.ResponseWriter, ws Workspace, command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	ctx, cancel := context.WithTimeout(parent, 70*time.Second)
	defer cancel()
	out, err := a.runRemoteWorkspaceExec(ctx, ws, command)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) runRemoteWorkspaceExec(ctx context.Context, ws Workspace, command string) (map[string]any, error) {
	return a.runRemoteWorkspaceExecAt(ctx, ws, command, ws.SSH.RemoteCWD, 60000)
}

func (a *app) runRemoteWorkspaceExecAt(ctx context.Context, ws Workspace, command string, cwd string, timeoutMs int) (map[string]any, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = ws.SSH.RemoteCWD
	}
	if timeoutMs <= 0 {
		timeoutMs = 60000
	}
	execID := "workspace_exec_" + randomID(12)
	proxy, events, unsubscribe, _, err := a.ssh.startExec(ctx, ws, execID, map[string]any{"cwd": cwd, "command": command, "timeout_ms": timeoutMs})
	if err != nil {
		return nil, err
	}
	defer unsubscribe()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return nil, errors.New("ssh exec event stream closed")
			}
			if event.Event != "exit" {
				continue
			}
			out := map[string]any{
				"command":     firstString(stringValue(event.Result["command"]), command),
				"cwd":         firstString(stringValue(event.Result["cwd"]), ws.SSH.RemoteCWD),
				"exit_code":   int(numberValue(event.Result["exit_code"])),
				"stdout":      stringValue(event.Result["stdout"]),
				"stderr":      stringValue(event.Result["stderr"]),
				"output":      stringValue(event.Result["output"]),
				"duration_ms": int64(numberValue(event.Result["duration_ms"])),
			}
			if failure := stringValue(event.Result["failure"]); failure != "" {
				out["failure"] = failure
			}
			return out, nil
		case <-ctx.Done():
			killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = proxy.call(killCtx, "exec_kill", map[string]any{"id": execID}, nil)
			cancel()
			return nil, ctx.Err()
		}
	}
}

type ptyClientMessage struct {
	Type         string `json:"type"`
	Data         string `json:"data,omitempty"`
	Cols         uint16 `json:"cols,omitempty"`
	Rows         uint16 `json:"rows,omitempty"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	HeartbeatSeq int64  `json:"heartbeat_seq,omitempty"`
	RenderedSeq  int64  `json:"rendered_seq,omitempty"`
}

func (a *app) handleWorkspacePTY(w http.ResponseWriter, r *http.Request, ws Workspace) {
	if !terminalAvailableOnHost() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": windowsTerminalDisabledReason})
		return
	}
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	controllerID := a.store.hostInfo().Identity.DeviceID
	terminals := a.terminalManager()
	terminalID := strings.TrimSpace(r.URL.Query().Get("terminal_id"))
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	open, ok := terminalOpenResult{}, false
	if terminalID != "" {
		open, ok = terminals.openTerminalResult(terminalID)
		if !ok || open.WorkspaceID != ws.ID {
			_ = conn.WriteJSON(map[string]any{"type": "error", "message": "terminal not found"})
			return
		}
	}
	if !ok {
		var err error
		open, err = terminals.open(r.Context(), controllerID, terminalOpenParams{WorkspaceID: ws.ID, Cols: defaultTerminalCols, Rows: defaultTerminalRows})
		if err != nil {
			_ = conn.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
			return
		}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	localControl := newLocalPTYControlConnection(ctx, cancel, controllerID, conn)
	attach, err := terminals.attach(controllerID, localControl, terminalAttachParams{TerminalID: open.TerminalID, AfterSeq: afterSeq})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	_ = localControl.writeJSON(terminalReadySocketPayload(open.TerminalID, open.Shell, open.CWD, attach.OutputSeq, attach.ViewerID, attach.InputLeaseID, attach.CanInput))
	defer func() {
		_, _ = terminals.detach(controllerID, localControl, terminalDetachParams{TerminalID: open.TerminalID})
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var message ptyClientMessage
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "input":
			if _, err := terminals.input(r.Context(), controllerID, terminalInputParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID, Data: message.Data}); err != nil {
				_ = localControl.writeJSON(terminalSocketErrorPayload(err))
			}
		case "resize":
			if message.Cols > 0 && message.Rows > 0 {
				if _, err := terminals.resize(r.Context(), controllerID, terminalResizeParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID, Cols: message.Cols, Rows: message.Rows}); err != nil {
					_ = localControl.writeJSON(terminalSocketErrorPayload(err))
				}
			}
		case "heartbeat_ack":
			if ack, err := terminals.heartbeatAck(controllerID, terminalHeartbeatAckParams{TerminalID: open.TerminalID, ViewerID: attach.ViewerID, InputLeaseID: attach.InputLeaseID, HeartbeatSeq: message.HeartbeatSeq, RenderedSeq: message.RenderedSeq}); err == nil {
				_ = localControl.writeJSON(map[string]any{"type": "status", "terminal_id": ack.TerminalID, "state": "live", "can_input": ack.CanInput, "output_seq": ack.OutputSeq})
			}
		case "close":
			_, _ = terminals.close(r.Context(), controllerID, terminalCloseParams{TerminalID: open.TerminalID})
			return
		case "detach":
			return
		}
	}
}

type localPTYControlConnection struct {
	id                 string
	controllerDeviceID string
	ctx                context.Context
	cancel             context.CancelFunc
	socket             *websocket.Conn
	writeMu            sync.Mutex
}

func newLocalPTYControlConnection(ctx context.Context, cancel context.CancelFunc, controllerDeviceID string, socket *websocket.Conn) *localPTYControlConnection {
	return &localPTYControlConnection{
		id:                 "local_pty_" + randomID(12),
		controllerDeviceID: controllerDeviceID,
		ctx:                ctx,
		cancel:             cancel,
		socket:             socket,
	}
}

func (c *localPTYControlConnection) connectionID() string {
	if c == nil {
		return ""
	}
	return c.id
}

func (c *localPTYControlConnection) controllerID() string {
	if c == nil {
		return ""
	}
	return c.controllerDeviceID
}

func (c *localPTYControlConnection) requestContext() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *localPTYControlConnection) writePlain(frame controlPlainFrame) {
	if c == nil || c.socket == nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	switch frame.Type {
	case terminalFrameOutput:
		if payload := terminalOutputSocketPayload(frame.Terminal); payload != nil {
			_ = c.writeJSON(payload)
		}
	case terminalFrameHeartbeat:
		if payload := terminalHeartbeatSocketPayload(frame.Terminal); payload != nil {
			_ = c.writeJSON(payload)
		}
	case terminalFrameClosed:
		_ = c.writeJSON(terminalExitSocketPayload(frame.Terminal))
		if c.cancel != nil {
			c.cancel()
		}
	case "response":
		if frame.Response != nil && !frame.Response.OK {
			_ = c.writeJSON(terminalControlResponseErrorPayload(*frame.Response))
		}
	}
}

func (c *localPTYControlConnection) writeJSON(payload any) error {
	if c == nil || c.socket == nil {
		return errors.New("terminal socket is closed")
	}
	_ = c.socket.SetWriteDeadline(time.Now().Add(terminalLocalSocketWriteTimeout))
	err := c.socket.WriteJSON(payload)
	_ = c.socket.SetWriteDeadline(time.Time{})
	if err != nil {
		if c.cancel != nil {
			c.cancel()
		}
		_ = c.socket.Close()
	}
	return err
}

func terminalSocketErrorPayload(err error) map[string]any {
	payload := map[string]any{"type": "error", "message": "terminal stream error"}
	if err != nil {
		payload["message"] = err.Error()
	}
	var actionErr *actionError
	if errors.As(err, &actionErr) && actionErr.Code != "" {
		payload["code"] = actionErr.Code
	}
	return payload
}

func terminalControlResponseErrorPayload(response ControlResponse) map[string]any {
	payload := map[string]any{"type": "error", "message": controlResponseMessage(response)}
	if response.Error != nil && response.Error.Code != "" {
		payload["code"] = response.Error.Code
	}
	return payload
}

func (c *localPTYControlConnection) registerControlStream(string, context.CancelFunc) {}
func (c *localPTYControlConnection) unregisterControlStream(string)                   {}
func (c *localPTYControlConnection) cancelControlStream(string) bool                  { return false }
func (c *localPTYControlConnection) cancelAllControlStreams()                         {}

func (c *localPTYControlConnection) terminateControlConnection(code, reason string) {
	if c == nil {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.socket != nil {
		_ = c.socket.Close()
	}
}

func resolveWorkspacePath(root, queryPath string) (string, string, error) {
	if root == "" {
		return "", "", errors.New("workspace local cwd is empty")
	}
	rel := strings.TrimSpace(queryPath)
	if rel == "." || rel == "/" {
		rel = ""
	}
	if filepath.IsAbs(rel) {
		var err error
		rel, err = filepath.Rel(root, rel)
		if err != nil {
			return "", "", err
		}
	}
	target := filepath.Clean(filepath.Join(root, rel))
	resolvedRel, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	if resolvedRel == "." {
		resolvedRel = ""
	}
	if pathEscapesRoot(resolvedRel) {
		return "", "", errors.New("path escapes workspace")
	}
	return target, filepath.ToSlash(resolvedRel), nil
}

func resolveRemoteWorkspacePath(root, queryPath string) (string, string, error) {
	if root == "" {
		return "", "", errors.New("workspace remote cwd is empty")
	}
	root = remotePathClean(root)
	rel := strings.TrimSpace(queryPath)
	if rel == "." || rel == "/" {
		rel = ""
	}
	if remotePathIsAbs(rel) {
		var err error
		rel, err = remotePathRel(root, remotePathClean(rel))
		if err != nil {
			return "", "", err
		}
	}
	target := remotePathClean(remotePathJoin(root, rel))
	resolvedRel, err := remotePathRel(root, target)
	if err != nil {
		return "", "", err
	}
	if resolvedRel == "." {
		resolvedRel = ""
	}
	if pathEscapesRoot(resolvedRel) {
		return "", "", errors.New("path escapes remote workspace")
	}
	return target, filepath.ToSlash(resolvedRel), nil
}

type createSessionRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	Agent       AgentKind `json:"agent,omitempty"`
}

func (a *app) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.store.listSessions(r.URL.Query().Get("workspace_id")))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createSessionRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ss, err := a.sessions().createSession(req)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ss)
}

func (a *app) handleSessionAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if _, err := a.sessions().deleteSessionByID(parts[0]); err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if len(parts) < 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sessionID, action := parts[0], parts[1]
	switch {
	case action == "view" && r.Method == http.MethodGet:
		view, ok := a.buildSessionView(sessionID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusOK, view)
	case action == "fork" && r.Method == http.MethodPost:
		a.handleForkSession(w, sessionID, r)
	case action == "commands" && len(parts) == 2 && r.Method == http.MethodGet:
		a.handleListSessionCommands(w, sessionID)
	case action == "commands" && len(parts) == 3 && r.Method == http.MethodPost:
		var req SessionCommandRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.handleRunSessionCommand(w, sessionID, parts[2], req)
	case action == "input" && r.Method == http.MethodPost:
		var req struct {
			Input           string            `json:"input"`
			Model           string            `json:"model"`
			ReasoningEffort string            `json:"reasoning_effort"`
			PermissionMode  string            `json:"permission_mode"`
			Attachments     []InputAttachment `json:"attachments"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.handleSessionInput(w, sessionID, req.Input, TurnOptions{Model: req.Model, ReasoningEffort: req.ReasoningEffort, PermissionMode: req.PermissionMode, Attachments: sanitizeInputAttachments(req.Attachments)})
	case action == "media" && len(parts) == 4 && r.Method == http.MethodGet:
		a.handleSessionMedia(w, r, sessionID, parts[2], parts[3])
	case action == "edit-last-user-message" && r.Method == http.MethodPost:
		var req editLastUserMessageRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.handleEditLastUserMessage(w, sessionID, req)
	case action == "interrupt" && r.Method == http.MethodPost:
		a.handleSessionInterrupt(w, sessionID)
	case action == "queue" && len(parts) == 4 && parts[3] == "cancel" && r.Method == http.MethodPost:
		a.sessions().cancelQueuedTurn(sessionID, parts[2])
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case action == "queue" && len(parts) == 4 && parts[3] == "steer" && r.Method == http.MethodPost:
		if err := a.sessions().steerQueuedTurn(sessionID, parts[2]); err != nil {
			status := http.StatusConflict
			if err.Error() == "session not found" {
				status = http.StatusNotFound
			}
			if errors.Is(err, ErrSteerUnsupported) {
				status = http.StatusNotImplemented
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *app) handleSessionInput(w http.ResponseWriter, sessionID, input string, options TurnOptions) {
	result, err := a.sessions().startSessionInput(sessionID, input, options)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) handleSessionInterrupt(w http.ResponseWriter, sessionID string) {
	result, err := a.sessions().interruptSession(sessionID)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		a.handleEventsWS(w, r)
		return
	}
	if r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		a.handleEventsSSE(w, r)
		return
	}
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	beforeSeq, _ := strconv.ParseInt(r.URL.Query().Get("before_seq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events := a.store.queryEventsWindow(r.URL.Query().Get("workspace_id"), r.URL.Query().Get("session_id"), afterSeq, beforeSeq, limit)
	writeJSON(w, http.StatusOK, events)
}

func (a *app) handleEventsWS(w http.ResponseWriter, r *http.Request) {
	c, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.hub.add(c)
	defer a.hub.remove(c)
	for {
		if _, _, err := c.NextReader(); err != nil {
			return
		}
	}
}

func (a *app) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	workspaceID := r.URL.Query().Get("workspace_id")
	sessionID := r.URL.Query().Get("session_id")

	writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
	for _, ev := range a.store.queryEvents(workspaceID, sessionID, afterSeq) {
		writeSSE(w, flusher, "astral-event", ev)
	}

	client := a.hub.addSSE()
	defer a.hub.removeSSE(client)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
		case ev, ok := <-client.ch:
			if !ok {
				return
			}
			if afterSeq > 0 && ev.Seq <= afterSeq {
				continue
			}
			if workspaceID != "" && ev.WorkspaceID != workspaceID {
				continue
			}
			if sessionID != "" && ev.SessionID != sessionID {
				continue
			}
			writeSSE(w, flusher, "astral-event", ev)
		}
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, name string, payload any) {
	body, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body)
	flusher.Flush()
}

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
