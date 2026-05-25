package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var claudeBridgeExecPattern = regexp.MustCompile(`\b(?:hook_bridge\.py|claude-remote-hook)['"]?\s+['"]?exec['"]?\s+['"]?([A-Za-z0-9+/=]+)['"]?`)
var claudeBridgeFullExecPattern = regexp.MustCompile(`(?:ASTRALOPS_[A-Z_]+=(?:'[^']*'|"[^"]*"|\S+)\s+)*(?:'[^']*(?:daemon|astralops-daemon)'|"[^"]*(?:daemon|astralops-daemon)"|\S*(?:daemon|astralops-daemon))\s+claude-remote-hook\s+['"]?exec['"]?\s+['"]?([A-Za-z0-9+/=]+)['"]?`)

type claudeLocalRuntime struct {
	app     *app
	mu      sync.Mutex
	running map[string]*claudeRun
}

type claudeRun struct {
	cancel            context.CancelFunc
	stdin             io.WriteCloser
	mu                sync.Mutex
	pausedForApproval bool
}

func newClaudeLocalRuntime(a *app) *claudeLocalRuntime {
	return &claudeLocalRuntime{
		app:     a,
		running: map[string]*claudeRun{},
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
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		var hello map[string]any
		callErr := r.app.ssh.call(ctx, workspace, "hello", map[string]any{}, &hello)
		cancel()
		if callErr != nil {
			return callErr
		}
		cwd = workspace.LocalProjectionRoot
		remoteCWD = remotePathClean(workspace.SSH.RemoteCWD)
		var err error
		settingsPath, err = r.app.writeClaudeRemoteSettings(workspace)
		if err != nil {
			return err
		}
		appendPrompt = "Treat the current working directory as " + remoteCWD + ". Use absolute paths from that directory when referring to files."
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
	run := &claudeRun{cancel: cancel}
	r.running[session.ID] = run
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

	go r.runClaude(ctx, session, cwd, info.Path, input, options, run, claudeRemoteOptions{
		SettingsPath: settingsPath,
		RemoteCWD:    remoteCWD,
		AppendPrompt: appendPrompt,
	})
	return nil
}

func (r *claudeLocalRuntime) Interrupt(sessionID string) error {
	r.mu.Lock()
	run, ok := r.running[sessionID]
	if ok {
		delete(r.running, sessionID)
	}
	r.mu.Unlock()
	if !ok {
		return ErrSessionIdle
	}
	run.cancel()
	session, _ := r.app.store.getSession(sessionID)
	r.app.store.updateSessionStatus(sessionID, "idle")
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.interrupt", Normalized: map[string]any{"status": "requested"}})
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "turn.cancelled", Normalized: map[string]any{"status": "idle"}})
	return nil
}

func (r *claudeLocalRuntime) StopSession(sessionID string, reason string) {
	if err := r.Interrupt(sessionID); err != nil && !errors.Is(err, ErrSessionIdle) {
		session, _ := r.app.store.getSession(sessionID)
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.warning", Normalized: map[string]any{
			"message": err.Error(),
			"reason":  reason,
		}})
	}
}

func (r *claudeLocalRuntime) Steer(sessionID string, input string, options TurnOptions) error {
	r.mu.Lock()
	run, ok := r.running[sessionID]
	r.mu.Unlock()
	if !ok {
		return ErrSessionIdle
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.stdin == nil {
		return ErrSessionIdle
	}
	if err := writeClaudeUserInput(run.stdin, input); err != nil {
		return err
	}
	session, _ := r.app.store.getSession(sessionID)
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.steer", Normalized: map[string]any{"status": "sent"}})
	return nil
}

func (run *claudeRun) pauseForApproval() {
	run.mu.Lock()
	run.pausedForApproval = true
	if run.stdin != nil {
		_ = run.stdin.Close()
		run.stdin = nil
	}
	cancel := run.cancel
	run.mu.Unlock()
	cancel()
}

type claudeRemoteOptions struct {
	SettingsPath string
	RemoteCWD    string
	AppendPrompt string
}

func (r *claudeLocalRuntime) runClaude(ctx context.Context, session Session, cwd, path, input string, options TurnOptions, run *claudeRun, remote claudeRemoteOptions) {
	defer func() {
		r.mu.Lock()
		delete(r.running, session.ID)
		r.mu.Unlock()
		go r.app.startNextQueuedTurn(session.ID)
	}()

	args := []string{
		"-p",
		"--input-format", "stream-json",
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
	if len(options.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(options.AllowedTools, ","))
	}
	if remote.SettingsPath != "" {
		args = append(args, "--settings", remote.SettingsPath)
	}
	if remote.AppendPrompt != "" {
		args = append(args, "--append-system-prompt", remote.AppendPrompt)
		args = append(args, "--exclude-dynamic-system-prompt-sections")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	if remote.RemoteCWD != "" {
		cmd.Env = append(os.Environ(), "ASTRALOPS_REMOTE_CWD="+remote.RemoteCWD)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.finishFailed(session, err.Error(), nil)
		return
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
	run.mu.Lock()
	run.stdin = stdin
	run.mu.Unlock()
	if err := writeClaudeUserInput(stdin, input); err != nil {
		r.finishFailed(session, err.Error(), nil)
		_ = cmd.Process.Kill()
		return
	}

	var stderrText strings.Builder
	var completedMu sync.Mutex
	completed := false
	completeTurn := func() {
		completedMu.Lock()
		if completed {
			completedMu.Unlock()
			return
		}
		completed = true
		completedMu.Unlock()
		r.app.store.updateSessionStatus(session.ID, "idle")
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: map[string]any{"status": "idle"}})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.scanClaudeStream(ctx, session, stdout, run, completeTurn)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrText, stderr)
	}()
	wg.Wait()

	err = cmd.Wait()
	run.mu.Lock()
	run.stdin = nil
	run.mu.Unlock()
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err != nil {
		r.finishFailed(session, strings.TrimSpace(stderrText.String()), err)
		return
	}
	completeTurn()
}

func writeClaudeUserInput(writer io.Writer, input string) error {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": input,
			}},
		},
		"parent_tool_use_id": nil,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func (r *claudeLocalRuntime) scanClaudeStream(ctx context.Context, session Session, reader io.Reader, run *claudeRun, completeTurn func()) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	toolStarts := map[string]AstralEvent{}
	pendingInteraction := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resultLine := claudeLineType([]byte(line)) == "result"
		for _, ev := range normalizeClaudeStreamJSON(session, []byte(line)) {
			ev = r.remapProjectionEventPaths(ev)
			r.app.emit(ev)
			if ev.Kind == "approval.requested" || ev.Kind == "ask.requested" {
				pendingInteraction = true
				r.app.store.updateSessionStatus(session.ID, "requires_action")
				if ev.Kind == "ask.requested" && isClaudeAskUserQuestionEvent(ev) {
					run.pauseForApproval()
					return
				}
			}
			if ev.Kind == "tool.started" {
				if id := stringValue(mapValue(ev.Normalized)["id"]); id != "" {
					toolStarts[id] = ev
				}
			}
			if approval, ok := claudeApprovalFromToolResult(session, ev, toolStarts); ok {
				r.app.emit(r.remapProjectionEventPaths(approval))
				r.app.store.updateSessionStatus(session.ID, "requires_action")
				run.pauseForApproval()
				return
			}
		}
		if resultLine {
			completeTurn()
			if pendingInteraction {
				r.app.store.updateSessionStatus(session.ID, "requires_action")
			}
			run.mu.Lock()
			if run.stdin != nil {
				_ = run.stdin.Close()
				run.stdin = nil
			}
			run.mu.Unlock()
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		r.finishFailed(session, err.Error(), nil)
	}
}

func isClaudeAskUserQuestionEvent(ev AstralEvent) bool {
	value := mapValue(ev.Normalized)
	return stringValue(value["source"]) == "claude" && stringValue(value["kind"]) == "AskUserQuestion"
}

func claudeApprovalFromToolResult(session Session, ev AstralEvent, toolStarts map[string]AstralEvent) (AstralEvent, bool) {
	if ev.Kind != "tool.completed" {
		return AstralEvent{}, false
	}
	value := mapValue(ev.Normalized)
	if !boolValue(value["is_error"]) {
		return AstralEvent{}, false
	}
	result := firstString(value["result"], value["content"])
	if !isClaudeCommandApprovalError(result) {
		return AstralEvent{}, false
	}
	id := stringValue(value["id"])
	started := mapValue(toolStarts[id].Normalized)
	params := mapValue(started["input"])
	toolName := firstString(started["name"], "Bash")
	command := stringValue(params["command"])
	normalized := map[string]any{
		"source":      "claude",
		"kind":        "permission",
		"approval_id": id,
		"request_id":  id,
		"tool_name":   toolName,
		"params":      params,
		"reason":      claudeCommandApprovalReason(result),
	}
	if command != "" {
		normalized["command"] = command
	}
	return baseClaudeEvent(session, "approval.requested", normalized, ev.Raw), true
}

func isClaudeCommandApprovalError(result string) bool {
	return strings.Contains(result, "This command requires approval") ||
		(strings.Contains(result, "require approval") && strings.Contains(result, "contains multiple operations"))
}

func claudeCommandApprovalReason(result string) string {
	result = strings.TrimSpace(strings.TrimPrefix(result, "Error:"))
	if result == "" {
		return "This command requires approval"
	}
	return result
}

func claudeLineType(line []byte) string {
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return ""
	}
	return stringValue(raw["type"])
}

func (r *claudeLocalRuntime) remapProjectionEventPaths(ev AstralEvent) AstralEvent {
	ws, ok := r.app.store.getWorkspace(ev.WorkspaceID)
	if !ok || ws.Target != "ssh" || ws.SSH == nil {
		return ev
	}
	ev.Normalized = remapProjectionValue(ev.Normalized, filepath.Clean(ws.LocalProjectionRoot), remotePathClean(ws.SSH.RemoteCWD))
	ev.Normalized = scrubClaudeRemoteBridgeEvent(ev.Normalized, remotePathClean(ws.SSH.RemoteCWD))
	return ev
}

func scrubClaudeRemoteBridgeEvent(value any, remoteCWD string) any {
	normalized, ok := value.(map[string]any)
	if !ok {
		return value
	}
	normalized = scrubClaudeBridgeValue(normalized, remoteCWD).(map[string]any)
	if hook := stringValue(normalized["hook_event_name"]); hook == "PreToolUse" || hook == "PostToolUse" {
		normalized["hidden"] = true
		normalized["visibility"] = "debug"
		return normalized
	}
	return normalized
}

func scrubClaudeBridgeValue(value any, remoteCWD string) any {
	switch typed := value.(type) {
	case string:
		return scrubClaudeBridgeText(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = scrubClaudeBridgeValue(item, remoteCWD)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = scrubClaudeBridgeValue(item, remoteCWD)
		}
		replaceBridgeCommand(out, remoteCWD)
		return out
	default:
		return value
	}
}

func replaceBridgeCommand(value map[string]any, remoteCWD string) {
	command := stringValue(value["command"])
	if command == "" {
		return
	}
	decoded := decodeClaudeBridgeCommand(command)
	if decoded == "" {
		return
	}
	value["command"] = decoded
	if _, ok := value["cwd"]; !ok {
		value["cwd"] = remoteCWD
	}
	value["remote"] = true
}

func scrubClaudeBridgeText(text string) string {
	return claudeBridgeFullExecPattern.ReplaceAllStringFunc(text, func(match string) string {
		decoded := decodeClaudeBridgeCommand(match)
		if decoded == "" {
			return "[remote command]"
		}
		return decoded
	})
}

func decodeClaudeBridgeCommand(command string) string {
	match := claudeBridgeFullExecPattern.FindStringSubmatch(command)
	if len(match) < 2 {
		match = claudeBridgeExecPattern.FindStringSubmatch(command)
	}
	if len(match) < 2 {
		return ""
	}
	body, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		return ""
	}
	return string(body)
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
