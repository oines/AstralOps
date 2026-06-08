package terminal

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
	"github.com/oines/astralops/daemon/internal/apperrors"
	internalssh "github.com/oines/astralops/daemon/internal/ssh"
	"github.com/oines/astralops/pkg/protocol"
)

const (
	terminalStatusOpen              = "open"
	terminalStatusClosed            = "closed"
	terminalFrameInput              = "terminal.input"
	terminalFrameResize             = "terminal.resize"
	terminalFrameHeartbeatAck       = "terminal.heartbeat_ack"
	terminalFrameOutput             = "terminal.output"
	terminalFrameHeartbeat          = "terminal.heartbeat"
	terminalFrameClosed             = "terminal.closed"
	terminalFrameError              = "terminal.error"
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

const (
	DefaultCols            = defaultTerminalCols
	DefaultRows            = defaultTerminalRows
	InputMaxBytes          = terminalInputMaxBytes
	ViewerAckTTL           = terminalViewerAckTTL
	OutputFrameMaxBytes    = terminalOutputFrameMaxBytes
	OutputDisconnectedCode = terminalOutputDisconnectedCode
	OutputDisconnectedText = terminalOutputDisconnectedText
	ViewerRequiredCode     = terminalViewerRequiredCode
	ViewerNotReadyCode     = terminalViewerNotReadyCode
	ViewerMismatchCode     = terminalViewerMismatchCode
)

const (
	FrameInput        = terminalFrameInput
	FrameResize       = terminalFrameResize
	FrameHeartbeatAck = terminalFrameHeartbeatAck
	FrameOutput       = terminalFrameOutput
	FrameHeartbeat    = terminalFrameHeartbeat
	FrameClosed       = terminalFrameClosed
	FrameError        = terminalFrameError
)

type Manager struct {
	deps             Deps
	mu               sync.Mutex
	sessions         map[string]*terminalSession
	retentionTimeout time.Duration
}

type Deps struct {
	Workspaces WorkspaceReader
	Events     EventPublisher
	SSH        SSH
}

type WorkspaceReader interface {
	GetWorkspace(string) (protocol.Workspace, bool)
}

type EventPublisher interface {
	Emit(protocol.AstralEvent)
}

type SSH interface {
	StartPTY(context.Context, protocol.Workspace, string, map[string]any) (<-chan internalssh.Event, func(), map[string]any, error)
	Call(context.Context, protocol.Workspace, string, any, any) error
}

type terminalSession struct {
	mu                 sync.Mutex
	closeOnce          sync.Once
	id                 string
	workspaceID        string
	agent              protocol.AgentKind
	target             string
	cwd                string
	shell              string
	writerDeviceID     string
	status             string
	outputSeq          int64
	outputHistory      []StreamFrame
	outputHistoryBytes int
	createdAt          time.Time
	updatedAt          time.Time
	viewers            map[string]*terminalViewer
	retentionTimer     *time.Timer
	retentionUntil     time.Time

	localCmd *exec.Cmd
	localPTY *os.File

	sshWorkspace    *protocol.Workspace
	sshUnsubscribe  func()
	sshTerminalID   string
	sshTerminalOpen bool
}

type terminalOpenParams = protocol.TerminalOpenParams
type terminalInputParams = protocol.TerminalInputParams
type terminalAttachParams = protocol.TerminalAttachParams
type terminalDetachParams = protocol.TerminalDetachParams
type terminalResizeParams = protocol.TerminalResizeParams
type terminalCloseParams = protocol.TerminalCloseParams
type terminalHeartbeatAckParams = protocol.TerminalHeartbeatAckParams
type terminalOpenResult = protocol.TerminalOpenResult
type terminalAckResult = protocol.TerminalAckResult
type terminalTab = protocol.TerminalTab
type terminalAttachResult = protocol.TerminalAttachResult

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
	deliveredSeq       int64
	renderedSeq        int64
	heartbeatSeq       int64
	lastAckSeq         int64
	lastAckAt          time.Time
	conn               ControlConnection
	frames             chan StreamFrame
	closed             bool
}

func NewManager(deps Deps) *Manager {
	return &Manager{deps: deps, sessions: map[string]*terminalSession{}, retentionTimeout: defaultTerminalRetentionTimeout}
}

func (m *Manager) UpdateDependencies(deps Deps) {
	if m == nil {
		return
	}
	m.deps = deps
}

func (m *Manager) SetRetentionTimeoutForTest(timeout time.Duration) {
	if m != nil {
		m.retentionTimeout = timeout
	}
}

func (m *Manager) RegisterSessionForTest(workspaceID string, agent protocol.AgentKind, target, cwd, shell string) string {
	session := newTerminalSession(workspaceID, agent, target, cwd, shell)
	m.register(session)
	return session.id
}

func (m *Manager) Emit(ev protocol.AstralEvent) {
	if m != nil && m.deps.Events != nil {
		m.deps.Events.Emit(ev)
	}
}

func (m *Manager) Open(ctx context.Context, controllerDeviceID string, params terminalOpenParams) (terminalOpenResult, error) {
	if !AvailableOnHost() {
		return terminalOpenResult{}, apperrors.New(http.StatusBadRequest, WindowsTerminalDisabledReason, "terminal is not available on this Host")
	}
	ws, ok := m.deps.Workspaces.GetWorkspace(params.WorkspaceID)
	if !ok {
		return terminalOpenResult{}, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	cols, rows := terminalSize(params.Cols, params.Rows)
	switch ws.Target {
	case "local":
		return m.openLocal(ctx, controllerDeviceID, ws, params.CWD, cols, rows)
	case "ssh":
		return m.openSSH(ctx, controllerDeviceID, ws, params.CWD, cols, rows)
	default:
		return terminalOpenResult{}, apperrors.New(http.StatusBadRequest, "workspace_target_unsupported", "workspace target does not support terminal")
	}
}

func (m *Manager) openLocal(_ context.Context, controllerDeviceID string, ws protocol.Workspace, requestedCWD string, cols, rows uint16) (terminalOpenResult, error) {
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
	session.scheduleRetention(m, m.retentionTimeout)
	m.Emit(protocol.AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: protocol.EventNormalized("control.terminal.opened", session.lifecycle("opened"))})
	go session.readLocalOutput(m)
	return session.openResult(), nil
}

func (m *Manager) openSSH(ctx context.Context, controllerDeviceID string, ws protocol.Workspace, requestedCWD string, cols, rows uint16) (terminalOpenResult, error) {
	if m.deps.SSH == nil {
		return terminalOpenResult{}, apperrors.New(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
	}
	if ws.SSH == nil {
		return terminalOpenResult{}, apperrors.New(http.StatusBadRequest, "workspace_ssh_missing", "workspace SSH config is missing")
	}
	cwd := ws.SSH.RemoteCWD
	displayCWD := ""
	if requestedCWD != "" {
		var err error
		cwd, displayCWD, err = resolveRemoteWorkspacePath(ws.SSH.RemoteCWD, requestedCWD)
		if err != nil {
			return terminalOpenResult{}, apperrors.New(http.StatusBadRequest, "path_invalid", err.Error())
		}
	}
	session := newTerminalSession(ws.ID, ws.Agent, ws.Target, displayCWD, "")
	session.sshWorkspace = &ws
	session.sshTerminalID = session.id
	events, unsubscribe, started, err := m.deps.SSH.StartPTY(ctx, ws, session.id, map[string]any{"cwd": cwd, "cols": cols, "rows": rows})
	if err != nil {
		return terminalOpenResult{}, err
	}
	session.sshUnsubscribe = unsubscribe
	session.sshTerminalOpen = true
	session.shell = stringValue(started["shell"])
	m.register(session)
	session.scheduleRetention(m, m.retentionTimeout)
	m.Emit(protocol.AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: protocol.EventNormalized("control.terminal.opened", session.lifecycle("opened"))})
	go session.readSSHOutput(m, events)
	return session.openResult(), nil
}

func (m *Manager) Attach(controllerDeviceID string, conn ControlConnection, params terminalAttachParams) (terminalAttachResult, error) {
	if conn == nil || conn.ConnectionID() == "" {
		return terminalAttachResult{}, apperrors.New(http.StatusBadRequest, "control_connection_required", "terminal.attach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, apperrors.New(http.StatusNotFound, "terminal_not_found", "terminal not found")
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
	m.Emit(protocol.AstralEvent{
		WorkspaceID: session.workspaceID,
		Agent:       session.agent,
		Kind:        "control.terminal.attached",
		Normalized: protocol.EventNormalized("control.terminal.attached",
			session.viewerLifecycle(controllerDeviceID, conn.ConnectionID(), "attached")),
	})
	return result, nil
}

func (m *Manager) Detach(controllerDeviceID string, conn ControlConnection, params terminalDetachParams) (terminalAttachResult, error) {
	if conn == nil || conn.ConnectionID() == "" {
		return terminalAttachResult{}, apperrors.New(http.StatusBadRequest, "control_connection_required", "terminal.detach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, apperrors.New(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	result, removed := session.detachViewer(conn.ConnectionID())
	if removed != nil {
		removed.close()
		session.scheduleRetention(m, m.retentionTimeout)
		m.Emit(protocol.AstralEvent{
			WorkspaceID: session.workspaceID,
			Agent:       session.agent,
			Kind:        "control.terminal.detached",
			Normalized: protocol.EventNormalized("control.terminal.detached",
				session.viewerLifecycle(controllerDeviceID, conn.ConnectionID(), "detached")),
		})
	}
	return result, nil
}

func (m *Manager) Input(ctx context.Context, controllerDeviceID string, params terminalInputParams) (terminalAckResult, error) {
	if len(params.Data) > terminalInputMaxBytes {
		return terminalAckResult{}, apperrors.New(http.StatusRequestEntityTooLarge, "terminal_input_too_large", "terminal input is too large")
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
		if session.sshWorkspace == nil || m.deps.SSH == nil {
			return terminalAckResult{}, apperrors.New(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
		}
		if err := m.deps.SSH.Call(ctx, *session.sshWorkspace, "pty_write", map[string]any{"id": session.sshTerminalID, "data": params.Data}, nil); err != nil {
			return terminalAckResult{}, err
		}
		return session.ack(), nil
	}
	session.mu.Lock()
	ptmx := session.localPTY
	session.mu.Unlock()
	if ptmx == nil {
		return terminalAckResult{}, apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	if _, err := ptmx.Write([]byte(params.Data)); err != nil {
		return terminalAckResult{}, err
	}
	return session.ack(), nil
}

func (m *Manager) Resize(ctx context.Context, controllerDeviceID string, params terminalResizeParams) (terminalAckResult, error) {
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	if params.Cols == 0 || params.Rows == 0 {
		return terminalAckResult{}, apperrors.New(http.StatusBadRequest, "terminal_size_invalid", "terminal cols and rows are required")
	}
	if err := session.validateViewerLease(controllerDeviceID, params.ViewerID, params.InputLeaseID); err != nil {
		return terminalAckResult{}, err
	}
	if session.sshTerminalOpen {
		if session.sshWorkspace == nil || m.deps.SSH == nil {
			return terminalAckResult{}, apperrors.New(http.StatusServiceUnavailable, "ssh_unavailable", "SSH manager is not available")
		}
		if err := m.deps.SSH.Call(ctx, *session.sshWorkspace, "pty_resize", map[string]any{"id": session.sshTerminalID, "cols": params.Cols, "rows": params.Rows}, nil); err != nil {
			return terminalAckResult{}, err
		}
		return session.ack(), nil
	}
	session.mu.Lock()
	ptmx := session.localPTY
	session.mu.Unlock()
	if ptmx == nil {
		return terminalAckResult{}, apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: params.Rows, Cols: params.Cols}); err != nil {
		return terminalAckResult{}, err
	}
	return session.ack(), nil
}

func (m *Manager) HeartbeatAck(controllerDeviceID string, params terminalHeartbeatAckParams) (terminalAckResult, error) {
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	return session.heartbeatAck(controllerDeviceID, params)
}

func (m *Manager) Close(ctx context.Context, controllerDeviceID string, params terminalCloseParams) (terminalAckResult, error) {
	session, err := m.openSession(params.TerminalID)
	if err != nil {
		return terminalAckResult{}, err
	}
	session.close(ctx, m, "closed")
	return session.ack(), nil
}

func (m *Manager) CloseWorkspace(ctx context.Context, workspaceID, reason string) {
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
		session.close(ctx, m, reason)
	}
}

func (m *Manager) register(session *terminalSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.id] = session
}

func (m *Manager) ListTabs() []terminalTab {
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

func (m *Manager) OpenTerminalResult(terminalID string) (terminalOpenResult, bool) {
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

func (m *Manager) DetachConnection(connectionID, reason string) {
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
		session.scheduleRetention(m, m.retentionTimeout)
		m.Emit(protocol.AstralEvent{
			WorkspaceID: session.workspaceID,
			Agent:       session.agent,
			Kind:        "control.terminal.detached",
			Normalized: protocol.EventNormalized("control.terminal.detached",
				session.viewerLifecycle(result.ViewerDeviceID, connectionID, reason)),
		})
	}
}

func (m *Manager) session(id string) (*terminalSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

func (m *Manager) openSession(terminalID string) (*terminalSession, error) {
	session, ok := m.session(terminalID)
	if !ok {
		return nil, apperrors.New(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status != terminalStatusOpen {
		return nil, apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	return session, nil
}

func newTerminalSession(workspaceID string, agent protocol.AgentKind, target, cwd, shell string) *terminalSession {
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

func (s *terminalSession) readLocalOutput(manager *Manager) {
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
			s.markClosed(manager, "exited")
			return
		}
	}
}

func (s *terminalSession) readSSHOutput(manager *Manager, events <-chan internalssh.Event) {
	for event := range events {
		switch event.Event {
		case "output":
			s.appendOutput(stringValue(event.Result["data"]))
		case "exit":
			s.markClosed(manager, "exited")
			return
		}
	}
	s.markClosed(manager, "exited")
}

func (s *terminalSession) appendOutput(data string) {
	if data == "" {
		return
	}
	chunks := terminalOutputChunks(data)
	s.mu.Lock()
	s.updatedAt = time.Now().UTC()
	frames := make([]StreamFrame, 0, len(chunks))
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

func (s *terminalSession) attachViewer(viewer *terminalViewer, afterSeq int64) (terminalAttachResult, *terminalViewer, []StreamFrame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != terminalStatusOpen {
		return terminalAttachResult{}, nil, nil, apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	s.cancelRetentionLocked()
	viewer.bindTerminalLocked(s, afterSeq)
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

func (s *terminalSession) sendToViewers(frame StreamFrame, viewers []*terminalViewer) {
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
		return apperrors.New(http.StatusConflict, terminalViewerRequiredCode, "terminal input requires an attached healthy viewer")
	}
	s.mu.Lock()
	if s.status != terminalStatusOpen {
		s.mu.Unlock()
		return apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	viewer := s.viewerByIDLocked(viewerID)
	s.mu.Unlock()
	if viewer == nil {
		return apperrors.New(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.closed {
		return apperrors.New(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	if viewer.controllerDeviceID != controllerDeviceID || viewer.inputLeaseID != inputLeaseID {
		return apperrors.New(http.StatusForbidden, terminalViewerMismatchCode, "terminal viewer lease does not match controller")
	}
	return nil
}

func (s *terminalSession) heartbeatAck(controllerDeviceID string, params terminalHeartbeatAckParams) (terminalAckResult, error) {
	viewerID := strings.TrimSpace(params.ViewerID)
	inputLeaseID := strings.TrimSpace(params.InputLeaseID)
	if viewerID == "" || inputLeaseID == "" {
		return terminalAckResult{}, apperrors.New(http.StatusBadRequest, terminalViewerRequiredCode, "terminal heartbeat ack requires viewer lease")
	}
	s.mu.Lock()
	if s.status != terminalStatusOpen {
		s.mu.Unlock()
		return terminalAckResult{}, apperrors.New(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	viewer := s.viewerByIDLocked(viewerID)
	ack := s.ackLocked()
	s.mu.Unlock()
	if viewer == nil {
		return terminalAckResult{}, apperrors.New(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.closed {
		return terminalAckResult{}, apperrors.New(http.StatusConflict, terminalViewerNotReadyCode, "terminal viewer is not attached")
	}
	if viewer.controllerDeviceID != controllerDeviceID || viewer.inputLeaseID != inputLeaseID {
		return terminalAckResult{}, apperrors.New(http.StatusForbidden, terminalViewerMismatchCode, "terminal viewer lease does not match controller")
	}
	if params.HeartbeatSeq > 0 && params.HeartbeatSeq >= viewer.lastAckSeq {
		viewer.lastAckSeq = params.HeartbeatSeq
	}
	if params.RenderedSeq > viewer.renderedSeq {
		viewer.renderedSeq = params.RenderedSeq
	}
	viewer.lastAckAt = time.Now().UTC()
	ack.CanInput = viewer.canInputLocked(time.Now())
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

func (s *terminalSession) rememberOutputLocked(frame StreamFrame) {
	if frame.FrameType != terminalFrameOutput || frame.Data == "" {
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

func (s *terminalSession) outputHistoryAfterLocked(afterSeq int64) []StreamFrame {
	if len(s.outputHistory) == 0 {
		return nil
	}
	frames := make([]StreamFrame, 0, len(s.outputHistory))
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

func (s *terminalSession) streamFrameLocked(frameType, data, reason string) StreamFrame {
	return StreamFrame{
		FrameType:   frameType,
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
	canInput := false
	if viewer := s.viewers[connectionID]; viewer != nil {
		viewerID = viewer.viewerID
		inputLeaseID = viewer.inputLeaseID
		viewer.mu.Lock()
		canInput = viewer.canInputLocked(time.Now())
		viewer.mu.Unlock()
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
		CanInput:       canInput,
	}
}

func (s *terminalSession) scheduleRetention(manager *Manager, timeout time.Duration) {
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
		s.closeIfRetentionExpired(manager)
	})
}

func (s *terminalSession) cancelRetentionLocked() {
	if s.retentionTimer != nil {
		s.retentionTimer.Stop()
		s.retentionTimer = nil
	}
	s.retentionUntil = time.Time{}
}

func (s *terminalSession) closeIfRetentionExpired(manager *Manager) {
	now := time.Now().UTC()
	s.mu.Lock()
	expired := s.status == terminalStatusOpen && len(s.viewers) == 0 && !s.retentionUntil.IsZero() && !now.Before(s.retentionUntil)
	if expired {
		s.retentionTimer = nil
		s.retentionUntil = time.Time{}
	}
	s.mu.Unlock()
	if expired {
		s.close(context.Background(), manager, "retention_timeout")
	}
}

func (s *terminalSession) close(ctx context.Context, manager *Manager, reason string) {
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

		if sshOpen && sshWorkspace != nil && manager != nil && manager.deps.SSH != nil {
			_ = manager.deps.SSH.Call(ctx, *sshWorkspace, "pty_kill", map[string]any{"id": sshTerminalID}, nil)
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
		manager.Emit(protocol.AstralEvent{WorkspaceID: s.workspaceID, Agent: s.agent, Kind: "control.terminal.closed", Normalized: protocol.EventNormalized("control.terminal.closed", s.lifecycle(reason))})
	})
}

func (s *terminalSession) markClosed(manager *Manager, reason string) {
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
		manager.Emit(protocol.AstralEvent{WorkspaceID: s.workspaceID, Agent: s.agent, Kind: "control.terminal.closed", Normalized: protocol.EventNormalized("control.terminal.closed", s.lifecycle(reason))})
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

func newTerminalViewer(conn ControlConnection) *terminalViewer {
	now := time.Now().UTC()
	return &terminalViewer{
		connectionID:       conn.ConnectionID(),
		controllerDeviceID: conn.ControllerID(),
		viewerID:           "viewer_" + randomID(12),
		inputLeaseID:       "lease_" + randomID(16),
		lastAckAt:          now,
		conn:               conn,
		frames:             make(chan StreamFrame, terminalViewerBuffer),
	}
}

func (v *terminalViewer) run() {
	ticker := time.NewTicker(terminalHeartbeatInterval)
	defer ticker.Stop()
	var pending *StreamFrame
	for {
		var frame StreamFrame
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
		if frame.FrameType != terminalFrameOutput {
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

func (v *terminalViewer) bindTerminalLocked(session *terminalSession, afterSeq int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.terminalID = session.id
	v.workspaceID = session.workspaceID
	v.target = session.target
	v.status = session.status
	v.outputSeq = session.outputSeq
	if afterSeq > v.renderedSeq {
		v.renderedSeq = afterSeq
	}
	if afterSeq > v.deliveredSeq {
		v.deliveredSeq = afterSeq
	}
}

func (v *terminalViewer) writeFrame(frame StreamFrame) {
	v.conn.WriteTerminalFrame(frame.FrameType, frame)
}

func (v *terminalViewer) writeHeartbeat() {
	frame, ok := v.heartbeatFrame()
	if !ok {
		return
	}
	v.writeFrame(frame)
}

func (v *terminalViewer) heartbeatFrame() (StreamFrame, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.terminalID == "" {
		return StreamFrame{}, false
	}
	v.heartbeatSeq++
	return StreamFrame{
		FrameType:    terminalFrameHeartbeat,
		TerminalID:   v.terminalID,
		WorkspaceID:  v.workspaceID,
		Target:       v.target,
		Status:       v.status,
		OutputSeq:    v.outputSeq,
		ViewerID:     v.viewerID,
		InputLeaseID: v.inputLeaseID,
		HeartbeatSeq: v.heartbeatSeq,
		CanInput:     v.canInputLocked(time.Now()),
	}, true
}

func (v *terminalViewer) coalesceOutput(first StreamFrame) (StreamFrame, StreamFrame, bool) {
	batch := first
	timer := time.NewTimer(terminalOutputCoalesceWindow)
	defer timer.Stop()
	for len(batch.Data) < terminalOutputFrameMaxBytes {
		select {
		case next, ok := <-v.frames:
			if !ok {
				return batch, StreamFrame{}, false
			}
			if next.FrameType != terminalFrameOutput || next.TerminalID != batch.TerminalID || len(batch.Data)+len(next.Data) > terminalOutputFrameMaxBytes {
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
			return batch, StreamFrame{}, false
		}
	}
	return batch, StreamFrame{}, false
}

func (v *terminalViewer) enqueue(frame StreamFrame) bool {
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
		if frame.FrameType == terminalFrameOutput && frame.OutputSeq > v.deliveredSeq {
			v.deliveredSeq = frame.OutputSeq
		}
		frame.CanInput = v.canInputLocked(time.Now())
	}
	select {
	case v.frames <- frame:
		return true
	default:
	}
	ctx := context.Background()
	if v.conn != nil {
		ctx = v.conn.RequestContext()
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

func (v *terminalViewer) canInputLocked(now time.Time) bool {
	if v == nil || v.closed || v.viewerID == "" || v.inputLeaseID == "" {
		return false
	}
	return true
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
		v.conn.WriteTerminalError(code, message)
		v.conn.TerminateTerminalConnection(code, message)
	}
	v.close()
}

func closeViewersAfterFrame(frame StreamFrame, viewers []*terminalViewer) {
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

func localTerminalCWD(ws protocol.Workspace, requested string) (string, string, error) {
	root := filepath.Clean(ws.LocalCWD)
	if root == "" || root == "." {
		return "", "", apperrors.New(http.StatusBadRequest, "workspace_cwd_empty", "workspace local cwd is empty")
	}
	if requested == "" {
		if err := ensureLocalControlWorkspaceExistingPath(root, root); err != nil {
			return "", "", apperrors.New(http.StatusBadRequest, "workspace_path_invalid", err.Error())
		}
		return root, "", nil
	}
	target, rel, err := resolveWorkspacePath(root, requested)
	if err != nil {
		return "", "", apperrors.New(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if err := ensureLocalControlWorkspaceExistingPath(root, target); err != nil {
		return "", "", apperrors.New(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	return target, rel, nil
}
