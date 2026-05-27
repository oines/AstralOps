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

const codexRequestTimeout = 30 * time.Second

type codexLocalRuntime struct {
	app *app

	mu      sync.Mutex
	clients map[string]*codexClient
}

type codexClient struct {
	runtime       *codexLocalRuntime
	session       Session
	cwd           string
	processCWD    string
	execServerURL string
	remoteShell   string
	codexHome     string
	path          string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	closed chan struct{}

	mu           sync.Mutex
	nextID       int64
	pending      map[int64]chan codexRPCResponse
	approvals    map[string]codexPendingApproval
	items        map[string]map[string]any
	threadID     string
	activeTurn   string
	nextInternal bool
	editingLast  bool
	running      bool
	initialized  bool
	stopping     bool
}

type codexPendingApproval struct {
	RequestID any
	Method    string
	Params    map[string]any
}

type codexRPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newCodexLocalRuntime(a *app) *codexLocalRuntime {
	return &codexLocalRuntime{
		app:     a,
		clients: map[string]*codexClient{},
	}
}

func (r *codexLocalRuntime) StartTurn(session Session, workspace Workspace, input string, options TurnOptions) error {
	client, err := r.clientForSession(session, workspace)
	if err != nil {
		return err
	}
	if !client.trySetRunning() {
		return ErrSessionRunning
	}

	r.app.store.updateSessionStatus(session.ID, "running")
	if !options.Internal {
		displayInput := input
		if strings.TrimSpace(options.DisplayInput) != "" {
			displayInput = options.DisplayInput
		}
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": displayInput}})
	}

	go client.startTurn(input, options)
	return nil
}

func (r *codexLocalRuntime) RunCommand(session Session, workspace Workspace, commandID string, _ map[string]any) error {
	if commandID != "compact" {
		return fmt.Errorf("codex command %s is not implemented", commandID)
	}
	client, err := r.clientForSession(session, workspace)
	if err != nil {
		return err
	}
	if !client.trySetRunning() {
		return ErrSessionRunning
	}
	r.app.store.updateSessionStatus(session.ID, "running")
	if commandID == "compact" {
		r.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "memory.compacting", Normalized: map[string]any{
			"source":  "astralops",
			"command": "compact",
			"status":  "running",
		}})
	}

	go func() {
		if err := client.runCommand(commandID); err != nil {
			if client.isStopping() {
				return
			}
			client.finishFailed(err.Error())
			return
		}
		client.markIdle("idle")
	}()
	return nil
}

func (r *codexLocalRuntime) ForkSession(source Session, fork Session, workspace Workspace, rollbackTurns int) error {
	if strings.TrimSpace(source.NativeThreadID) == "" {
		return fmt.Errorf("source codex session is missing native thread id")
	}
	client, err := r.clientForSession(fork, workspace)
	if err != nil {
		return err
	}
	return client.forkThread(source.NativeThreadID, rollbackTurns)
}

func (r *codexLocalRuntime) EditLastUserMessageAndResend(session Session, workspace Workspace, input string, options TurnOptions) error {
	client, err := r.clientForSession(session, workspace)
	if err != nil {
		return err
	}
	client.beginLastUserMessageEdit()
	started := false
	defer func() {
		client.endLastUserMessageEdit()
		if !started {
			go r.app.startNextQueuedTurn(session.ID)
		}
	}()

	if client.isRunning() {
		if _, ok := client.waitForActiveTurn(2 * time.Second); ok {
			if err := client.interruptForEdit(); err != nil {
				return err
			}
		}
		if err := client.waitUntilIdle(20 * time.Second); err != nil {
			return err
		}
	}
	if err := client.rollbackLastTurnWithRetry(10 * time.Second); err != nil {
		return err
	}
	if updated, ok := r.app.store.getSession(session.ID); ok {
		session = updated
	}
	if err := r.StartTurn(session, workspace, input, options); err != nil {
		return err
	}
	started = true
	return nil
}

func (r *codexLocalRuntime) clientForSession(session Session, workspace Workspace) (*codexClient, error) {
	info := r.app.agents[AgentCodex]
	if !info.Available || info.Path == "" {
		return nil, fmt.Errorf("codex executable was not found on PATH")
	}
	cwd := strings.TrimSpace(workspace.LocalCWD)
	processCWD := cwd
	execServerURL := ""
	remoteShell := ""
	codexHome := ""
	remoteCodexHome := ""
	if workspace.Target == "ssh" {
		if workspace.SSH == nil || strings.TrimSpace(workspace.SSH.RemoteCWD) == "" {
			return nil, fmt.Errorf("ssh workspace remote cwd is empty")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		var hello map[string]any
		err := r.app.ssh.call(ctx, workspace, "hello", map[string]any{}, &hello)
		if err == nil {
			codexHome, err = r.app.prepareCodexRemoteHome(ctx, workspace)
		}
		if err == nil {
			var skillErr error
			remoteCodexHome, skillErr = r.app.prepareCodexRemoteBundledSkills(ctx, workspace, info.Path)
			if skillErr != nil {
				r.app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "control.warning", Normalized: map[string]any{
					"source":  "codex",
					"message": "failed to prepare Codex bundled skills on SSH remote: " + skillErr.Error(),
				}})
			}
		}
		cancel()
		if err != nil {
			return nil, err
		}
		r.app.setCodexRemoteHome(workspace.ID, remoteCodexHome)
		cwd = remotePathClean(workspace.SSH.RemoteCWD)
		processCWD = workspace.LocalProjectionRoot
		execServerURL = r.app.codexExecServerURL(workspace.ID)
		remoteShell = stringValue(hello["shell"])
	}
	if cwd == "" {
		return nil, fmt.Errorf("workspace cwd is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	client := r.clients[session.ID]
	if client == nil {
		client = newCodexClient(r, session, cwd, processCWD, execServerURL, remoteShell, codexHome, info.Path)
		r.clients[session.ID] = client
	} else {
		client.updateSession(session)
		client.updateWorkspace(cwd, processCWD, execServerURL, remoteShell, codexHome)
	}
	return client, nil
}

func (a *app) prepareCodexRemoteHome(_ context.Context, ws Workspace) (string, error) {
	home := filepath.Join(a.store.dataDir, "runtime", "codex-remote", ws.ID, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := copyCodexAuthIntoRemoteHome(home); err != nil {
		return "", err
	}
	return home, nil
}

func copyCodexAuthIntoRemoteHome(remoteHome string) error {
	sourceHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if sourceHome == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		sourceHome = filepath.Join(userHome, ".codex")
	}
	if err := os.MkdirAll(remoteHome, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"auth.json", "models_cache.json"} {
		if err := copyOptionalCodexRuntimeFile(sourceHome, remoteHome, name); err != nil {
			return err
		}
	}
	return nil
}

func copyOptionalCodexRuntimeFile(sourceHome, remoteHome, name string) error {
	body, err := os.ReadFile(filepath.Join(sourceHome, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(filepath.Join(remoteHome, name), body, 0o600)
}

func (r *codexLocalRuntime) Interrupt(sessionID string) error {
	r.mu.Lock()
	client := r.clients[sessionID]
	r.mu.Unlock()
	if client == nil || !client.isRunning() {
		return ErrSessionIdle
	}
	if err := client.interrupt(); err != nil {
		return err
	}
	return nil
}

func (r *codexLocalRuntime) StopSession(sessionID string, reason string) {
	r.mu.Lock()
	client := r.clients[sessionID]
	delete(r.clients, sessionID)
	r.mu.Unlock()
	if client == nil {
		return
	}
	client.stop(reason)
}

func (r *codexLocalRuntime) Steer(sessionID string, input string, options TurnOptions) error {
	r.mu.Lock()
	client := r.clients[sessionID]
	r.mu.Unlock()
	if client == nil || !client.isRunning() {
		return ErrSessionIdle
	}
	return client.steer(input)
}

func (r *codexLocalRuntime) RespondApproval(approvalID string, response map[string]any) error {
	r.mu.Lock()
	clients := make([]*codexClient, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	r.mu.Unlock()

	for _, client := range clients {
		if err := client.respondApproval(approvalID, response); err == nil {
			return nil
		}
	}
	return fmt.Errorf("approval %s is not pending in codex runtime", approvalID)
}

func newCodexClient(runtime *codexLocalRuntime, session Session, cwd, processCWD, execServerURL, remoteShell, codexHome, path string) *codexClient {
	if processCWD == "" {
		processCWD = cwd
	}
	return &codexClient{
		runtime:       runtime,
		session:       session,
		cwd:           cwd,
		processCWD:    processCWD,
		execServerURL: execServerURL,
		remoteShell:   remoteShell,
		codexHome:     codexHome,
		path:          path,
		closed:        make(chan struct{}),
		pending:       map[int64]chan codexRPCResponse{},
		approvals:     map[string]codexPendingApproval{},
		items:         map[string]map[string]any{},
	}
}

func (c *codexClient) startTurn(input string, options TurnOptions) {
	if err := c.ensureStarted(); err != nil {
		if c.isStopping() {
			return
		}
		c.finishFailed(err.Error())
		return
	}
	if err := c.ensureThread(); err != nil {
		if c.isStopping() {
			return
		}
		c.finishFailed(err.Error())
		return
	}

	params := map[string]any{
		"threadId": c.getThreadID(),
		"input": []map[string]any{{
			"type":          "text",
			"text":          input,
			"text_elements": []any{},
		}},
	}
	applyCodexTurnOptions(params, options, c.cwd, c.defaultModel(), c.defaultReasoningEffort())
	c.mu.Lock()
	c.nextInternal = options.Internal
	c.mu.Unlock()
	result, err := c.request("turn/start", params, codexRequestTimeout)
	if err != nil {
		c.mu.Lock()
		c.nextInternal = false
		c.mu.Unlock()
		if c.isStopping() {
			return
		}
		c.finishFailed(err.Error())
		return
	}

	if turnID := codexResultTurnID(result); turnID != "" {
		c.mu.Lock()
		c.activeTurn = turnID
		c.mu.Unlock()
	}
}

func (c *codexClient) runCommand(commandID string) error {
	if err := c.ensureStarted(); err != nil {
		return err
	}
	if err := c.ensureThread(); err != nil {
		return err
	}
	switch commandID {
	case "compact":
		_, err := c.request("thread/compact/start", map[string]any{
			"threadId": c.getThreadID(),
		}, codexRequestTimeout)
		return err
	default:
		return fmt.Errorf("codex command %s is not implemented", commandID)
	}
}

func (c *codexClient) forkThread(sourceThreadID string, rollbackTurns int) error {
	if err := c.ensureStarted(); err != nil {
		return err
	}
	if c.getThreadID() != "" {
		return nil
	}
	params := c.threadParams()
	params["threadId"] = sourceThreadID
	result, err := c.request("thread/fork", params, codexRequestTimeout)
	if err != nil {
		return err
	}
	threadID := codexResultThreadID(result)
	if threadID == "" {
		return errors.New("codex thread/fork did not return a thread id")
	}
	c.mu.Lock()
	c.threadID = threadID
	c.session.NativeThreadID = threadID
	c.mu.Unlock()
	c.runtime.app.store.updateSessionNativeThreadID(c.session.ID, threadID)

	if rollbackTurns > 0 {
		result, err = c.request("thread/rollback", map[string]any{
			"threadId": threadID,
			"numTurns": rollbackTurns,
		}, codexRequestTimeout)
		if err != nil {
			return err
		}
		if rolledThreadID := codexResultThreadID(result); rolledThreadID != "" && rolledThreadID != threadID {
			c.mu.Lock()
			c.threadID = rolledThreadID
			c.session.NativeThreadID = rolledThreadID
			c.mu.Unlock()
			c.runtime.app.store.updateSessionNativeThreadID(c.session.ID, rolledThreadID)
		}
	}
	return nil
}

func (c *codexClient) rollbackLastTurnWithRetry(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := c.rollbackLastTurn()
		if err == nil {
			return nil
		}
		canRetry := strings.Contains(err.Error(), "Cannot rollback while a turn is in progress") || isCodexProcessRestartBoundaryError(err)
		if !canRetry || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func isCodexProcessRestartBoundaryError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "codex app-server exited") || strings.Contains(text, "broken pipe")
}

func (c *codexClient) rollbackLastTurn() error {
	if err := c.ensureStarted(); err != nil {
		return err
	}
	if err := c.ensureThread(); err != nil {
		return err
	}
	threadID := c.getThreadID()
	if threadID == "" {
		return errors.New("codex thread is not available for rollback")
	}
	result, err := c.request("thread/rollback", map[string]any{
		"threadId": threadID,
		"numTurns": 1,
	}, codexRequestTimeout)
	if err != nil {
		return err
	}
	if rolledThreadID := codexResultThreadID(result); rolledThreadID != "" && rolledThreadID != threadID {
		c.mu.Lock()
		c.threadID = rolledThreadID
		c.session.NativeThreadID = rolledThreadID
		c.mu.Unlock()
		c.runtime.app.store.updateSessionNativeThreadID(c.session.ID, rolledThreadID)
	}
	return nil
}

func (c *codexClient) ensureStarted() error {
	c.mu.Lock()
	if c.initialized && c.stdin != nil {
		c.mu.Unlock()
		return nil
	}
	processClosed := make(chan struct{})
	c.closed = processClosed
	c.mu.Unlock()

	cmd := exec.Command(c.path, codexAppServerArgs(c.execServerURL != "")...)
	cmd.Dir = c.processCWD
	cmd.Env = os.Environ()
	if c.execServerURL != "" {
		cmd.Env = withEnvValue(cmd.Env, "CODEX_EXEC_SERVER_URL", c.execServerURL)
		if strings.TrimSpace(c.codexHome) != "" {
			cmd.Env = withEnvValue(cmd.Env, "CODEX_HOME", strings.TrimSpace(c.codexHome))
		}
		if strings.TrimSpace(c.remoteShell) != "" {
			cmd.Env = withEnvValue(cmd.Env, "SHELL", strings.TrimSpace(c.remoteShell))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.mu.Unlock()

	go c.scan(stdout)
	go c.scanStderr(stderr)
	go c.wait(cmd, processClosed)

	if _, err := c.request("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "AstralOps", "version": version},
		"capabilities": map[string]any{
			"supportsStreaming": true,
			"experimentalApi":   true,
		},
	}, codexRequestTimeout); err != nil {
		c.cleanupStartedProcess("initialize failed")
		return err
	}
	if err := c.notify("initialized", nil); err != nil {
		c.cleanupStartedProcess("initialized notify failed")
		return err
	}

	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()
	return nil
}

func (c *codexClient) cleanupStartedProcess(reason string) {
	c.mu.Lock()
	stdin := c.stdin
	cmd := c.cmd
	closed := c.closed
	c.stopping = true
	c.running = false
	c.activeTurn = ""
	c.initialized = false
	for id, ch := range c.pending {
		ch <- codexRPCResponse{ID: id, Error: &codexRPCError{Code: -32000, Message: reason}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if closed != nil {
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
		}
	}
}

func codexAppServerArgs(disableLocalNodeREPL bool) []string {
	args := []string{"app-server"}
	if disableLocalNodeREPL {
		args = append(args,
			"-c", "mcp_servers={}",
			"-c", "skills.bundled.enabled=false",
			"--disable", "apps",
		)
	}
	return append(args, "--listen", "stdio://")
}

func (a *app) setCodexRemoteHome(workspaceID string, remoteHome string) {
	a.codexRemoteHomeMu.Lock()
	defer a.codexRemoteHomeMu.Unlock()
	if a.codexRemoteHome == nil {
		a.codexRemoteHome = map[string]string{}
	}
	if strings.TrimSpace(remoteHome) == "" {
		delete(a.codexRemoteHome, workspaceID)
		return
	}
	a.codexRemoteHome[workspaceID] = remoteHome
}

func (a *app) getCodexRemoteHome(workspaceID string) string {
	a.codexRemoteHomeMu.Lock()
	defer a.codexRemoteHomeMu.Unlock()
	return strings.TrimSpace(a.codexRemoteHome[workspaceID])
}

func withEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		next = append(next, entry)
	}
	return append(next, prefix+value)
}

func (c *codexClient) ensureThread() error {
	if c.getThreadID() != "" {
		return nil
	}
	if nativeThreadID := c.persistedThreadID(); nativeThreadID != "" {
		params := c.threadParams()
		params["threadId"] = nativeThreadID
		result, err := c.request("thread/resume", params, codexRequestTimeout)
		if err == nil {
			threadID := codexResultThreadID(result)
			if threadID == "" {
				return errors.New("codex thread/resume did not return a thread id")
			}
			c.mu.Lock()
			c.threadID = threadID
			c.session.NativeThreadID = threadID
			c.mu.Unlock()
			c.runtime.app.store.updateSessionNativeThreadID(c.session.ID, threadID)
			return nil
		}
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: c.session.Agent, Kind: "control.warning", Normalized: map[string]any{
			"source":  "codex",
			"message": fmt.Sprintf("failed to resume codex thread %s: %s", nativeThreadID, err.Error()),
		}})
	}
	result, err := c.request("thread/start", c.threadParams(), codexRequestTimeout)
	if err != nil {
		return err
	}
	threadID := codexResultThreadID(result)
	if threadID == "" {
		return errors.New("codex thread/start did not return a thread id")
	}
	c.mu.Lock()
	c.threadID = threadID
	c.session.NativeThreadID = threadID
	c.mu.Unlock()
	c.runtime.app.store.updateSessionNativeThreadID(c.session.ID, threadID)
	return nil
}

func (c *codexClient) threadParams() map[string]any {
	params := map[string]any{"cwd": c.cwd}
	if c.execServerURL != "" {
		params["config"] = map[string]any{
			"features.shell_snapshot": false,
		}
	}
	return params
}

func (c *codexClient) interrupt() error {
	threadID := c.getThreadID()
	turnID := c.getActiveTurn()
	if threadID == "" || turnID == "" {
		c.markIdle("cancelled")
		return nil
	}
	_, err := c.request("turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, 5*time.Second)
	if err != nil {
		return err
	}
	c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.interrupt", Normalized: map[string]any{"status": "requested"}})
	c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "turn.cancelled", Normalized: map[string]any{"status": "idle", "turn_id": turnID}})
	c.markIdle("cancelled")
	return nil
}

func (c *codexClient) interruptForEdit() error {
	threadID := c.getThreadID()
	turnID := c.getActiveTurn()
	if threadID == "" || turnID == "" {
		return nil
	}
	_, err := c.request("turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, 5*time.Second)
	if err != nil {
		if strings.Contains(err.Error(), "no active turn to interrupt") {
			return nil
		}
		return err
	}
	c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.interrupt", Normalized: map[string]any{"status": "requested", "turn_id": turnID}})
	return nil
}

func (c *codexClient) stop(reason string) {
	c.mu.Lock()
	wasRunning := c.running
	turnID := c.activeTurn
	c.stopping = true
	c.running = false
	c.activeTurn = ""
	stdin := c.stdin
	cmd := c.cmd
	for id, ch := range c.pending {
		ch <- codexRPCResponse{ID: id, Error: &codexRPCError{Code: -32000, Message: "session stopped"}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.runtime.app.store.updateSessionStatus(c.session.ID, "idle")
	if wasRunning {
		normalized := map[string]any{"status": "idle"}
		if turnID != "" {
			normalized["turn_id"] = turnID
		}
		if reason != "" {
			normalized["reason"] = reason
		}
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "turn.cancelled", Normalized: normalized})
	}
}

func (c *codexClient) steer(input string) error {
	threadID := c.getThreadID()
	turnID := c.getActiveTurn()
	if threadID == "" || turnID == "" {
		return ErrSessionIdle
	}
	_, err := c.request("turn/steer", map[string]any{
		"threadId":       threadID,
		"expectedTurnId": turnID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          input,
			"text_elements": []any{},
		}},
	}, codexRequestTimeout)
	if err != nil {
		return err
	}
	c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.steer", Normalized: map[string]any{"status": "sent", "turn_id": turnID}})
	return nil
}

func (c *codexClient) scan(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.handleLine([]byte(line))
	}
	if err := scanner.Err(); err != nil {
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.error", Normalized: map[string]any{"message": err.Error()}})
	}
}

func (c *codexClient) scanStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 16*1024), 8*1024*1024)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if shouldSuppressCodexStderr(text) {
			continue
		}
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.warning", Normalized: map[string]any{
			"source":  "codex",
			"message": text,
		}})
	}
}

func shouldSuppressCodexStderr(text string) bool {
	var entry map[string]any
	if json.Unmarshal([]byte(text), &entry) != nil {
		return false
	}
	level := strings.ToUpper(stringValue(entry["level"]))
	target := stringValue(entry["target"])
	fields := mapValue(entry["fields"])
	message := stringValue(fields["message"])
	if level != "WARN" {
		return false
	}
	if strings.HasPrefix(target, "codex_core_skills::loader") && strings.Contains(message, "ignoring interface.icon_") {
		return true
	}
	if strings.HasPrefix(target, "codex_core::goals") && strings.Contains(message, "thread goal") {
		return true
	}
	return false
}

func (c *codexClient) handleLine(line []byte) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "control.raw", Normalized: map[string]any{
			"source": "codex",
			"line":   string(line),
			"error":  err.Error(),
		}, Raw: string(line)})
		return
	}

	if id, ok := numericID(raw["id"]); ok && raw["method"] == nil {
		response := codexRPCResponse{ID: id}
		if result, err := json.Marshal(raw["result"]); err == nil {
			response.Result = result
		}
		if errorMap := mapValue(raw["error"]); len(errorMap) > 0 {
			response.Error = &codexRPCError{
				Code:    int(numberValue(errorMap["code"])),
				Message: firstString(errorMap["message"], "codex request failed"),
			}
		}
		if c.deliverResponse(id, response) {
			return
		}
	}

	if raw["id"] != nil && raw["method"] != nil {
		ev := normalizeCodexServerRequest(c.session, raw)
		c.enrichServerRequestEvent(&ev)
		c.runtime.app.emit(ev)
		method := stringValue(raw["method"])
		if !codexServerRequestSupported(method) {
			_ = c.writeJSON(map[string]any{"id": raw["id"], "error": map[string]any{"code": -32601, "message": "unsupported codex server request " + method}})
			return
		}
		approvalID := codexApprovalID(c.session.ID, raw["id"], mapValue(raw["params"]))
		c.mu.Lock()
		c.approvals[approvalID] = codexPendingApproval{RequestID: raw["id"], Method: method, Params: mapValue(raw["params"])}
		c.mu.Unlock()
		return
	}

	c.rememberNotificationItem(raw)
	for _, ev := range normalizeCodexMessage(c.session, raw) {
		c.enrichRemoteCommandEvent(&ev)
		c.enrichInternalTurnEvent(&ev)
		c.runtime.app.emit(ev)
		if ev.Kind == "turn.started" {
			if value := mapValue(ev.Normalized); stringValue(value["turn_id"]) != "" {
				c.mu.Lock()
				c.activeTurn = stringValue(value["turn_id"])
				c.mu.Unlock()
			}
		}
		if ev.Kind == "turn.completed" || ev.Kind == "turn.failed" || ev.Kind == "turn.cancelled" {
			c.mu.Lock()
			c.nextInternal = false
			c.mu.Unlock()
			c.markIdle(stringValue(mapValue(ev.Normalized)["status"]))
		}
	}
}

func (c *codexClient) enrichInternalTurnEvent(ev *AstralEvent) {
	if ev.Kind != "turn.started" {
		return
	}
	c.mu.Lock()
	internal := c.nextInternal
	c.nextInternal = false
	c.mu.Unlock()
	if !internal {
		return
	}
	normalized := mapValue(ev.Normalized)
	normalized["internal"] = true
	ev.Normalized = normalized
}

func (c *codexClient) enrichRemoteCommandEvent(ev *AstralEvent) {
	if c.execServerURL == "" || (ev.Kind != "tool.started" && ev.Kind != "tool.completed") {
		return
	}
	normalized := mapValue(ev.Normalized)
	if stringValue(normalized["category"]) != "command" {
		return
	}
	raw := mapValue(ev.Raw)
	params := mapValue(raw["params"])
	item := mapValue(params["item"])
	processID := stringValue(item["processId"])
	if processID == "" {
		return
	}
	command, ok := c.runtime.app.codexExecCommand(c.session.WorkspaceID, processID)
	if !ok {
		return
	}
	if stringValue(normalized["native_command"]) == "" {
		normalized["native_command"] = firstString(normalized["command"], command.NativeCommand)
	}
	normalized["effective_command"] = command.EffectiveCommand
	normalized["remote_command"] = command.EffectiveCommand
	normalized["command"] = command.EffectiveCommand
	ev.Normalized = normalized
}

func (c *codexClient) wait(cmd *exec.Cmd, closed chan struct{}) {
	err := cmd.Wait()
	c.mu.Lock()
	current := c.cmd == cmd && c.closed == closed
	stopping := c.stopping
	if current {
		c.cmd = nil
		c.stdin = nil
		c.running = false
		c.initialized = false
		c.threadID = ""
		c.stopping = false
		for id, ch := range c.pending {
			ch <- codexRPCResponse{ID: id, Error: &codexRPCError{Code: -32000, Message: "codex app-server exited"}}
			delete(c.pending, id)
		}
	}
	c.mu.Unlock()
	close(closed)
	if err != nil && !stopping && current {
		c.runtime.app.store.updateSessionStatus(c.session.ID, "failed")
		c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "turn.failed", Normalized: map[string]any{
			"status":  "failed",
			"message": err.Error(),
		}})
	}
}

func (c *codexClient) finishFailed(message string) {
	c.markIdle("failed")
	c.runtime.app.store.updateSessionStatus(c.session.ID, "failed")
	c.runtime.app.emit(AstralEvent{WorkspaceID: c.session.WorkspaceID, SessionID: c.session.ID, Agent: AgentCodex, Kind: "turn.failed", Normalized: map[string]any{
		"status":  "failed",
		"message": message,
	}})
}

func (c *codexClient) markIdle(status string) {
	c.mu.Lock()
	c.running = false
	c.activeTurn = ""
	editingLast := c.editingLast
	c.mu.Unlock()
	if status == "failed" {
		c.runtime.app.store.updateSessionStatus(c.session.ID, "failed")
		return
	}
	c.runtime.app.store.updateSessionStatus(c.session.ID, "idle")
	if !editingLast {
		go c.runtime.app.startNextQueuedTurn(c.session.ID)
	}
}

func (c *codexClient) setRunning(running bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = running
}

func (c *codexClient) trySetRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return false
	}
	c.running = true
	return true
}

func (c *codexClient) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *codexClient) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

func (c *codexClient) getThreadID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threadID
}

func (c *codexClient) getActiveTurn() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeTurn
}

func (c *codexClient) waitForActiveTurn(timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if turnID := c.getActiveTurn(); turnID != "" {
			return turnID, true
		}
		if !c.isRunning() || time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *codexClient) waitUntilIdle(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !c.isRunning() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for codex turn to stop before editing")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *codexClient) beginLastUserMessageEdit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.editingLast = true
}

func (c *codexClient) endLastUserMessageEdit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.editingLast = false
}

func (c *codexClient) updateSession(session Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
}

func (c *codexClient) updateWorkspace(cwd, processCWD, execServerURL, remoteShell, codexHome string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cwd = cwd
	if processCWD == "" {
		processCWD = cwd
	}
	c.processCWD = processCWD
	c.execServerURL = execServerURL
	c.remoteShell = remoteShell
	c.codexHome = codexHome
}

func (c *codexClient) persistedThreadID() string {
	c.mu.Lock()
	if threadID := strings.TrimSpace(c.session.NativeThreadID); threadID != "" {
		c.mu.Unlock()
		return threadID
	}
	sessionID := c.session.ID
	c.mu.Unlock()

	session, ok := c.runtime.app.store.getSession(sessionID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(session.NativeThreadID)
}

func (c *codexClient) defaultModel() string {
	info := c.runtime.app.agents[AgentCodex]
	if model := strings.TrimSpace(info.CurrentModel); model != "" {
		return model
	}
	for _, model := range info.Models {
		if id := strings.TrimSpace(model.ID); id != "" {
			return id
		}
	}
	return "gpt-5.5"
}

func (c *codexClient) defaultReasoningEffort() string {
	info := c.runtime.app.agents[AgentCodex]
	if effort := strings.TrimSpace(info.CurrentEffort); effort != "" {
		return effort
	}
	for _, model := range info.Models {
		if effort := strings.TrimSpace(model.DefaultReasoningEffort); effort != "" {
			return effort
		}
	}
	return "medium"
}
