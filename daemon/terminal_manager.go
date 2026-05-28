package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	terminalStatusOpen   = "open"
	terminalStatusClosed = "closed"
	defaultTerminalCols  = 100
	defaultTerminalRows  = 28
)

type terminalManager struct {
	app      *app
	mu       sync.Mutex
	sessions map[string]*terminalSession
}

type terminalSession struct {
	mu             sync.Mutex
	closeOnce      sync.Once
	id             string
	workspaceID    string
	agent          AgentKind
	target         string
	cwd            string
	shell          string
	writerDeviceID string
	status         string
	outputSeq      int64
	createdAt      time.Time
	updatedAt      time.Time

	localCmd *exec.Cmd
	localPTY *os.File

	sshWorkspace    *Workspace
	sshUnsubscribe  func()
	sshTerminalID   string
	sshTerminalOpen bool
}

type terminalOpenParams struct {
	WorkspaceID string `json:"workspace_id"`
	CWD         string `json:"cwd,omitempty"`
	Cols        uint16 `json:"cols,omitempty"`
	Rows        uint16 `json:"rows,omitempty"`
}

type terminalInputParams struct {
	TerminalID string `json:"terminal_id"`
	Data       string `json:"data,omitempty"`
}

type terminalResizeParams struct {
	TerminalID string `json:"terminal_id"`
	Cols       uint16 `json:"cols"`
	Rows       uint16 `json:"rows"`
}

type terminalCloseParams struct {
	TerminalID string `json:"terminal_id"`
}

type terminalOpenResult struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Shell          string `json:"shell,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Status         string `json:"status"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
}

type terminalAckResult struct {
	TerminalID string `json:"terminal_id"`
	Status     string `json:"status"`
	OutputSeq  int64  `json:"output_seq"`
}

func (a *app) terminalManager() *terminalManager {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	if a.terminals == nil {
		a.terminals = &terminalManager{app: a, sessions: map[string]*terminalSession{}}
	}
	return a.terminals
}

func (m *terminalManager) open(ctx context.Context, controllerDeviceID string, params terminalOpenParams) (terminalOpenResult, error) {
	if !terminalAvailableOnHost() {
		return terminalOpenResult{}, newActionError(http.StatusBadRequest, windowsTerminalDisabledReason, "terminal is not available on this Host")
	}
	ws, ok := m.app.store.getWorkspace(params.WorkspaceID)
	if !ok {
		return terminalOpenResult{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	cols, rows := terminalSize(params.Cols, params.Rows)
	switch ws.Target {
	case "local":
		return m.openLocal(ctx, controllerDeviceID, ws, params.CWD, cols, rows)
	case "ssh":
		return m.openSSH(ctx, controllerDeviceID, ws, params.CWD, cols, rows)
	default:
		return terminalOpenResult{}, newActionError(http.StatusBadRequest, "workspace_target_unsupported", "workspace target does not support terminal")
	}
}

func (m *terminalManager) openLocal(_ context.Context, controllerDeviceID string, ws Workspace, requestedCWD string, cols, rows uint16) (terminalOpenResult, error) {
	cwd, err := localTerminalCWD(ws, requestedCWD)
	if err != nil {
		return terminalOpenResult{}, err
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = terminalEnv(os.Environ())
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return terminalOpenResult{}, err
	}
	session := newTerminalSession(ws.ID, ws.Agent, ws.Target, cwd, filepath.Base(shell), controllerDeviceID)
	session.localCmd = cmd
	session.localPTY = ptmx
	m.register(session)
	m.app.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: session.lifecycle("opened")})
	go session.readLocalOutput(m.app)
	return session.openResult(), nil
}

func (m *terminalManager) openSSH(ctx context.Context, controllerDeviceID string, ws Workspace, requestedCWD string, cols, rows uint16) (terminalOpenResult, error) {
	if m.app.ssh == nil {
		return terminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
	}
	if ws.SSH == nil {
		return terminalOpenResult{}, newActionError(http.StatusBadRequest, "workspace_ssh_missing", "workspace SSH config is missing")
	}
	cwd := ws.SSH.RemoteCWD
	if requestedCWD != "" {
		var err error
		cwd, _, err = resolveRemoteWorkspacePath(ws.SSH.RemoteCWD, requestedCWD)
		if err != nil {
			return terminalOpenResult{}, newActionError(http.StatusBadRequest, "path_invalid", err.Error())
		}
	}
	session := newTerminalSession(ws.ID, ws.Agent, ws.Target, cwd, "", controllerDeviceID)
	session.sshWorkspace = &ws
	session.sshTerminalID = session.id
	_, events, unsubscribe, started, err := m.app.ssh.startPTY(ctx, ws, session.id, map[string]any{"cwd": cwd, "cols": cols, "rows": rows})
	if err != nil {
		return terminalOpenResult{}, err
	}
	session.sshUnsubscribe = unsubscribe
	session.sshTerminalOpen = true
	session.shell = stringValue(started["shell"])
	m.register(session)
	m.app.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: session.lifecycle("opened")})
	go session.readSSHOutput(m.app, events)
	return session.openResult(), nil
}

func (m *terminalManager) input(ctx context.Context, controllerDeviceID string, params terminalInputParams) (terminalAckResult, error) {
	session, err := m.writerSession(controllerDeviceID, params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	if params.Data == "" {
		return session.ack(), nil
	}
	if session.sshTerminalOpen {
		if session.sshWorkspace == nil || m.app.ssh == nil {
			return terminalAckResult{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
		}
		if err := m.app.ssh.call(ctx, *session.sshWorkspace, "pty_write", map[string]any{"id": session.sshTerminalID, "data": params.Data}, nil); err != nil {
			return terminalAckResult{}, err
		}
		return session.ack(), nil
	}
	session.mu.Lock()
	ptmx := session.localPTY
	session.mu.Unlock()
	if ptmx == nil {
		return terminalAckResult{}, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	if _, err := ptmx.Write([]byte(params.Data)); err != nil {
		return terminalAckResult{}, err
	}
	return session.ack(), nil
}

func (m *terminalManager) resize(ctx context.Context, controllerDeviceID string, params terminalResizeParams) (terminalAckResult, error) {
	session, err := m.writerSession(controllerDeviceID, params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	if params.Cols == 0 || params.Rows == 0 {
		return terminalAckResult{}, newActionError(http.StatusBadRequest, "terminal_size_invalid", "terminal cols and rows are required")
	}
	if session.sshTerminalOpen {
		if session.sshWorkspace == nil || m.app.ssh == nil {
			return terminalAckResult{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
		}
		if err := m.app.ssh.call(ctx, *session.sshWorkspace, "pty_resize", map[string]any{"id": session.sshTerminalID, "cols": params.Cols, "rows": params.Rows}, nil); err != nil {
			return terminalAckResult{}, err
		}
		return session.ack(), nil
	}
	session.mu.Lock()
	ptmx := session.localPTY
	session.mu.Unlock()
	if ptmx == nil {
		return terminalAckResult{}, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: params.Rows, Cols: params.Cols}); err != nil {
		return terminalAckResult{}, err
	}
	return session.ack(), nil
}

func (m *terminalManager) close(ctx context.Context, controllerDeviceID string, params terminalCloseParams) (terminalAckResult, error) {
	session, err := m.writerSession(controllerDeviceID, params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	session.close(ctx, m.app, "closed")
	return session.ack(), nil
}

func (m *terminalManager) register(session *terminalSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.id] = session
}

func (m *terminalManager) session(id string) (*terminalSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

func (m *terminalManager) writerSession(controllerDeviceID, terminalID string) (*terminalSession, error) {
	session, ok := m.session(terminalID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status != terminalStatusOpen {
		return nil, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	if session.writerDeviceID != controllerDeviceID {
		return nil, newActionError(http.StatusForbidden, "terminal_writer_denied", "controller is not the terminal active writer")
	}
	return session, nil
}

func newTerminalSession(workspaceID string, agent AgentKind, target, cwd, shell, writerDeviceID string) *terminalSession {
	now := time.Now().UTC()
	return &terminalSession{
		id:             "term_" + randomID(16),
		workspaceID:    workspaceID,
		agent:          agent,
		target:         target,
		cwd:            cwd,
		shell:          shell,
		writerDeviceID: writerDeviceID,
		status:         terminalStatusOpen,
		createdAt:      now,
		updatedAt:      now,
	}
}

func (s *terminalSession) readLocalOutput(app *app) {
	buf := make([]byte, 4096)
	for {
		s.mu.Lock()
		ptmx := s.localPTY
		s.mu.Unlock()
		if ptmx == nil {
			return
		}
		n, err := ptmx.Read(buf)
		if n > 0 {
			s.appendOutput(n)
		}
		if err != nil {
			s.markClosed(app, "exited")
			return
		}
	}
}

func (s *terminalSession) readSSHOutput(app *app, events <-chan proxyEvent) {
	for event := range events {
		switch event.Event {
		case "output":
			s.appendOutput(len(stringValue(event.Result["data"])))
		case "exit":
			s.markClosed(app, "exited")
			return
		}
	}
	s.markClosed(app, "exited")
}

func (s *terminalSession) appendOutput(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputSeq++
	s.updatedAt = time.Now().UTC()
}

func (s *terminalSession) close(ctx context.Context, app *app, reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		localCmd := s.localCmd
		localPTY := s.localPTY
		sshWorkspace := s.sshWorkspace
		sshTerminalID := s.sshTerminalID
		sshUnsubscribe := s.sshUnsubscribe
		sshOpen := s.sshTerminalOpen
		s.localCmd = nil
		s.localPTY = nil
		s.sshTerminalOpen = false
		s.status = terminalStatusClosed
		s.updatedAt = time.Now().UTC()
		s.mu.Unlock()

		if sshOpen && sshWorkspace != nil && app.ssh != nil {
			_ = app.ssh.call(ctx, *sshWorkspace, "pty_kill", map[string]any{"id": sshTerminalID}, nil)
		}
		if sshUnsubscribe != nil {
			sshUnsubscribe()
		}
		if localCmd != nil {
			_ = killCommandProcessGroup(localCmd)
		}
		if localPTY != nil {
			_ = localPTY.Close()
		}
		if localCmd != nil && localCmd.Process != nil {
			_, _ = localCmd.Process.Wait()
		}
		app.emit(AstralEvent{WorkspaceID: s.workspaceID, Agent: s.agent, Kind: "control.terminal.closed", Normalized: s.lifecycle(reason)})
	})
}

func (s *terminalSession) markClosed(app *app, reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		localCmd := s.localCmd
		localPTY := s.localPTY
		sshUnsubscribe := s.sshUnsubscribe
		s.localCmd = nil
		s.localPTY = nil
		s.sshTerminalOpen = false
		s.status = terminalStatusClosed
		s.updatedAt = time.Now().UTC()
		s.sshUnsubscribe = nil
		s.mu.Unlock()
		if sshUnsubscribe != nil {
			sshUnsubscribe()
		}
		if localPTY != nil {
			_ = localPTY.Close()
		}
		if localCmd != nil && localCmd.Process != nil {
			_, _ = localCmd.Process.Wait()
		}
		app.emit(AstralEvent{WorkspaceID: s.workspaceID, Agent: s.agent, Kind: "control.terminal.closed", Normalized: s.lifecycle(reason)})
	})
}

func (s *terminalSession) openResult() terminalOpenResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminalOpenResult{
		TerminalID:     s.id,
		WorkspaceID:    s.workspaceID,
		Target:         s.target,
		Shell:          s.shell,
		CWD:            s.cwd,
		Status:         s.status,
		WriterDeviceID: s.writerDeviceID,
		OutputSeq:      s.outputSeq,
	}
}

func (s *terminalSession) ack() terminalAckResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminalAckResult{TerminalID: s.id, Status: s.status, OutputSeq: s.outputSeq}
}

func (s *terminalSession) lifecycle(reason string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"terminal_id":      s.id,
		"workspace_id":     s.workspaceID,
		"agent":            s.agent,
		"target":           s.target,
		"cwd":              s.cwd,
		"status":           s.status,
		"writer_device_id": s.writerDeviceID,
		"reason":           reason,
	}
}

func terminalSize(cols, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = defaultTerminalCols
	}
	if rows == 0 {
		rows = defaultTerminalRows
	}
	return cols, rows
}

func localTerminalCWD(ws Workspace, requested string) (string, error) {
	root := filepath.Clean(ws.LocalCWD)
	if root == "" || root == "." {
		return "", newActionError(http.StatusBadRequest, "workspace_cwd_empty", "workspace local cwd is empty")
	}
	if requested == "" {
		return root, nil
	}
	target, _, err := resolveWorkspacePath(root, requested)
	if err != nil {
		return "", newActionError(http.StatusBadRequest, "path_invalid", err.Error())
	}
	return target, nil
}
