package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type claudeLocalRuntime struct {
	app     *app
	mu      sync.Mutex
	running map[string]*claudeRun
}

type claudeRun struct {
	cancel            context.CancelFunc
	stdin             io.WriteCloser
	done              chan struct{}
	mu                sync.Mutex
	pausedForApproval bool
	skipQueueAfterRun bool
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
	mcpConfigPath := ""
	appendPrompt := ""
	settingSources := ""
	if workspace.Target == "ssh" {
		if workspace.SSH == nil || strings.TrimSpace(workspace.SSH.RemoteCWD) == "" {
			return fmt.Errorf("ssh workspace remote cwd is empty")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		var hello map[string]any
		callErr := r.app.ssh.call(ctx, workspace, "hello", map[string]any{}, &hello)
		if callErr != nil {
			cancel()
			return callErr
		}
		cwd = workspace.LocalProjectionRoot
		remoteCWD = remotePathClean(workspace.SSH.RemoteCWD)
		cancel()
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := r.app.syncRemoteSkillTree(syncCtx, workspace, ".claude/skills", filepath.Join(workspace.LocalProjectionRoot, ".claude", "skills"))
		syncCancel()
		if err != nil {
			return err
		}
		mcpConfigPath, err = r.app.writeClaudeRemoteMCPConfig(workspace)
		if err != nil {
			return err
		}
		appendPrompt = claudeRemoteAppendPrompt(remoteCWD)
		settingSources = "project,local"
		options.PermissionMode = claudeRemotePermissionMode(options.PermissionMode)
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
	run := &claudeRun{cancel: cancel, done: make(chan struct{})}
	r.running[session.ID] = run
	r.mu.Unlock()

	r.app.store.updateSessionStatus(session.ID, "running")
	if !options.Internal && !options.SuppressUserMessage {
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: displayInputNormalized(input, options)})
	}
	started := map[string]any{"status": "running"}
	if options.Internal {
		started["internal"] = true
	}
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: started})

	go r.runClaude(ctx, session, cwd, info.Path, input, options, run, claudeRemoteOptions{
		MCPConfigPath:  mcpConfigPath,
		RemoteCWD:      remoteCWD,
		AppendPrompt:   appendPrompt,
		SettingSources: settingSources,
	})
	return nil
}

func claudeRemotePermissionMode(mode string) string {
	if strings.TrimSpace(mode) == "plan" {
		return "plan"
	}
	return "bypassPermissions"
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

	session, ok := r.app.store.getSession(sessionID)
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	workspace, ok := r.app.store.getWorkspace(session.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", session.WorkspaceID)
	}

	run.mu.Lock()
	run.skipQueueAfterRun = true
	if run.stdin != nil {
		_ = run.stdin.Close()
		run.stdin = nil
	}
	cancel := run.cancel
	done := run.done
	run.mu.Unlock()

	startOptions := options
	if !options.Internal && !options.SuppressUserMessage {
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "message.user", Normalized: displayInputNormalized(input, options)})
		startOptions.SuppressUserMessage = true
	}
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.steer", Normalized: map[string]any{"status": "interrupting"}})
	r.app.store.updateSessionStatus(sessionID, "idle")
	r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "turn.cancelled", Normalized: map[string]any{"status": "idle", "reason": "steer", "hidden": true}})
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timed out interrupting Claude turn before steering")
	}
	if updated, ok := r.app.store.getSession(sessionID); ok {
		session = updated
	}
	return r.StartTurn(session, workspace, input, startOptions)
}

func (run *claudeRun) pauseForApproval() {
	run.mu.Lock()
	run.pausedForApproval = true
	run.skipQueueAfterRun = true
	if run.stdin != nil {
		_ = run.stdin.Close()
		run.stdin = nil
	}
	cancel := run.cancel
	run.mu.Unlock()
	cancel()
}

type claudeRemoteOptions struct {
	MCPConfigPath  string
	RemoteCWD      string
	AppendPrompt   string
	SettingSources string
}

func (r *claudeLocalRuntime) runClaude(ctx context.Context, session Session, cwd, path, input string, options TurnOptions, run *claudeRun, remote claudeRemoteOptions) {
	defer func() {
		run.mu.Lock()
		startQueued := !run.skipQueueAfterRun
		run.mu.Unlock()
		r.mu.Lock()
		delete(r.running, session.ID)
		r.mu.Unlock()
		close(run.done)
		if startQueued {
			go r.app.startNextQueuedTurn(session.ID)
		}
	}()

	args, argErr := r.claudeArgs(session, options, remote)
	if argErr != nil {
		r.finishFailed(session, argErr.Error(), nil)
		return
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	if remote.RemoteCWD != "" {
		cmd.Env = appendClaudeSettingsEnv(cmd.Env)
		cmd.Env = withEnvValue(cmd.Env, "ASTRALOPS_REMOTE_CWD", remote.RemoteCWD)
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
	if err := writeClaudeUserInput(stdin, input, options.Attachments); err != nil {
		r.finishFailed(session, err.Error(), nil)
		_ = cmd.Process.Kill()
		return
	}

	var stderrText strings.Builder
	var completedMu sync.Mutex
	completed := false
	failTurn := func(message string, cause error) {
		completedMu.Lock()
		if completed {
			completedMu.Unlock()
			return
		}
		completed = true
		completedMu.Unlock()
		r.finishFailed(session, message, cause)
	}
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
		r.scanClaudeStream(ctx, session, stdout, run, completeTurn, failTurn)
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
		failTurn(strings.TrimSpace(stderrText.String()), err)
		return
	}
	completeTurn()
}

func (r *claudeLocalRuntime) claudeArgs(session Session, options TurnOptions, remote claudeRemoteOptions) ([]string, error) {
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
	} else if session.ForkedFromSessionID != "" {
		source, ok := r.app.store.getSession(session.ForkedFromSessionID)
		if !ok || source.NativeSessionID == "" {
			return nil, fmt.Errorf("source Claude session is not available for fork")
		}
		if session.ForkedFromNativeAnchor == "" {
			return nil, fmt.Errorf("Claude fork is missing native message anchor")
		}
		args = append(args,
			"--resume", source.NativeSessionID,
			"--resume-session-at", session.ForkedFromNativeAnchor,
			"--fork-session",
			"--session-id", session.NativeSessionID,
		)
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
	for _, dir := range attachmentAllowedDirs(options.Attachments) {
		args = append(args, "--add-dir", dir)
	}
	if len(options.AllowedTools) > 0 {
		allowed := append([]string{}, options.AllowedTools...)
		if remote.RemoteCWD != "" {
			allowed = append(allowed, claudeRemoteMCPAllowedTools()...)
		}
		args = append(args, "--allowedTools", strings.Join(allowed, ","))
	} else if remote.RemoteCWD != "" {
		args = append(args, "--allowedTools", strings.Join(claudeRemoteMCPAllowedTools(), ","))
	}
	if remote.SettingSources != "" {
		args = append(args, "--setting-sources", remote.SettingSources)
	}
	if remote.MCPConfigPath != "" {
		args = append(args, "--disallowedTools", strings.Join(claudeRemoteNativeDisallowedTools(), ","))
		args = append(args, "--mcp-config", remote.MCPConfigPath, "--strict-mcp-config")
	}
	if remote.AppendPrompt != "" {
		args = append(args, "--append-system-prompt", remote.AppendPrompt)
		args = append(args, "--exclude-dynamic-system-prompt-sections")
	}
	return args, nil
}

func writeClaudeUserInput(writer io.Writer, input string, attachments []InputAttachment) error {
	content := []map[string]any{{
		"type": "text",
		"text": inputWithAttachmentManifest(input, attachments),
	}}
	content = append(content, claudeImageContentBlocks(attachments)...)
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
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

func claudeResultErrorMessage(line []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return ""
	}
	if stringValue(raw["type"]) != "result" || !boolValue(raw["is_error"]) {
		return ""
	}
	if message := strings.TrimSpace(stringValue(raw["result"])); message != "" {
		return message
	}
	if status := stringValue(raw["api_error_status"]); status != "" {
		return "Claude API error: " + status
	}
	return "Claude turn failed"
}

func appendClaudeSettingsEnv(env []string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return env
	}
	settings := mergeJSONSettings(
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "settings.local.json"),
	)
	values, ok := settings["env"].(map[string]any)
	if !ok {
		return env
	}
	for key, value := range values {
		text := stringValue(value)
		if strings.TrimSpace(key) == "" || text == "" {
			continue
		}
		env = withEnvValue(env, key, text)
	}
	return env
}

func (r *claudeLocalRuntime) scanClaudeStream(ctx context.Context, session Session, reader io.Reader, run *claudeRun, completeTurn func(), failTurn func(string, error)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	toolStarts := map[string]AstralEvent{}
	pendingInteraction := false
	visibleText := r.claudeRemoteVisibleTextStream(session)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resultLine := claudeLineType([]byte(line)) == "result"
		resultError := claudeResultErrorMessage([]byte(line))
		for _, ev := range normalizeClaudeStreamJSON(session, []byte(line)) {
			ev = r.remapProjectionEventPaths(ev)
			for _, visibleEvent := range r.prepareClaudeVisibleEvent(session, ev, visibleText) {
				r.app.emit(visibleEvent)
				if visibleEvent.Kind == "approval.requested" || visibleEvent.Kind == "ask.requested" {
					pendingInteraction = true
					r.app.store.updateSessionStatus(session.ID, "requires_action")
					if visibleEvent.Kind == "ask.requested" && isClaudeAskUserQuestionEvent(visibleEvent) {
						run.pauseForApproval()
						return
					}
				}
				if visibleEvent.Kind == "tool.started" {
					if id := stringValue(mapValue(visibleEvent.Normalized)["id"]); id != "" {
						toolStarts[id] = visibleEvent
					}
				}
				if approval, ok := claudeApprovalFromToolResult(session, visibleEvent, toolStarts); ok {
					r.app.emit(r.remapProjectionEventPaths(approval))
					r.app.store.updateSessionStatus(session.ID, "requires_action")
					run.pauseForApproval()
					return
				}
			}
		}
		if resultLine {
			if resultError != "" {
				failTurn(resultError, nil)
				return
			}
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
		failTurn(err.Error(), nil)
	}
}

func (r *claudeLocalRuntime) claudeRemoteVisibleTextStream(session Session) *claudeVisibleTextStream {
	ws, ok := r.app.store.getWorkspace(session.WorkspaceID)
	if !ok || ws.Target != "ssh" || ws.SSH == nil {
		return nil
	}
	return &claudeVisibleTextStream{
		localRoot:  filepath.Clean(ws.LocalProjectionRoot),
		remoteRoot: remotePathClean(ws.SSH.RemoteCWD),
	}
}

func (r *claudeLocalRuntime) prepareClaudeVisibleEvent(session Session, ev AstralEvent, stream *claudeVisibleTextStream) []AstralEvent {
	if stream == nil {
		return []AstralEvent{ev}
	}
	if ev.Kind == "message.delta" {
		value := mapValue(ev.Normalized)
		text := stringValue(value["text"])
		if text == "" {
			return []AstralEvent{ev}
		}
		out := stream.Push(text)
		if out == "" {
			return nil
		}
		next := copyStringAny(value)
		next["text"] = out
		ev.Normalized = next
		return []AstralEvent{ev}
	}
	if out := stream.Flush(); out != "" {
		return []AstralEvent{
			baseClaudeEvent(session, "message.delta", map[string]any{"source": "claude", "text": out}, ev.Raw),
			ev,
		}
	}
	return []AstralEvent{ev}
}

type claudeVisibleTextStream struct {
	localRoot  string
	remoteRoot string
	pending    string
}

func (s *claudeVisibleTextStream) Push(text string) string {
	s.pending += text
	return ""
}

func (s *claudeVisibleTextStream) Flush() string {
	if s.pending == "" {
		return ""
	}
	out := sanitizeClaudeRemoteVisibleText(s.pending, s.localRoot, s.remoteRoot)
	s.pending = ""
	return out
}

func sanitizeClaudeRemoteVisibleText(text, localRoot, remoteRoot string) string {
	text = remapProjectionString(text, localRoot, remoteRoot)
	text = strings.ReplaceAll(text, ".astralops/remote-abs/", "/")
	text = strings.ReplaceAll(text, ".astralops/remote-abs", "")
	text = strings.ReplaceAll(text, "本地文件系统查找，不走 AstralOps 路径映射", "远端 cwd 内文件应使用相对路径")
	text = strings.ReplaceAll(text, "因为 Edit 在本地文件系统查找，不走 AstralOps 路径映射", "因为 Edit 对远端 cwd 内文件应使用相对路径")
	text = strings.ReplaceAll(text, "映射路径", "远端路径")
	text = strings.ReplaceAll(text, "映射到", "对应到")
	return text
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
	return ev
}

func claudeRemoteAppendPrompt(remoteCWD string) string {
	return "This is an SSH workspace on the remote machine. The remote current working directory is " + remoteCWD + ". Use the AstralOps remote MCP tools for all file, search, edit, and shell work: read, write, edit, multiedit, glob, grep, and bash. Native Claude Code file and shell tools are intentionally unavailable in this SSH workspace. Treat AstralOps remote tool results as the source of truth. In final user-facing text, describe files by their remote paths only and do not mention local projection paths, hidden handles, or path mapping internals unless the user explicitly asks."
}

func remapProjectionValue(value any, localRoot string, remoteRoot string) any {
	switch typed := value.(type) {
	case string:
		return remapProjectionString(typed, localRoot, remoteRoot)
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

func remapProjectionString(value, localRoot, remoteRoot string) string {
	for _, alias := range claudeProjectionRootAliases(localRoot) {
		value = strings.ReplaceAll(value, alias, remoteRoot)
	}
	return value
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
