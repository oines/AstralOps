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
	"time"

	"github.com/creack/pty"
)

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
		ws, err := a.store.createWorkspace(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.emit(AstralEvent{WorkspaceID: ws.ID, SessionID: "", Agent: ws.Agent, Kind: "workspace.created", Normalized: ws})
		writeJSON(w, http.StatusCreated, ws)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *app) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/workspaces/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		ws, ok := a.store.getWorkspace(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		a.stopWorkspaceSessions(ws.ID, "workspace deleted")
		if ws.Target == "ssh" {
			a.ssh.disconnect(ws)
		}
		a.store.deleteWorkspace(parts[0])
		a.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.removed", Normalized: map[string]any{"workspace_id": ws.ID}})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (a *app) handleWorkspacePTY(w http.ResponseWriter, r *http.Request, ws Workspace) {
	if !terminalAvailableOnHost() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": windowsTerminalDisabledReason})
		return
	}
	if ws.Target == "ssh" {
		a.handleRemoteWorkspacePTY(w, r, ws)
		return
	}
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = ws.LocalCWD
	cmd.Env = terminalEnv(os.Environ())
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 28, Cols: 100})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer func() {
		_ = killCommandProcessGroup(cmd)
		_ = ptmx.Close()
		_, _ = cmd.Process.Wait()
	}()

	_ = conn.WriteJSON(map[string]any{
		"type":  "ready",
		"shell": filepath.Base(shell),
		"cwd":   ws.LocalCWD,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteJSON(map[string]any{"type": "output", "data": string(buf[:n])}); writeErr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteJSON(map[string]any{"type": "exit"})
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}
		var message ptyClientMessage
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "input":
			_, _ = ptmx.Write([]byte(message.Data))
		case "resize":
			if message.Cols > 0 && message.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: message.Rows, Cols: message.Cols})
			}
		case "close":
			return
		}
	}
}

func (a *app) handleRemoteWorkspacePTY(w http.ResponseWriter, r *http.Request, ws Workspace) {
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ptyID := "pty_" + randomID(12)
	_, events, unsubscribe, started, err := a.ssh.startPTY(ctx, ws, ptyID, map[string]any{"cwd": ws.SSH.RemoteCWD, "cols": 100, "rows": 28})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer unsubscribe()
	_ = conn.WriteJSON(map[string]any{"type": "ready", "shell": stringValue(started["shell"]), "cwd": ws.SSH.RemoteCWD})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range events {
			switch event.Event {
			case "output":
				if err := conn.WriteJSON(map[string]any{"type": "output", "data": stringValue(event.Result["data"])}); err != nil {
					return
				}
			case "exit":
				_ = conn.WriteJSON(map[string]any{"type": "exit", "exit_code": int(numberValue(event.Result["exit_code"]))})
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		var message ptyClientMessage
		if err := conn.ReadJSON(&message); err != nil {
			_ = a.ssh.call(context.Background(), ws, "pty_kill", map[string]any{"id": ptyID}, nil)
			return
		}
		switch message.Type {
		case "input":
			_ = a.ssh.call(context.Background(), ws, "pty_write", map[string]any{"id": ptyID, "data": message.Data}, nil)
		case "resize":
			_ = a.ssh.call(context.Background(), ws, "pty_resize", map[string]any{"id": ptyID, "cols": message.Cols, "rows": message.Rows}, nil)
		case "close":
			_ = a.ssh.call(context.Background(), ws, "pty_kill", map[string]any{"id": ptyID}, nil)
			return
		}
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
	ws, ok := a.store.getWorkspace(req.WorkspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	agent := req.Agent
	if agent == "" {
		agent = ws.Agent
	}
	if agent != AgentClaude && agent != AgentCodex {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent must be claude or codex"})
		return
	}
	ss := a.store.createSession(ws, agent)
	a.emit(AstralEvent{WorkspaceID: ws.ID, SessionID: ss.ID, Agent: ss.Agent, Kind: "session.started", Normalized: ss})
	writeJSON(w, http.StatusCreated, ss)
}

func (a *app) handleSessionAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		ss, ok := a.store.getSession(parts[0])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		a.stopSessionRuntime(ss, "session deleted")
		a.store.deleteSession(parts[0])
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "session.deleted", Normalized: map[string]any{"session_id": ss.ID}})
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
			Input           string `json:"input"`
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			PermissionMode  string `json:"permission_mode"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.handleSessionInput(w, sessionID, req.Input, TurnOptions{Model: req.Model, ReasoningEffort: req.ReasoningEffort, PermissionMode: req.PermissionMode})
	case action == "interrupt" && r.Method == http.MethodPost:
		a.handleSessionInterrupt(w, sessionID)
	case action == "queue" && len(parts) == 4 && parts[3] == "cancel" && r.Method == http.MethodPost:
		a.cancelQueuedTurn(sessionID, parts[2])
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case action == "queue" && len(parts) == 4 && parts[3] == "steer" && r.Method == http.MethodPost:
		if err := a.steerQueuedTurn(sessionID, parts[2]); err != nil {
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
	input = strings.TrimSpace(input)
	if input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input required"})
		return
	}

	ss, ok := a.store.getSession(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	ws, ok := a.store.getWorkspace(ss.WorkspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": "agent runtime is not implemented"}})
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
		return
	}
	if err := runtime.StartTurn(ss, ws, input, options); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			turn := a.enqueueTurn(ss, input, options)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": true, "queue_id": turn.ID})
			return
		}
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleSessionInterrupt(w http.ResponseWriter, sessionID string) {
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
		return
	}
	if err := runtime.Interrupt(sessionID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
