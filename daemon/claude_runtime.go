package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	deps    runtimeDeps
	mu      sync.Mutex
	clients map[string]*claudeClient
}

type claudeClient struct {
	runtime *claudeLocalRuntime
	session Session
	cwd     string
	path    string
	remote  claudeRemoteOptions
	config  string

	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex

	active             bool
	activeDone         chan struct{}
	pendingInteraction bool
	pausedForApproval  bool
	skipQueueAfterTurn bool
	cancelRequested    bool
	cancelReason       string
	cancelHidden       bool
	interruptSucceeded bool
	interruptRequestID string
	endSessionID       string
	stopping           bool
	finishedTurn       bool
	idleTimer          *time.Timer
	toolStarts         map[string]AstralEvent
}

func newClaudeLocalRuntime(deps runtimeDeps) *claudeLocalRuntime {
	return &claudeLocalRuntime{
		deps:    deps,
		clients: map[string]*claudeClient{},
	}
}

func (r *claudeLocalRuntime) ensureDeps() {
}

func (r *claudeLocalRuntime) StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error {
	r.ensureDeps()
	info := r.deps.agents[AgentClaude]
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
		callErr := r.deps.ssh.Call(ctx, workspace, "hello", map[string]any{}, &hello)
		if callErr != nil {
			cancel()
			return callErr
		}
		cwd = workspace.LocalProjectionRoot
		remoteCWD = remotePathClean(workspace.SSH.RemoteCWD)
		cancel()
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := r.deps.syncRemoteSkillTree(syncCtx, workspace, ".claude/skills", filepath.Join(workspace.LocalProjectionRoot, ".claude", "skills"))
		syncCancel()
		if err != nil {
			return err
		}
		mcpConfigPath, err = r.deps.writeClaudeRemoteMCPConfig(workspace)
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

	remote := claudeRemoteOptions{
		MCPConfigPath:  mcpConfigPath,
		RemoteCWD:      remoteCWD,
		AppendPrompt:   appendPrompt,
		SettingSources: settingSources,
	}
	args, argErr := r.claudeArgs(session, options, remote)
	if argErr != nil {
		return argErr
	}
	config, configErr := claudeClientConfig(info.Path, cwd, options, remote)
	if configErr != nil {
		return configErr
	}

	client, err := r.clientForTurn(session, cwd, info.Path, args, config, remote)
	if err != nil {
		return err
	}
	return client.startTurn(session, input, options)
}

func claudeRemotePermissionMode(mode string) string {
	if strings.TrimSpace(mode) == "plan" {
		return "plan"
	}
	return "bypassPermissions"
}

func (r *claudeLocalRuntime) Interrupt(sessionID string) error {
	r.ensureDeps()
	r.mu.Lock()
	client := r.clients[sessionID]
	r.mu.Unlock()
	if client == nil || !client.isActive() {
		return ErrSessionIdle
	}
	session, _ := r.deps.store.getSession(sessionID)
	return client.requestInterrupt(session, "user", false, false)
}

func (r *claudeLocalRuntime) StopSession(sessionID string, reason string) {
	r.ensureDeps()
	r.mu.Lock()
	client := r.clients[sessionID]
	delete(r.clients, sessionID)
	r.mu.Unlock()
	if client == nil {
		return
	}
	if err := client.stop(reason, claudeEndSessionTimeout); err != nil {
		session, _ := r.deps.store.getSession(sessionID)
		r.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.warning", Normalized: eventNormalized("control.warning", map[string]any{
			"message": err.Error(),
			"reason":  reason,
		})})
	}
}

func (r *claudeLocalRuntime) Steer(sessionID string, input string, options TurnOptions) error {
	r.ensureDeps()
	r.mu.Lock()
	client := r.clients[sessionID]
	r.mu.Unlock()
	if client == nil || !client.isActive() {
		return ErrSessionIdle
	}

	session, ok := r.deps.store.getSession(sessionID)
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	workspace, ok := r.deps.store.getWorkspace(session.WorkspaceID)
	if !ok {
		return fmt.Errorf("workspace %s not found", session.WorkspaceID)
	}

	startOptions := options
	if !options.Internal && !options.SuppressUserMessage {
		r.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "message.user", Normalized: eventNormalized("message.user", displayInputNormalized(input, options))})
		startOptions.SuppressUserMessage = true
	}
	r.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: sessionID, Agent: AgentClaude, Kind: "control.steer", Normalized: eventNormalized("control.steer", map[string]any{"status": "interrupting"})})

	done, err := client.requestSteerInterrupt(session)
	if err != nil {
		return err
	}
	select {
	case <-done:
	case <-time.After(claudeInterruptTimeout + time.Second):
		return fmt.Errorf("timed out interrupting Claude turn before steering")
	}
	if updated, ok := r.deps.store.getSession(sessionID); ok {
		session = updated
	}
	return r.StartTurn(session, workspace, input, startOptions)
}

type claudeRemoteOptions struct {
	MCPConfigPath  string
	RemoteCWD      string
	AppendPrompt   string
	SettingSources string
}

const (
	claudeInterruptTimeout  = 10 * time.Second
	claudeEndSessionTimeout = 5 * time.Second
	claudeIdleTimeout       = 15 * time.Minute
)

func (r *claudeLocalRuntime) clientForTurn(session Session, cwd, path string, args []string, config string, remote claudeRemoteOptions) (*claudeClient, error) {
	for {
		r.mu.Lock()
		client := r.clients[session.ID]
		if client == nil {
			r.mu.Unlock()
			return r.launchClaudeClient(session, cwd, path, args, config, remote)
		}
		active, sameConfig := client.stateForReuse(config)
		if active {
			r.mu.Unlock()
			return nil, ErrSessionRunning
		}
		if sameConfig {
			client.cancelIdleTimer()
			r.mu.Unlock()
			return client, nil
		}
		delete(r.clients, session.ID)
		r.mu.Unlock()
		if err := client.stop("claude runtime arguments changed", claudeEndSessionTimeout); err != nil {
			r.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: AgentClaude, Kind: "control.warning", Normalized: eventNormalized("control.warning", map[string]any{
				"message": err.Error(),
				"reason":  "claude runtime arguments changed",
			})})
		}
	}
}

func (r *claudeLocalRuntime) launchClaudeClient(session Session, cwd, path string, args []string, config string, remote claudeRemoteOptions) (*claudeClient, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	if remote.RemoteCWD != "" {
		cmd.Env = appendClaudeSettingsEnv(cmd.Env)
		cmd.Env = withEnvValue(cmd.Env, "ASTRALOPS_REMOTE_CWD", remote.RemoteCWD)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := &claudeClient{
		runtime:    r,
		session:    session,
		cwd:        cwd,
		path:       path,
		remote:     remote,
		config:     config,
		cmd:        cmd,
		stdin:      stdin,
		done:       make(chan struct{}),
		toolStarts: map[string]AstralEvent{},
	}
	r.mu.Lock()
	r.clients[session.ID] = client
	r.mu.Unlock()

	var stderrText strings.Builder
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		client.scanClaudeStream(stdout)
	}()
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderrText, stderr)
	}()
	go func() {
		<-stdoutDone
		<-stderrDone
		err := cmd.Wait()
		client.processExited(err, strings.TrimSpace(stderrText.String()))
	}()
	return client, nil
}

func claudeClientConfig(path, cwd string, options TurnOptions, remote claudeRemoteOptions) (string, error) {
	allowedTools := append([]string{}, options.AllowedTools...)
	if remote.RemoteCWD != "" {
		allowedTools = append(allowedTools, claudeRemoteMCPAllowedTools()...)
	}
	permissionMode := strings.TrimSpace(options.PermissionMode)
	if permissionMode == "default" {
		permissionMode = ""
	}
	value := map[string]any{
		"path":             path,
		"cwd":              filepath.Clean(cwd),
		"model":            strings.TrimSpace(options.Model),
		"effort":           strings.TrimSpace(options.ReasoningEffort),
		"permission_mode":  permissionMode,
		"allowed_tools":    allowedTools,
		"attachment_dirs":  attachmentAllowedDirs(options.Attachments),
		"mcp_config":       remote.MCPConfigPath,
		"remote_cwd":       remote.RemoteCWD,
		"append_prompt":    remote.AppendPrompt,
		"setting_sources":  remote.SettingSources,
		"disallowed_tools": claudeRemoteNativeDisallowedTools(),
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *claudeClient) stateForReuse(config string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active, !c.stopping && c.config == config
}

func (c *claudeClient) isActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active && !c.finishedTurn
}

func (c *claudeClient) cancelIdleTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
}

func (c *claudeClient) startTurn(session Session, input string, options TurnOptions) error {
	c.runtime.ensureDeps()
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		return ErrSessionIdle
	}
	if c.active && !c.finishedTurn {
		c.mu.Unlock()
		return ErrSessionRunning
	}
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	c.session = session
	c.active = true
	c.activeDone = make(chan struct{})
	c.pendingInteraction = false
	c.pausedForApproval = false
	c.skipQueueAfterTurn = false
	c.cancelRequested = false
	c.cancelReason = ""
	c.cancelHidden = false
	c.interruptSucceeded = false
	c.interruptRequestID = ""
	c.finishedTurn = false
	c.toolStarts = map[string]AstralEvent{}
	c.mu.Unlock()

	c.runtime.deps.updateSessionStatus(session.ID, "running")
	if !options.Internal && !options.SuppressUserMessage {
		c.runtime.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: eventNormalized("message.user", displayInputNormalized(input, options))})
	}
	started := map[string]any{"status": "running"}
	if options.Internal {
		started["internal"] = true
	}
	c.runtime.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: eventNormalized("turn.started", started)})

	if err := c.writeUserInput(input, options.Attachments); err != nil {
		c.finishFailed(err.Error(), nil)
		c.forceKill()
		return nil
	}
	return nil
}

func (c *claudeClient) writeUserInput(input string, attachments []InputAttachment) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	stopping := c.stopping
	c.mu.Unlock()
	if stdin == nil || stopping {
		return fmt.Errorf("Claude stdin is not available")
	}
	return writeClaudeUserInput(stdin, input, attachments)
}

func (c *claudeClient) writeControlRequest(subtype, reason string) (string, error) {
	requestID := "claude_control_" + randomID(12)
	payload := map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]any{
			"subtype": subtype,
		},
	}
	if strings.TrimSpace(reason) != "" {
		mapValue(payload["request"])["reason"] = reason
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	stopping := c.stopping
	c.mu.Unlock()
	if stdin == nil || (stopping && subtype != "end_session") {
		return "", fmt.Errorf("Claude stdin is not available")
	}
	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return "", err
	}
	return requestID, nil
}

func (c *claudeClient) requestInterrupt(session Session, reason string, hidden bool, skipQueue bool) error {
	c.mu.Lock()
	if c.stopping || c.stdin == nil {
		c.mu.Unlock()
		return ErrSessionIdle
	}
	c.cancelRequested = true
	c.cancelReason = reason
	c.cancelHidden = hidden
	if skipQueue {
		c.skipQueueAfterTurn = true
	}
	c.mu.Unlock()

	requestID, err := c.writeControlRequest("interrupt", "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.interruptRequestID = requestID
	c.mu.Unlock()
	c.runtime.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: AgentClaude, Kind: "control.interrupt", Normalized: eventNormalized("control.interrupt", map[string]any{"status": "requested", "request_id": requestID})})
	go c.cancelTimeout(requestID)
	return nil
}

func (c *claudeClient) requestSteerInterrupt(session Session) (<-chan struct{}, error) {
	c.mu.Lock()
	done := c.activeDone
	c.mu.Unlock()
	if done == nil {
		return nil, ErrSessionIdle
	}
	if err := c.requestInterrupt(session, "steer", true, true); err != nil {
		return nil, err
	}
	return done, nil
}

func (c *claudeClient) pauseForApproval(session Session) {
	c.mu.Lock()
	if c.pausedForApproval || c.stopping {
		c.mu.Unlock()
		return
	}
	c.pausedForApproval = true
	c.pendingInteraction = true
	c.cancelRequested = true
	c.cancelReason = "requires_action"
	c.skipQueueAfterTurn = true
	c.mu.Unlock()
	_, _ = c.writeControlRequest("interrupt", "")
}

func (c *claudeClient) repeatInterruptIfCancelling(session Session) {
	c.mu.Lock()
	cancelRequested := c.cancelRequested
	paused := c.pausedForApproval
	c.mu.Unlock()
	if !cancelRequested || paused {
		return
	}
	requestID, err := c.writeControlRequest("interrupt", "")
	if err != nil {
		c.runtime.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: AgentClaude, Kind: "control.warning", Normalized: eventNormalized("control.warning", map[string]any{
			"message": err.Error(),
			"reason":  "claude interrupt retry",
		})})
		return
	}
	c.mu.Lock()
	c.interruptRequestID = requestID
	c.mu.Unlock()
}

func (c *claudeClient) cancelTimeout(requestID string) {
	time.Sleep(claudeInterruptTimeout)
	c.mu.Lock()
	stale := c.interruptRequestID != requestID
	active := c.active && !c.finishedTurn
	cancelRequested := c.cancelRequested
	c.mu.Unlock()
	if stale || !active || !cancelRequested {
		return
	}
	c.runtime.deps.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentClaude, Kind: "control.warning", Normalized: eventNormalized("control.warning", map[string]any{
		"message": "Claude interrupt timed out; terminating process",
		"reason":  "interrupt timeout",
	})})
	c.finishCancelled()
	c.forceKill()
}

func (c *claudeClient) stop(reason string, timeout time.Duration) error {
	c.cancelIdleTimer()
	c.mu.Lock()
	wasActive := c.active && !c.finishedTurn
	c.stopping = true
	c.skipQueueAfterTurn = true
	c.mu.Unlock()
	if wasActive {
		c.finishCancelledWithReason(reason, false)
	}
	requestID, err := c.writeControlRequest("end_session", reason)
	if err != nil {
		c.forceKill()
		return err
	}
	c.mu.Lock()
	c.endSessionID = requestID
	c.mu.Unlock()
	select {
	case <-c.done:
		return nil
	case <-time.After(timeout):
		c.forceKill()
		select {
		case <-c.done:
		case <-time.After(time.Second):
		}
		return fmt.Errorf("timed out ending Claude session")
	}
}

func (c *claudeClient) forceKill() {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (c *claudeClient) processExited(err error, stderrText string) {
	c.mu.Lock()
	stdin := c.stdin
	active := c.active && !c.finishedTurn
	stopping := c.stopping
	c.stdin = nil
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if active && !stopping {
		if c.shouldFinishPendingOnExit() {
			c.finishPendingInteraction()
		} else if c.shouldFinishCancelledOnExit() {
			c.finishCancelled()
		} else {
			if stderrText == "" && err != nil {
				stderrText = err.Error()
			}
			c.finishFailed(stderrText, err)
		}
	}
	c.runtime.mu.Lock()
	if c.runtime.clients[c.session.ID] == c {
		delete(c.runtime.clients, c.session.ID)
	}
	c.runtime.mu.Unlock()
	close(c.done)
}

func (c *claudeClient) shouldFinishPendingOnExit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pausedForApproval
}

func (c *claudeClient) shouldFinishCancelledOnExit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelRequested
}

func (c *claudeClient) markControlResponse(line []byte) {
	requestID, subtype, ok := claudeControlResponse(line)
	if !ok || subtype != "success" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if requestID == c.interruptRequestID {
		c.interruptSucceeded = true
	}
}

func (c *claudeClient) finishCompleted() {
	c.mu.Lock()
	pendingInteraction := c.pendingInteraction
	c.mu.Unlock()
	if pendingInteraction {
		c.finishCompletedRequiresAction()
		return
	}
	c.finishTurn("completed", "", nil)
}

func (c *claudeClient) finishCompletedRequiresAction() {
	if _, finished := c.finishTurnState(); !finished {
		return
	}
	c.runtime.deps.updateSessionStatus(c.session.ID, "idle")
	c.runtime.deps.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: c.session.Agent, Kind: "turn.completed", Normalized: eventNormalized("turn.completed", map[string]any{"status": "idle"})})
	c.runtime.deps.updateSessionStatus(c.session.ID, "requires_action")
	c.scheduleIdleStop()
}

func (c *claudeClient) finishFailed(message string, cause error) {
	c.finishTurn("failed", message, cause)
}

func (c *claudeClient) finishCancelled() {
	c.finishCancelledWithReason("", false)
}

func (c *claudeClient) finishCancelledWithReason(reason string, hidden bool) {
	c.mu.Lock()
	if reason == "" {
		reason = c.cancelReason
	}
	if !hidden {
		hidden = c.cancelHidden
	}
	c.mu.Unlock()
	c.finishTurn("cancelled", reason, nil, hidden)
}

func (c *claudeClient) finishPendingInteraction() {
	if _, finished := c.finishTurnState(); !finished {
		return
	}
	c.runtime.deps.updateSessionStatus(c.session.ID, "requires_action")
	c.scheduleIdleStop()
}

func (c *claudeClient) finishTurn(kind string, message string, cause error, flags ...bool) {
	hidden := len(flags) > 0 && flags[0]
	startQueued, finished := c.finishTurnState()
	if !finished {
		return
	}
	switch kind {
	case "completed":
		c.runtime.deps.updateSessionStatus(c.session.ID, "idle")
		c.runtime.deps.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: c.session.Agent, Kind: "turn.completed", Normalized: eventNormalized("turn.completed", map[string]any{"status": "idle"})})
	case "cancelled":
		c.runtime.deps.updateSessionStatus(c.session.ID, "idle")
		normalized := map[string]any{"status": "idle"}
		if message != "" {
			normalized["reason"] = message
		}
		if hidden {
			normalized["hidden"] = true
		}
		c.runtime.deps.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: c.session.Agent, Kind: "turn.cancelled", Normalized: eventNormalized("turn.cancelled", normalized)})
	case "failed":
		c.runtime.finishFailed(c.session, message, cause)
	}
	if startQueued {
		go c.runtime.deps.startNextQueuedTurn(c.session.ID)
	}
	c.scheduleIdleStop()
}

func (c *claudeClient) finishTurnState() (bool, bool) {
	c.mu.Lock()
	if !c.active || c.finishedTurn {
		c.mu.Unlock()
		return false, false
	}
	startQueued := !c.skipQueueAfterTurn && !c.pendingInteraction
	c.active = false
	c.finishedTurn = true
	done := c.activeDone
	c.activeDone = nil
	c.pendingInteraction = false
	c.pausedForApproval = false
	c.skipQueueAfterTurn = false
	c.cancelRequested = false
	c.cancelReason = ""
	c.cancelHidden = false
	c.interruptSucceeded = false
	c.interruptRequestID = ""
	c.toolStarts = map[string]AstralEvent{}
	c.mu.Unlock()
	if done != nil {
		close(done)
	}
	return startQueued, true
}

func (c *claudeClient) scheduleIdleStop() {
	c.mu.Lock()
	if c.stopping || c.active || c.stdin == nil {
		c.mu.Unlock()
		return
	}
	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	sessionID := c.session.ID
	c.idleTimer = time.AfterFunc(claudeIdleTimeout, func() {
		c.runtime.stopIdleClient(sessionID)
	})
	c.mu.Unlock()
}

func (r *claudeLocalRuntime) stopIdleClient(sessionID string) {
	r.mu.Lock()
	client := r.clients[sessionID]
	if client == nil {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if client.isActive() {
		return
	}
	r.mu.Lock()
	if r.clients[sessionID] != client {
		r.mu.Unlock()
		return
	}
	delete(r.clients, sessionID)
	r.mu.Unlock()
	_ = client.stop("idle timeout", claudeEndSessionTimeout)
}

func (r *claudeLocalRuntime) claudeArgs(session Session, options TurnOptions, remote claudeRemoteOptions) ([]string, error) {
	r.ensureDeps()
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--include-hook-events",
	}
	if r.deps.store.shouldResumeClaudeSession(session) {
		args = append(args, "--resume", session.NativeSessionID)
	} else if session.ForkedFromSessionID != "" {
		source, ok := r.deps.store.getSession(session.ForkedFromSessionID)
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

func (c *claudeClient) scanClaudeStream(reader io.Reader) {
	r := c.runtime
	r.ensureDeps()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	toolStarts := map[string]AstralEvent{}
	session := c.currentSession()
	visibleText := r.claudeRemoteVisibleTextStream(session)
	for scanner.Scan() {
		session = c.currentSession()
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineBytes := []byte(line)
		c.markControlResponse(lineBytes)
		resultLine := claudeLineType([]byte(line)) == "result"
		resultError := claudeResultErrorMessage(lineBytes)
		if c.suppressUntilResult() && !resultLine {
			continue
		}
		for _, ev := range normalizeClaudeStreamJSON(session, lineBytes) {
			ev = r.remapProjectionEventPaths(ev)
			for _, visibleEvent := range r.prepareClaudeVisibleEvent(session, ev, visibleText) {
				if visibleEvent.Kind == "approval.requested" || visibleEvent.Kind == "ask.requested" {
					c.markPendingInteraction()
					r.deps.updateSessionStatus(session.ID, "requires_action")
				}
				r.deps.emit(visibleEvent)
				if visibleEvent.Kind == "approval.requested" || visibleEvent.Kind == "ask.requested" {
					if visibleEvent.Kind == "ask.requested" && isClaudeAskUserQuestionEvent(visibleEvent) {
						c.pauseForApproval(session)
					}
				}
				if visibleEvent.Kind == "tool.started" {
					if id := stringValue(mapValue(visibleEvent.Normalized)["id"]); id != "" {
						toolStarts[id] = visibleEvent
					}
					c.repeatInterruptIfCancelling(session)
				}
				if approval, ok := claudeApprovalFromToolResult(session, visibleEvent, toolStarts); ok {
					approval = r.remapProjectionEventPaths(approval)
					r.deps.updateSessionStatus(session.ID, "requires_action")
					r.deps.emit(approval)
					c.markPendingInteraction()
					c.pauseForApproval(session)
				}
			}
		}
		if resultLine {
			c.handleResult(resultError)
			toolStarts = map[string]AstralEvent{}
		}
	}
	if err := scanner.Err(); err != nil {
		c.finishFailed(err.Error(), nil)
	}
}

func (c *claudeClient) currentSession() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *claudeClient) suppressUntilResult() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pausedForApproval
}

func (c *claudeClient) markPendingInteraction() {
	c.mu.Lock()
	c.pendingInteraction = true
	c.mu.Unlock()
}

func (c *claudeClient) handleResult(resultError string) {
	c.mu.Lock()
	pausedForApproval := c.pausedForApproval
	cancelRequested := c.cancelRequested
	c.mu.Unlock()
	if pausedForApproval {
		c.finishPendingInteraction()
		return
	}
	if cancelRequested {
		c.finishCancelled()
		return
	}
	if resultError != "" {
		c.finishFailed(resultError, nil)
		return
	}
	c.finishCompleted()
}

func (r *claudeLocalRuntime) claudeRemoteVisibleTextStream(session Session) *claudeVisibleTextStream {
	r.ensureDeps()
	ws, ok := r.deps.store.getWorkspace(session.WorkspaceID)
	if !ok || ws.Target != "ssh" || ws.SSH == nil {
		return nil
	}
	return &claudeVisibleTextStream{
		localRoot:  filepath.Clean(ws.LocalProjectionRoot),
		remoteRoot: remotePathClean(ws.SSH.RemoteCWD),
	}
}

func (r *claudeLocalRuntime) prepareClaudeVisibleEvent(session Session, ev AstralEvent, stream *claudeVisibleTextStream) []AstralEvent {
	r.ensureDeps()
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
		ev.Normalized = eventNormalized(ev.Kind, next)
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

func claudeControlResponse(line []byte) (string, string, bool) {
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return "", "", false
	}
	if stringValue(raw["type"]) != "control_response" {
		return "", "", false
	}
	response := mapValue(raw["response"])
	requestID := stringValue(response["request_id"])
	subtype := stringValue(response["subtype"])
	if requestID == "" {
		return "", "", false
	}
	return requestID, subtype, true
}

func (r *claudeLocalRuntime) remapProjectionEventPaths(ev AstralEvent) AstralEvent {
	r.ensureDeps()
	ws, ok := r.deps.store.getWorkspace(ev.WorkspaceID)
	if !ok || ws.Target != "ssh" || ws.SSH == nil {
		return ev
	}
	ev.Normalized = eventNormalized(ev.Kind, remapProjectionValue(ev.Normalized, filepath.Clean(ws.LocalProjectionRoot), remotePathClean(ws.SSH.RemoteCWD)))
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
	r.ensureDeps()
	if message == "" && cause != nil {
		message = cause.Error()
	}
	if message == "" {
		message = "claude turn failed"
	}
	r.deps.updateSessionStatus(session.ID, "failed")
	r.deps.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.failed", Normalized: eventNormalized("turn.failed", map[string]any{
		"status":  "failed",
		"message": message,
	})})
}
