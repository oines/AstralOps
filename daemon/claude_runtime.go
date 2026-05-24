package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	if workspace.Target != "local" {
		return fmt.Errorf("claude runtime only supports local workspaces in this milestone")
	}
	info := r.app.agents[AgentClaude]
	if !info.Available || info.Path == "" {
		return fmt.Errorf("claude executable was not found on PATH")
	}
	cwd := strings.TrimSpace(workspace.LocalCWD)
	if cwd == "" {
		return fmt.Errorf("local workspace cwd is empty")
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

	go r.runClaude(ctx, session, cwd, info.Path, input, options)
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

func (r *claudeLocalRuntime) runClaude(ctx context.Context, session Session, cwd, path, input string, options TurnOptions) {
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
	args = append(args, input)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd

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
			r.app.emit(ev)
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		r.finishFailed(session, err.Error(), nil)
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
