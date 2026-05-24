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
	path          string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	closed chan struct{}

	mu          sync.Mutex
	nextID      int64
	pending     map[int64]chan codexRPCResponse
	approvals   map[string]codexPendingApproval
	items       map[string]map[string]any
	threadID    string
	activeTurn  string
	running     bool
	initialized bool
	stopping    bool
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
	info := r.app.agents[AgentCodex]
	if !info.Available || info.Path == "" {
		return fmt.Errorf("codex executable was not found on PATH")
	}
	cwd := strings.TrimSpace(workspace.LocalCWD)
	processCWD := cwd
	execServerURL := ""
	remoteShell := ""
	if workspace.Target == "ssh" {
		if workspace.SSH == nil || strings.TrimSpace(workspace.SSH.RemoteCWD) == "" {
			return fmt.Errorf("ssh workspace remote cwd is empty")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		var hello map[string]any
		err := r.app.ssh.call(ctx, workspace, "hello", map[string]any{}, &hello)
		cancel()
		if err != nil {
			return err
		}
		cwd = filepath.Clean(workspace.SSH.RemoteCWD)
		processCWD = workspace.LocalProjectionRoot
		execServerURL = r.app.codexExecServerURL(workspace.ID)
		remoteShell = stringValue(hello["shell"])
	}
	if cwd == "" {
		return fmt.Errorf("workspace cwd is empty")
	}

	r.mu.Lock()
	client := r.clients[session.ID]
	if client == nil {
		client = newCodexClient(r, session, cwd, processCWD, execServerURL, remoteShell, info.Path)
		r.clients[session.ID] = client
	} else {
		client.updateSession(session)
		client.updateWorkspace(cwd, processCWD, execServerURL, remoteShell)
	}
	if client.isRunning() {
		r.mu.Unlock()
		return ErrSessionRunning
	}
	client.setRunning(true)
	r.mu.Unlock()

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

func newCodexClient(runtime *codexLocalRuntime, session Session, cwd, processCWD, execServerURL, remoteShell, path string) *codexClient {
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
	result, err := c.request("turn/start", params, codexRequestTimeout)
	if err != nil {
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

func (c *codexClient) ensureStarted() error {
	c.mu.Lock()
	if c.initialized && c.stdin != nil {
		c.mu.Unlock()
		return nil
	}
	c.closed = make(chan struct{})
	c.mu.Unlock()

	cmd := exec.Command(c.path, codexAppServerArgs(c.execServerURL != "")...)
	cmd.Dir = c.processCWD
	cmd.Env = os.Environ()
	if c.execServerURL != "" {
		cmd.Env = withEnvValue(cmd.Env, "CODEX_EXEC_SERVER_URL", c.execServerURL)
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
	go c.wait()

	if _, err := c.request("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "AstralOps", "version": version},
		"capabilities": map[string]any{
			"supportsStreaming": true,
			"experimentalApi":   true,
		},
	}, codexRequestTimeout); err != nil {
		return err
	}
	if err := c.notify("initialized", nil); err != nil {
		return err
	}

	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()
	return nil
}

func codexAppServerArgs(disableLocalNodeREPL bool) []string {
	args := []string{"app-server"}
	if disableLocalNodeREPL {
		args = append(args, "-c", "mcp_servers.node_repl.enabled=false")
	}
	return append(args, "--listen", "stdio://")
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
		approvalID := codexApprovalID(c.session.ID, raw["id"], mapValue(raw["params"]))
		c.mu.Lock()
		c.approvals[approvalID] = codexPendingApproval{RequestID: raw["id"], Method: method, Params: mapValue(raw["params"])}
		c.mu.Unlock()
		return
	}

	c.rememberNotificationItem(raw)
	for _, ev := range normalizeCodexMessage(c.session, raw) {
		c.enrichRemoteCommandEvent(&ev)
		c.runtime.app.emit(ev)
		if ev.Kind == "turn.started" {
			if value := mapValue(ev.Normalized); stringValue(value["turn_id"]) != "" {
				c.mu.Lock()
				c.activeTurn = stringValue(value["turn_id"])
				c.mu.Unlock()
			}
		}
		if ev.Kind == "turn.completed" || ev.Kind == "turn.failed" || ev.Kind == "turn.cancelled" {
			c.markIdle(stringValue(mapValue(ev.Normalized)["status"]))
		}
	}
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

func (c *codexClient) wait() {
	err := c.cmd.Wait()
	close(c.closed)
	c.mu.Lock()
	stopping := c.stopping
	c.stdin = nil
	c.running = false
	c.initialized = false
	c.threadID = ""
	c.stopping = false
	for id, ch := range c.pending {
		ch <- codexRPCResponse{ID: id, Error: &codexRPCError{Code: -32000, Message: "codex app-server exited"}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if err != nil && !stopping {
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
	c.mu.Unlock()
	if status == "failed" {
		c.runtime.app.store.updateSessionStatus(c.session.ID, "failed")
		return
	}
	c.runtime.app.store.updateSessionStatus(c.session.ID, "idle")
	go c.runtime.app.startNextQueuedTurn(c.session.ID)
}

func (c *codexClient) setRunning(running bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = running
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

func (c *codexClient) updateSession(session Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
}

func (c *codexClient) updateWorkspace(cwd, processCWD, execServerURL, remoteShell string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cwd = cwd
	if processCWD == "" {
		processCWD = cwd
	}
	c.processCWD = processCWD
	c.execServerURL = execServerURL
	c.remoteShell = remoteShell
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
