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
		a.handleWorkspaceExec(w, ws, req.Command)
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
	if len(parts) != 2 || parts[1] != "connect" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ws, ok := a.store.getWorkspace(parts[0])
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	a.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.connected", Normalized: map[string]any{
		"target": ws.Target,
		"status": "connected",
	}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleWorkspaceFiles(w http.ResponseWriter, ws Workspace, queryPath string) {
	if ws.Target != "local" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file browsing is only available for local workspaces"})
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

func (a *app) handleWorkspaceExec(w http.ResponseWriter, ws Workspace, command string) {
	if ws.Target != "local" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "terminal is only available for local workspaces"})
		return
	}
	command = strings.TrimSpace(command)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
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

type ptyClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (a *app) handleWorkspacePTY(w http.ResponseWriter, r *http.Request, ws Workspace) {
	if ws.Target != "local" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pty is only available for local workspaces"})
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
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 28, Cols: 100})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
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
	if strings.HasPrefix(resolvedRel, "..") {
		return "", "", errors.New("path escapes workspace")
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
