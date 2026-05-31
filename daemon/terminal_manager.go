package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	terminalStatusOpen              = "open"
	terminalStatusClosed            = "closed"
	terminalFrameOutput             = "terminal.output"
	terminalFrameHeartbeat          = "terminal.heartbeat"
	terminalFrameClosed             = "terminal.closed"
	terminalOutputDisconnectedCode  = "terminal_output_disconnected"
	terminalOutputDisconnectedText  = "terminal output stream disconnected"
	terminalViewerRequiredCode      = "terminal_viewer_required"
	terminalViewerNotReadyCode      = "terminal_viewer_not_ready"
	terminalViewerMismatchCode      = "terminal_viewer_mismatch"
	terminalOutputCoalesceWindow    = 25 * time.Millisecond
	terminalHeartbeatInterval       = 3 * time.Second
	terminalViewerAckTTL            = 12 * time.Second
	defaultTerminalCols             = 100
	defaultTerminalRows             = 28
	terminalViewerBuffer            = 4096
	terminalInputMaxBytes           = 64 * 1024
	terminalOutputFrameMaxBytes     = 64 * 1024
	defaultTerminalRetentionTimeout = 0
	terminalOutputHistoryMaxFrames  = 2000
	terminalOutputHistoryMaxBytes   = 256 * 1024
)

type terminalManager struct {
	app              *app
	mu               sync.Mutex
	sessions         map[string]*terminalSession
	retentionTimeout time.Duration
}

type terminalSession struct {
	mu                 sync.Mutex
	closeOnce          sync.Once
	id                 string
	workspaceID        string
	agent              AgentKind
	target             string
	cwd                string
	shell              string
	writerDeviceID     string
	status             string
	outputSeq          int64
	outputHistory      []terminalStreamFrame
	outputHistoryBytes int
	createdAt          time.Time
	updatedAt          time.Time
	viewers            map[string]*terminalViewer
	retentionTimer     *time.Timer
	retentionUntil     time.Time

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
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	Data         string `json:"data,omitempty"`
}

type terminalAttachParams struct {
	TerminalID string `json:"terminal_id"`
	AfterSeq   int64  `json:"after_seq,omitempty"`
}

type terminalDetachParams struct {
	TerminalID string `json:"terminal_id"`
}

type terminalResizeParams struct {
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	Cols         uint16 `json:"cols"`
	Rows         uint16 `json:"rows"`
}

type terminalCloseParams struct {
	TerminalID string `json:"terminal_id"`
}

type terminalHeartbeatAckParams struct {
	TerminalID   string `json:"terminal_id"`
	ViewerID     string `json:"viewer_id"`
	InputLeaseID string `json:"input_lease_id"`
	HeartbeatSeq int64  `json:"heartbeat_seq"`
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
	TerminalID     string `json:"terminal_id"`
	Status         string `json:"status"`
	OutputSeq      int64  `json:"output_seq"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
}

type terminalTab struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Agent          string `json:"agent"`
	Target         string `json:"target"`
	Shell          string `json:"shell,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Status         string `json:"status"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type terminalAttachResult struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	ViewerDeviceID string `json:"viewer_device_id"`
	ViewerID       string `json:"viewer_id"`
	InputLeaseID   string `json:"input_lease_id"`
	ConnectionID   string `json:"connection_id"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
}

type terminalStreamFrame struct {
	frameType    string `json:"-"`
	TerminalID   string `json:"terminal_id"`
	WorkspaceID  string `json:"workspace_id"`
	Target       string `json:"target"`
	Status       string `json:"status"`
	OutputSeq    int64  `json:"output_seq"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	HeartbeatSeq int64  `json:"heartbeat_seq,omitempty"`
	Data         string `json:"data,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type terminalViewer struct {
	closeOnce          sync.Once
	mu                 sync.Mutex
	connectionID       string
	controllerDeviceID string
	viewerID           string
	inputLeaseID       string
	terminalID         string
	workspaceID        string
	target             string
	status             string
	outputSeq          int64
	heartbeatSeq       int64
	lastAckSeq         int64
	lastAckAt          time.Time
	conn               controlConnection
	frames             chan terminalStreamFrame
	closed             bool
}

type terminalControlTerminator interface {
	terminateControlConnection(code, reason string)
}

func (a *app) terminalManager() *terminalManager {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	if a.terminals == nil {
		a.terminals = &terminalManager{app: a, sessions: map[string]*terminalSession{}, retentionTimeout: defaultTerminalRetentionTimeout}
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
	cwd, displayCWD, err := localTerminalCWD(ws, requestedCWD)
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
	session := newTerminalSession(ws.ID, ws.Agent, ws.Target, displayCWD, filepath.Base(shell))
	session.localCmd = cmd
	session.localPTY = ptmx
	m.register(session)
	session.scheduleRetention(m.app, m.retentionTimeout)
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
	displayCWD := ""
	if requestedCWD != "" {
		var err error
		cwd, displayCWD, err = resolveRemoteWorkspacePath(ws.SSH.RemoteCWD, requestedCWD)
		if err != nil {
			return terminalOpenResult{}, newActionError(http.StatusBadRequest, "path_invalid", err.Error())
		}
	}
	session := newTerminalSession(ws.ID, ws.Agent, ws.Target, displayCWD, "")
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
	session.scheduleRetention(m.app, m.retentionTimeout)
	m.app.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: session.lifecycle("opened")})
	go session.readSSHOutput(m.app, events)
	return session.openResult(), nil
}

func (m *terminalManager) attach(controllerDeviceID string, conn controlConnection, params terminalAttachParams) (terminalAttachResult, error) {
	if conn == nil || conn.connectionID() == "" {
		return terminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.attach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	viewer := newTerminalViewer(conn)
	result, replaced, history, err := session.attachViewer(viewer, params.AfterSeq)
	if err != nil {
		viewer.close()
		return terminalAttachResult{}, err
	}
	if replaced != nil {
		replaced.close()
	}
	go viewer.run()
	for _, frame := range history {
		if !viewer.enqueue(frame) {
			break
		}
	}
	m.app.emit(AstralEvent{
		WorkspaceID: session.workspaceID,
		Agent:       session.agent,
		Kind:        "control.terminal.attached",
		Normalized:  session.viewerLifecycle(controllerDeviceID, conn.connectionID(), "attached"),
	})
	return result, nil
}

func (m *terminalManager) detach(controllerDeviceID string, conn controlConnection, params terminalDetachParams) (terminalAttachResult, error) {
	if conn == nil || conn.connectionID() == "" {
		return terminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.detach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	result, removed := session.detachViewer(conn.connectionID())
	if removed != nil {
		removed.close()
		session.scheduleRetention(m.app, m.retentionTimeout)
		m.app.emit(AstralEvent{
			WorkspaceID: session.workspaceID,
			Agent:       session.agent,
			Kind:        "control.terminal.detached",
			Normalized:  session.viewerLifecycle(controllerDeviceID, conn.connectionID(), "detached"),
		})
	}
	return result, nil
}

func (m *terminalManager) input(ctx context.Context, controllerDeviceID string, params terminalInputParams) (terminalAckResult, error) {
	if len(params.Data) > terminalInputMaxBytes {
		return terminalAckResult{}, newActionError(http.StatusRequestEntityTooLarge, "terminal_input_too_large", "terminal input is too large")
	}
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	if err := session.validateViewerLease(controllerDeviceID, params.ViewerID, params.InputLeaseID); err != nil {
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
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	if params.Cols == 0 || params.Rows == 0 {
		return terminalAckResult{}, newActionError(http.StatusBadRequest, "terminal_size_invalid", "terminal cols and rows are required")
	}
	if err := session.validateViewerLease(controllerDeviceID, params.ViewerID, params.InputLeaseID); err != nil {
		return terminalAckResult{}, err
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

func (m *terminalManager) heartbeatAck(controllerDeviceID string, params terminalHeartbeatAckParams) (terminalAckResult, error) {
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	return session.heartbeatAck(controllerDeviceID, params)
}

func (m *terminalManager) close(ctx context.Context, controllerDeviceID string, params terminalCloseParams) (terminalAckResult, error) {
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	session.close(ctx, m.app, "closed")
	return session.ack(), nil
}

func (m *terminalManager) closeWorkspace(ctx context.Context, workspaceID, reason string) {
	if m == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	if reason == "" {
		reason = "workspace_deleted"
	}
	m.mu.Lock()
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		if session.workspaceID == workspaceID {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	for _, session := range sessions {
		session.close(ctx, m.app, reason)
	}
}

func (m *terminalManager) register(session *terminalSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.id] = session
}

func (m *terminalManager) listTabs() []terminalTab {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	tabs := make([]terminalTab, 0, len(sessions))
	for _, session := range sessions {
		tab := session.tab()
		if tab.Status == terminalStatusOpen {
			tabs = append(tabs, tab)
		}
	}
	return tabs
}

func (m *terminalManager) openTerminalResult(terminalID string) (terminalOpenResult, bool) {
	if m == nil || strings.TrimSpace(terminalID) == "" {
		return terminalOpenResult{}, false
	}
	m.mu.Lock()
	session := m.sessions[strings.TrimSpace(terminalID)]
	m.mu.Unlock()
	if session == nil {
		return terminalOpenResult{}, false
	}
	session.mu.Lock()
	open := session.status == terminalStatusOpen
	session.mu.Unlock()
	if !open {
		return terminalOpenResult{}, false
	}
	return session.openResult(), true
}

func (m *terminalManager) detachConnection(connectionID, reason string) {
	m.mu.Lock()
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		result, removed := session.detachViewer(connectionID)
		if removed == nil {
			continue
		}
		removed.close()
		session.scheduleRetention(m.app, m.retentionTimeout)
		m.app.emit(AstralEvent{
			WorkspaceID: session.workspaceID,
			Agent:       session.agent,
			Kind:        "control.terminal.detached",
			Normalized:  session.viewerLifecycle(result.ViewerDeviceID, connectionID, reason),
		})
	}
}

func (a *app) detachTerminalViewersForControlSession(connectionID, reason string) {
	a.terminalMu.Lock()
	manager := a.terminals
	a.terminalMu.Unlock()
	if manager == nil {
		return
	}
	manager.detachConnection(connectionID, reason)
}

func (m *terminalManager) session(id string) (*terminalSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

func (m *terminalManager) openSession(terminalID string) (*terminalSession, error) {
	session, ok := m.session(terminalID)
	if !ok {
		return nil, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status != terminalStatusOpen {
		return nil, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	return session, nil
}

func (a *app) releaseTerminalWritersForDevice(controllerDeviceID string) int {
	// Compatibility metric for older control responses; shared-input terminals no longer track per-controller writers.
	return 0
}

func newTerminalSession(workspaceID string, agent AgentKind, target, cwd, shell string) *terminalSession {
	now := time.Now().UTC()
	return &terminalSession{
		id:          "term_" + randomID(16),
		workspaceID: workspaceID,
		agent:       agent,
		target:      target,
		cwd:         cwd,
		shell:       shell,
		status:      terminalStatusOpen,
		createdAt:   now,
		updatedAt:   now,
		viewers:     map[string]*terminalViewer{},
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
			s.appendOutput(string(buf[:n]))
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
			s.appendOutput(stringValue(event.Result["data"]))
		case "exit":
			s.markClosed(app, "exited")
			return
		}
	}
	s.markClosed(app, "exited")
}

func (s *terminalSession) appendOutput(data string) {
	if data == "" {
		return
	}
	chunks := terminalOutputChunks(data)
	s.mu.Lock()
	s.updatedAt = time.Now().UTC()
	frames := make([]terminalStreamFrame, 0, len(chunks))
	for _, chunk := range chunks {
		s.outputSeq++
		frame := s.streamFrameLocked(terminalFrameOutput, chunk, "")
		s.rememberOutputLocked(frame)
		frames = append(frames, frame)
	}
	viewers := s.viewersSnapshotLocked()
	s.mu.Unlock()
	for _, frame := range frames {
		s.sendToViewers(frame, viewers)
	}
}

func (s *terminalSession) attachViewer(viewer *terminalViewer, afterSeq int64) (terminalAttachResult, *terminalViewer, []terminalStreamFrame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != terminalStatusOpen {
		return terminalAttachResult{}, nil, nil, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	s.cancelRetentionLocked()
	viewer.bindTerminalLocked(s)
	replaced := s.viewers[viewer.connectionID]
	s.viewers[viewer.connectionID] = viewer
	return s.attachResultLocked(viewer.connectionID, viewer.controllerDeviceID), replaced, s.outputHistoryAfterLocked(afterSeq), nil
}

func (s *terminalSession) detachViewer(connectionID string) (terminalAttachResult, *terminalViewer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	viewer := s.viewers[connectionID]
	viewerDeviceID := ""
	if viewer != nil {
		viewerDeviceID = viewer.controllerDeviceID
		delete(s.viewers, connectionID)
	}
	return s.attachResultLocked(connectionID, viewerDeviceID), viewer
}

func (s *terminalSession) sendToViewers(frame terminalStreamFrame, viewers []*terminalViewer) {
	for _, viewer := range viewers {
		if viewer.enqueue(frame) {
			continue
		}
		if _, removed := s.detachViewer(viewer.connectionID); removed != nil {
			removed.fail(terminalOutputDisconnectedCode, terminalOutputDisconnectedText)
		}
	}
}

func (s *terminalSession) validateViewerLease(controllerDeviceID, viewerID, inputLeaseID string) error {
	viewerID = strings.TrimSpace(viewerID)
	inputLeaseID = strings.TrimSpace(inputLeaseID)
	if viewerID == "" || inputLeaseID == "" {
		return newActionError(http.StatusConflict, terminalViewerRequiredCode, "terminal input requires an attached healthy viewer")
	}
	s.mu.Lock()
	if s.status != terminalStatusOpen {
		s.mu.Unlock()
		return newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	viewer := s.viewerByIDLocked(viewerID)
	s.mu.Unlock()
	if viewer == nil {
		return newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.closed {
		return newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	if viewer.controllerDeviceID != controllerDeviceID || viewer.inputLeaseID != inputLeaseID {
		return newActionError(http.StatusForbidden, terminalViewerMismatchCode, "terminal viewer lease does not match controller")
	}
	if viewer.lastAckAt.IsZero() || time.Since(viewer.lastAckAt) > terminalViewerAckTTL {
		return newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal output is not synchronized; input is paused")
	}
	return nil
}

func (s *terminalSession) heartbeatAck(controllerDeviceID string, params terminalHeartbeatAckParams) (terminalAckResult, error) {
	viewerID := strings.TrimSpace(params.ViewerID)
	inputLeaseID := strings.TrimSpace(params.InputLeaseID)
	if viewerID == "" || inputLeaseID == "" {
		return terminalAckResult{}, newActionError(http.StatusBadRequest, terminalViewerRequiredCode, "terminal heartbeat ack requires viewer lease")
	}
	s.mu.Lock()
	if s.status != terminalStatusOpen {
		s.mu.Unlock()
		return terminalAckResult{}, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	viewer := s.viewerByIDLocked(viewerID)
	ack := s.ackLocked()
	s.mu.Unlock()
	if viewer == nil {
		return terminalAckResult{}, newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.closed {
		return terminalAckResult{}, newActionError(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	if viewer.controllerDeviceID != controllerDeviceID || viewer.inputLeaseID != inputLeaseID {
		return terminalAckResult{}, newActionError(http.StatusForbidden, terminalViewerMismatchCode, "terminal viewer lease does not match controller")
	}
	if params.HeartbeatSeq > 0 && params.HeartbeatSeq >= viewer.lastAckSeq {
		viewer.lastAckSeq = params.HeartbeatSeq
	}
	viewer.lastAckAt = time.Now().UTC()
	return ack, nil
}

func (s *terminalSession) viewerByIDLocked(viewerID string) *terminalViewer {
	for _, viewer := range s.viewers {
		if viewer.viewerID == viewerID {
			return viewer
		}
	}
	return nil
}

func (s *terminalSession) rememberOutputLocked(frame terminalStreamFrame) {
	if frame.frameType != terminalFrameOutput || frame.Data == "" {
		return
	}
	s.outputHistory = append(s.outputHistory, frame)
	s.outputHistoryBytes += len(frame.Data)
	for len(s.outputHistory) > terminalOutputHistoryMaxFrames || s.outputHistoryBytes > terminalOutputHistoryMaxBytes {
		if len(s.outputHistory) == 0 {
			s.outputHistoryBytes = 0
			return
		}
		s.outputHistoryBytes -= len(s.outputHistory[0].Data)
		s.outputHistory = s.outputHistory[1:]
		if s.outputHistoryBytes < 0 {
			s.outputHistoryBytes = 0
		}
	}
}

func (s *terminalSession) outputHistoryAfterLocked(afterSeq int64) []terminalStreamFrame {
	if len(s.outputHistory) == 0 {
		return nil
	}
	frames := make([]terminalStreamFrame, 0, len(s.outputHistory))
	for _, frame := range s.outputHistory {
		if frame.OutputSeq > afterSeq {
			frames = append(frames, frame)
		}
	}
	return frames
}

func (s *terminalSession) viewersSnapshotLocked() []*terminalViewer {
	viewers := make([]*terminalViewer, 0, len(s.viewers))
	for _, viewer := range s.viewers {
		viewers = append(viewers, viewer)
	}
	return viewers
}

func (s *terminalSession) takeViewersLocked() []*terminalViewer {
	viewers := s.viewersSnapshotLocked()
	s.viewers = map[string]*terminalViewer{}
	return viewers
}

func (s *terminalSession) streamFrameLocked(frameType, data, reason string) terminalStreamFrame {
	return terminalStreamFrame{
		frameType:   frameType,
		TerminalID:  s.id,
		WorkspaceID: s.workspaceID,
		Target:      s.target,
		Status:      s.status,
		OutputSeq:   s.outputSeq,
		Data:        data,
		Reason:      reason,
	}
}

func (s *terminalSession) attachResultLocked(connectionID, viewerDeviceID string) terminalAttachResult {
	viewerID := ""
	inputLeaseID := ""
	if viewer := s.viewers[connectionID]; viewer != nil {
		viewerID = viewer.viewerID
		inputLeaseID = viewer.inputLeaseID
	}
	return terminalAttachResult{
		TerminalID:     s.id,
		WorkspaceID:    s.workspaceID,
		Target:         s.target,
		Status:         s.status,
		ViewerDeviceID: viewerDeviceID,
		ViewerID:       viewerID,
		InputLeaseID:   inputLeaseID,
		ConnectionID:   connectionID,
		WriterDeviceID: s.writerDeviceID,
		OutputSeq:      s.outputSeq,
	}
}

func (s *terminalSession) scheduleRetention(app *app, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != terminalStatusOpen || len(s.viewers) > 0 {
		return
	}
	s.cancelRetentionLocked()
	s.retentionUntil = time.Now().UTC().Add(timeout)
	s.retentionTimer = time.AfterFunc(timeout, func() {
		s.closeIfRetentionExpired(app)
	})
}

func (s *terminalSession) cancelRetentionLocked() {
	if s.retentionTimer != nil {
		s.retentionTimer.Stop()
		s.retentionTimer = nil
	}
	s.retentionUntil = time.Time{}
}

func (s *terminalSession) closeIfRetentionExpired(app *app) {
	now := time.Now().UTC()
	s.mu.Lock()
	expired := s.status == terminalStatusOpen && len(s.viewers) == 0 && !s.retentionUntil.IsZero() && !now.Before(s.retentionUntil)
	if expired {
		s.retentionTimer = nil
		s.retentionUntil = time.Time{}
	}
	s.mu.Unlock()
	if expired {
		s.close(context.Background(), app, "retention_timeout")
	}
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
		s.cancelRetentionLocked()
		closedFrame := s.streamFrameLocked(terminalFrameClosed, "", reason)
		viewers := s.takeViewersLocked()
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
		closeViewersAfterFrame(closedFrame, viewers)
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
		s.cancelRetentionLocked()
		closedFrame := s.streamFrameLocked(terminalFrameClosed, "", reason)
		viewers := s.takeViewersLocked()
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
		closeViewersAfterFrame(closedFrame, viewers)
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
	return s.ackLocked()
}

func (s *terminalSession) ackLocked() terminalAckResult {
	return terminalAckResult{TerminalID: s.id, Status: s.status, OutputSeq: s.outputSeq, WriterDeviceID: s.writerDeviceID}
}

func (s *terminalSession) tab() terminalTab {
	s.mu.Lock()
	defer s.mu.Unlock()
	return terminalTab{
		TerminalID:     s.id,
		WorkspaceID:    s.workspaceID,
		Agent:          string(s.agent),
		Target:         s.target,
		Shell:          s.shell,
		CWD:            s.cwd,
		Status:         s.status,
		WriterDeviceID: s.writerDeviceID,
		OutputSeq:      s.outputSeq,
		CreatedAt:      s.createdAt.Format(time.RFC3339Nano),
		UpdatedAt:      s.updatedAt.Format(time.RFC3339Nano),
	}
}

func (s *terminalSession) lifecycle(reason string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := map[string]any{
		"terminal_id":      s.id,
		"workspace_id":     s.workspaceID,
		"agent":            s.agent,
		"target":           s.target,
		"cwd":              s.cwd,
		"status":           s.status,
		"writer_device_id": s.writerDeviceID,
		"reason":           reason,
	}
	if !s.retentionUntil.IsZero() {
		value["retention_until"] = s.retentionUntil.Format(time.RFC3339Nano)
	}
	return value
}

func (s *terminalSession) viewerLifecycle(viewerDeviceID, connectionID, reason string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := map[string]any{
		"terminal_id":      s.id,
		"workspace_id":     s.workspaceID,
		"agent":            s.agent,
		"target":           s.target,
		"cwd":              s.cwd,
		"status":           s.status,
		"writer_device_id": s.writerDeviceID,
		"viewer_device_id": viewerDeviceID,
		"connection_id":    connectionID,
		"output_seq":       s.outputSeq,
		"reason":           reason,
	}
	if !s.retentionUntil.IsZero() {
		value["retention_until"] = s.retentionUntil.Format(time.RFC3339Nano)
	}
	return value
}

func newTerminalViewer(conn controlConnection) *terminalViewer {
	now := time.Now().UTC()
	return &terminalViewer{
		connectionID:       conn.connectionID(),
		controllerDeviceID: conn.controllerID(),
		viewerID:           "viewer_" + randomID(12),
		inputLeaseID:       "lease_" + randomID(16),
		lastAckAt:          now,
		conn:               conn,
		frames:             make(chan terminalStreamFrame, terminalViewerBuffer),
	}
}

func (v *terminalViewer) run() {
	ticker := time.NewTicker(terminalHeartbeatInterval)
	defer ticker.Stop()
	var pending *terminalStreamFrame
	for {
		var frame terminalStreamFrame
		if pending != nil {
			frame = *pending
			pending = nil
		} else {
			select {
			case next, ok := <-v.frames:
				if !ok {
					return
				}
				frame = next
			case <-ticker.C:
				v.writeHeartbeat()
				continue
			}
		}
		if frame.frameType != terminalFrameOutput {
			v.writeFrame(frame)
			continue
		}
		batch, next, hasNext := v.coalesceOutput(frame)
		v.writeFrame(batch)
		if hasNext {
			pending = &next
		}
	}
}

func (v *terminalViewer) bindTerminalLocked(session *terminalSession) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.terminalID = session.id
	v.workspaceID = session.workspaceID
	v.target = session.target
	v.status = session.status
	v.outputSeq = session.outputSeq
}

func (v *terminalViewer) writeFrame(frame terminalStreamFrame) {
	v.conn.writePlain(controlPlainFrame{Type: frame.frameType, Terminal: &frame})
}

func (v *terminalViewer) writeHeartbeat() {
	frame, ok := v.heartbeatFrame()
	if !ok {
		return
	}
	v.writeFrame(frame)
}

func (v *terminalViewer) heartbeatFrame() (terminalStreamFrame, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.terminalID == "" {
		return terminalStreamFrame{}, false
	}
	v.heartbeatSeq++
	return terminalStreamFrame{
		frameType:    terminalFrameHeartbeat,
		TerminalID:   v.terminalID,
		WorkspaceID:  v.workspaceID,
		Target:       v.target,
		Status:       v.status,
		OutputSeq:    v.outputSeq,
		ViewerID:     v.viewerID,
		InputLeaseID: v.inputLeaseID,
		HeartbeatSeq: v.heartbeatSeq,
	}, true
}

func (v *terminalViewer) coalesceOutput(first terminalStreamFrame) (terminalStreamFrame, terminalStreamFrame, bool) {
	batch := first
	timer := time.NewTimer(terminalOutputCoalesceWindow)
	defer timer.Stop()
	for len(batch.Data) < terminalOutputFrameMaxBytes {
		select {
		case next, ok := <-v.frames:
			if !ok {
				return batch, terminalStreamFrame{}, false
			}
			if next.frameType != terminalFrameOutput || next.TerminalID != batch.TerminalID || len(batch.Data)+len(next.Data) > terminalOutputFrameMaxBytes {
				return batch, next, true
			}
			batch.Data += next.Data
			batch.OutputSeq = next.OutputSeq
			batch.Status = next.Status
			batch.Reason = next.Reason
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(terminalOutputCoalesceWindow)
		case <-timer.C:
			return batch, terminalStreamFrame{}, false
		}
	}
	return batch, terminalStreamFrame{}, false
}

func (v *terminalViewer) enqueue(frame terminalStreamFrame) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return false
	}
	if frame.TerminalID != "" {
		v.terminalID = frame.TerminalID
		v.workspaceID = frame.WorkspaceID
		v.target = frame.Target
		v.status = frame.Status
		if frame.OutputSeq > v.outputSeq {
			v.outputSeq = frame.OutputSeq
		}
	}
	select {
	case v.frames <- frame:
		return true
	default:
	}
	ctx := context.Background()
	if v.conn != nil {
		ctx = v.conn.requestContext()
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case v.frames <- frame:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (v *terminalViewer) close() {
	v.closeOnce.Do(func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		v.closed = true
		close(v.frames)
	})
}

func (v *terminalViewer) fail(code, message string) {
	if v == nil {
		return
	}
	if v.conn != nil {
		v.conn.writePlain(controlPlainFrame{
			Type: "response",
			Response: &ControlResponse{
				OK: false,
				Error: &ControlError{
					Status:  http.StatusServiceUnavailable,
					Code:    code,
					Message: message,
				},
			},
		})
		if terminator, ok := v.conn.(terminalControlTerminator); ok {
			terminator.terminateControlConnection(code, message)
		}
	}
	v.close()
}

func closeViewersAfterFrame(frame terminalStreamFrame, viewers []*terminalViewer) {
	for _, viewer := range viewers {
		_ = viewer.enqueue(frame)
		viewer.close()
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

func terminalOutputChunks(data string) []string {
	if len(data) <= terminalOutputFrameMaxBytes {
		return []string{data}
	}
	chunks := make([]string, 0, len(data)/terminalOutputFrameMaxBytes+1)
	for len(data) > terminalOutputFrameMaxBytes {
		chunks = append(chunks, data[:terminalOutputFrameMaxBytes])
		data = data[terminalOutputFrameMaxBytes:]
	}
	if data != "" {
		chunks = append(chunks, data)
	}
	return chunks
}

func localTerminalCWD(ws Workspace, requested string) (string, string, error) {
	root := filepath.Clean(ws.LocalCWD)
	if root == "" || root == "." {
		return "", "", newActionError(http.StatusBadRequest, "workspace_cwd_empty", "workspace local cwd is empty")
	}
	if requested == "" {
		if err := ensureLocalControlWorkspaceExistingPath(root, root); err != nil {
			return "", "", err
		}
		return root, "", nil
	}
	target, rel, err := resolveWorkspacePath(root, requested)
	if err != nil {
		return "", "", newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if err := ensureLocalControlWorkspaceExistingPath(root, target); err != nil {
		return "", "", err
	}
	return target, rel, nil
}
