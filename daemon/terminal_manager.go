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
	terminalStatusOpen              = "open"
	terminalStatusClosed            = "closed"
	terminalFrameOutput             = "terminal.output"
	terminalFrameClosed             = "terminal.closed"
	defaultTerminalCols             = 100
	defaultTerminalRows             = 28
	terminalViewerBuffer            = 256
	terminalInputMaxBytes           = 64 * 1024
	terminalOutputFrameMaxBytes     = 64 * 1024
	defaultTerminalRetentionTimeout = 2 * time.Minute
)

type terminalManager struct {
	app              *app
	mu               sync.Mutex
	sessions         map[string]*terminalSession
	retentionTimeout time.Duration
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
	viewers        map[string]*terminalViewer
	retentionTimer *time.Timer
	retentionUntil time.Time

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

type terminalAttachParams struct {
	TerminalID string `json:"terminal_id"`
}

type terminalDetachParams struct {
	TerminalID string `json:"terminal_id"`
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
	TerminalID     string `json:"terminal_id"`
	Status         string `json:"status"`
	OutputSeq      int64  `json:"output_seq"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
}

type terminalAttachResult struct {
	TerminalID     string `json:"terminal_id"`
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	ViewerDeviceID string `json:"viewer_device_id"`
	ConnectionID   string `json:"connection_id"`
	WriterDeviceID string `json:"writer_device_id,omitempty"`
	OutputSeq      int64  `json:"output_seq"`
}

type terminalStreamFrame struct {
	frameType   string `json:"-"`
	TerminalID  string `json:"terminal_id"`
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Status      string `json:"status"`
	OutputSeq   int64  `json:"output_seq"`
	Data        string `json:"data,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type terminalViewer struct {
	closeOnce          sync.Once
	mu                 sync.Mutex
	connectionID       string
	controllerDeviceID string
	conn               *controlWSConn
	frames             chan terminalStreamFrame
	closed             bool
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
	session.scheduleRetention(m.app, m.retentionTimeout)
	m.app.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "control.terminal.opened", Normalized: session.lifecycle("opened")})
	go session.readSSHOutput(m.app, events)
	return session.openResult(), nil
}

func (m *terminalManager) attach(controllerDeviceID string, conn *controlWSConn, params terminalAttachParams) (terminalAttachResult, error) {
	if conn == nil || conn.id == "" {
		return terminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.attach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	viewer := newTerminalViewer(conn)
	result, replaced, err := session.attachViewer(viewer)
	if err != nil {
		viewer.close()
		return terminalAttachResult{}, err
	}
	if replaced != nil {
		replaced.close()
	}
	go viewer.run()
	m.app.emit(AstralEvent{
		WorkspaceID: session.workspaceID,
		Agent:       session.agent,
		Kind:        "control.terminal.attached",
		Normalized:  session.viewerLifecycle(controllerDeviceID, conn.id, "attached"),
	})
	return result, nil
}

func (m *terminalManager) detach(controllerDeviceID string, conn *controlWSConn, params terminalDetachParams) (terminalAttachResult, error) {
	if conn == nil || conn.id == "" {
		return terminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.detach requires an encrypted control connection")
	}
	session, ok := m.session(params.TerminalID)
	if !ok {
		return terminalAttachResult{}, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	result, removed := session.detachViewer(conn.id)
	if removed != nil {
		removed.close()
		session.scheduleRetention(m.app, m.retentionTimeout)
		m.app.emit(AstralEvent{
			WorkspaceID: session.workspaceID,
			Agent:       session.agent,
			Kind:        "control.terminal.detached",
			Normalized:  session.viewerLifecycle(controllerDeviceID, conn.id, "detached"),
		})
	}
	return result, nil
}

func (m *terminalManager) input(ctx context.Context, controllerDeviceID string, params terminalInputParams) (terminalAckResult, error) {
	if len(params.Data) > terminalInputMaxBytes {
		return terminalAckResult{}, newActionError(http.StatusRequestEntityTooLarge, "terminal_input_too_large", "terminal input is too large")
	}
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
	if session.writerDeviceID == "" {
		session.writerDeviceID = controllerDeviceID
		session.updatedAt = time.Now().UTC()
		return session, nil
	}
	if session.writerDeviceID != controllerDeviceID {
		return nil, newActionError(http.StatusForbidden, "terminal_writer_denied", "controller is not the terminal active writer")
	}
	return session, nil
}

func (m *terminalManager) releaseWriterForDevice(controllerDeviceID string) int {
	m.mu.Lock()
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	released := 0
	for _, session := range sessions {
		if session.releaseWriter(controllerDeviceID) {
			released++
		}
	}
	return released
}

func (a *app) releaseTerminalWritersForDevice(controllerDeviceID string) int {
	a.terminalMu.Lock()
	manager := a.terminals
	a.terminalMu.Unlock()
	if manager == nil {
		return 0
	}
	return manager.releaseWriterForDevice(controllerDeviceID)
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
		viewers:        map[string]*terminalViewer{},
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
		frames = append(frames, s.streamFrameLocked(terminalFrameOutput, chunk, ""))
	}
	viewers := s.viewersSnapshotLocked()
	s.mu.Unlock()
	for _, frame := range frames {
		s.sendToViewers(frame, viewers)
	}
}

func (s *terminalSession) attachViewer(viewer *terminalViewer) (terminalAttachResult, *terminalViewer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != terminalStatusOpen {
		return terminalAttachResult{}, nil, newActionError(http.StatusGone, "terminal_closed", "terminal is closed")
	}
	s.cancelRetentionLocked()
	replaced := s.viewers[viewer.connectionID]
	s.viewers[viewer.connectionID] = viewer
	return s.attachResultLocked(viewer.connectionID, viewer.controllerDeviceID), replaced, nil
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

func (s *terminalSession) releaseWriter(controllerDeviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != terminalStatusOpen || s.writerDeviceID != controllerDeviceID {
		return false
	}
	s.writerDeviceID = ""
	s.updatedAt = time.Now().UTC()
	return true
}

func (s *terminalSession) sendToViewers(frame terminalStreamFrame, viewers []*terminalViewer) {
	for _, viewer := range viewers {
		if viewer.enqueue(frame) {
			continue
		}
		if _, removed := s.detachViewer(viewer.connectionID); removed != nil {
			removed.close()
		}
	}
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
	return terminalAttachResult{
		TerminalID:     s.id,
		WorkspaceID:    s.workspaceID,
		Target:         s.target,
		Status:         s.status,
		ViewerDeviceID: viewerDeviceID,
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
	return terminalAckResult{TerminalID: s.id, Status: s.status, OutputSeq: s.outputSeq, WriterDeviceID: s.writerDeviceID}
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

func newTerminalViewer(conn *controlWSConn) *terminalViewer {
	return &terminalViewer{
		connectionID:       conn.id,
		controllerDeviceID: conn.controllerDeviceID,
		conn:               conn,
		frames:             make(chan terminalStreamFrame, terminalViewerBuffer),
	}
}

func (v *terminalViewer) run() {
	for frame := range v.frames {
		v.conn.writePlain(controlPlainFrame{Type: frame.frameType, Terminal: &frame})
	}
}

func (v *terminalViewer) enqueue(frame terminalStreamFrame) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return false
	}
	select {
	case v.frames <- frame:
		return true
	default:
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

func localTerminalCWD(ws Workspace, requested string) (string, error) {
	root := filepath.Clean(ws.LocalCWD)
	if root == "" || root == "." {
		return "", newActionError(http.StatusBadRequest, "workspace_cwd_empty", "workspace local cwd is empty")
	}
	if requested == "" {
		if err := ensureLocalControlWorkspaceExistingPath(root, root); err != nil {
			return "", err
		}
		return root, nil
	}
	target, _, err := resolveWorkspacePath(root, requested)
	if err != nil {
		return "", newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if err := ensureLocalControlWorkspaceExistingPath(root, target); err != nil {
		return "", err
	}
	return target, nil
}
