package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type claudeLocalRuntime struct {
	app     *app
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newClaudeLocalRuntime(a *app) *claudeLocalRuntime {
	return &claudeLocalRuntime{
		app:     a,
		running: map[string]context.CancelFunc{},
	}
}

func (r *claudeLocalRuntime) StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error {
	info := r.app.agents[AgentClaude]
	if !info.Available || info.Path == "" {
		return fmt.Errorf("claude executable was not found on PATH")
	}
	cwd := strings.TrimSpace(workspace.LocalCWD)
	remoteCWD := ""
	settingsPath := ""
	appendPrompt := ""
	if workspace.Target == "ssh" {
		if workspace.SSH == nil || strings.TrimSpace(workspace.SSH.RemoteCWD) == "" {
			return fmt.Errorf("ssh workspace remote cwd is empty")
		}
		if _, _, err := r.app.ssh.proxyFor(context.Background(), workspace); err != nil {
			return err
		}
		cwd = workspace.LocalProjectionRoot
		remoteCWD = filepath.Clean(workspace.SSH.RemoteCWD)
		var err error
		settingsPath, err = r.app.writeClaudeRemoteSettings(workspace)
		if err != nil {
			return err
		}
		appendPrompt = "You are operating inside a transparent SSH workspace. Treat the current working directory as the remote path " + remoteCWD + ". Do not mention or expose the local sparse projection path. File reads, searches, shell commands, and edits are mirrored through AstralOps to the remote machine. Use remote absolute paths when referring to files."
	}
	if cwd == "" {
		return fmt.Errorf("workspace cwd is empty")
	}
	if session.NativeSessionID == "" {
		return fmt.Errorf("session is missing native Claude session id")
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if _, ok := r.running[session.ID]; ok {
		r.mu.Unlock()
		cancel()
		return ErrSessionRunning
	}
	r.running[session.ID] = cancel
	r.mu.Unlock()

	r.app.store.updateSessionStatus(session.ID, "running")
	if !options.Internal {
		displayInput := input
		if strings.TrimSpace(options.DisplayInput) != "" {
			displayInput = options.DisplayInput
		}
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": displayInput}})
	}
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: map[string]any{"status": "running"}})

	go r.runClaude(ctx, session, cwd, info.Path, input, options, claudeRemoteOptions{
		SettingsPath: settingsPath,
		RemoteCWD:    remoteCWD,
		AppendPrompt: appendPrompt,
	})
	return nil
}

func (r *claudeLocalRuntime) Interrupt(sessionID string) error {
	r.mu.Lock()
	cancel, ok := r.running[sessionID]
	if ok {
		delete(r.running, sessionID)
	}
	r.mu.Unlock()
	if !ok {
		return ErrSessionIdle
	}
	cancel()
	session, _ := r.app.store.getSession(sessionID)
	r.app.store.updateSessionStatus(sessionID, "idle")
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.interrupt", Normalized: map[string]any{"status": "requested"}})
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "turn.cancelled", Normalized: map[string]any{"status": "idle"}})
	return nil
}

type claudeRemoteOptions struct {
	SettingsPath string
	RemoteCWD    string
	AppendPrompt string
}

func (r *claudeLocalRuntime) runClaude(ctx context.Context, session Session, cwd, path, input string, options TurnOptions, remote claudeRemoteOptions) {
	defer func() {
		r.mu.Lock()
		delete(r.running, session.ID)
		r.mu.Unlock()
		go r.app.startNextQueuedTurn(session.ID)
	}()

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--include-hook-events",
	}
	if r.app.store.hasEventKind(session.ID, "session.native") {
		args = append(args, "--resume", session.NativeSessionID)
	} else {
		args = append(args, "--session-id", session.NativeSessionID)
	}
	if model := strings.TrimSpace(options.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(options.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if mode := strings.TrimSpace(options.PermissionMode); mode != "" && mode != "default" {
		args = append(args, "--permission-mode", mode)
	}
	if remote.SettingsPath != "" {
		args = append(args, "--settings", remote.SettingsPath)
	}
	if remote.AppendPrompt != "" {
		args = append(args, "--append-system-prompt", remote.AppendPrompt)
		args = append(args, "--exclude-dynamic-system-prompt-sections")
	}
	args = append(args, input)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	if remote.RemoteCWD != "" {
		cmd.Env = append(os.Environ(), "ASTRALOPS_REMOTE_CWD="+remote.RemoteCWD)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.finishFailed(session, err.Error(), nil)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.finishFailed(session, err.Error(), nil)
		return
	}
	if err := cmd.Start(); err != nil {
		r.finishFailed(session, err.Error(), nil)
		return
	}

	var stderrText strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.scanClaudeStream(ctx, session, stdout)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrText, stderr)
	}()
	wg.Wait()

	err = cmd.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err != nil {
		r.finishFailed(session, strings.TrimSpace(stderrText.String()), err)
		return
	}
	r.app.store.updateSessionStatus(session.ID, "idle")
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: map[string]any{"status": "idle"}})
}

func (r *claudeLocalRuntime) scanClaudeStream(ctx context.Context, session Session, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		for _, ev := range normalizeClaudeStreamJSON(session, []byte(line)) {
			ev = r.remapProjectionEventPaths(ev)
			r.app.emit(ev)
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		r.finishFailed(session, err.Error(), nil)
	}
}

func (r *claudeLocalRuntime) remapProjectionEventPaths(ev AstralEvent) AstralEvent {
	ws, ok := r.app.store.getWorkspace(ev.WorkspaceID)
	if !ok || ws.Target != "ssh" || ws.SSH == nil {
		return ev
	}
	ev.Normalized = remapProjectionValue(ev.Normalized, filepath.Clean(ws.LocalProjectionRoot), filepath.Clean(ws.SSH.RemoteCWD))
	return ev
}

func remapProjectionValue(value any, localRoot string, remoteRoot string) any {
	switch typed := value.(type) {
	case string:
		return strings.ReplaceAll(typed, localRoot, remoteRoot)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = remapProjectionValue(item, localRoot, remoteRoot)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = remapProjectionValue(item, localRoot, remoteRoot)
		}
		return out
	default:
		return value
	}
}

func (r *claudeLocalRuntime) finishFailed(session Session, message string, cause error) {
	if message == "" && cause != nil {
		message = cause.Error()
	}
	if message == "" {
		message = "claude turn failed"
	}
	r.app.store.updateSessionStatus(session.ID, "failed")
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.failed", Normalized: map[string]any{
		"status":  "failed",
		"message": message,
	}})
}
