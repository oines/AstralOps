package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

type legacyControlGatewayAdapter struct {
	app *app
}

var _ ports.ControlGateway = legacyControlGatewayAdapter{}

func (a *app) controlGateway() ports.ControlGateway {
	return legacyControlGatewayAdapter{app: a}
}

func (g legacyControlGatewayAdapter) Dispatch(ctx context.Context, req protocol.ControlRequest) (protocol.ControlResponse, error) {
	if g.app == nil {
		return protocol.ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusServiceUnavailable, "control_gateway_unavailable", "control gateway is not initialized")
	}
	return g.app.remoteControlService().executeControlRequestWithContext(ctx, req, nil)
}

type legacySessionCommandsAdapter struct {
	app *app
}

var _ ports.SessionCommands = legacySessionCommandsAdapter{}

func (a *app) sessionCommands() ports.SessionCommands {
	return legacySessionCommandsAdapter{app: a}
}

func (s legacySessionCommandsAdapter) CreateSession(_ context.Context, req protocol.CreateSessionRequest) (protocol.Session, error) {
	if s.app == nil {
		return protocol.Session{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	return s.app.sessionControlPlane().CreateSession(req.WorkspaceID, req.Agent)
}

func (s legacySessionCommandsAdapter) ReadSessions(_ context.Context, params protocol.SessionsReadParams) ([]protocol.Session, error) {
	if s.app == nil || s.app.store == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	return s.app.store.listSessions(params.WorkspaceID), nil
}

func (s legacySessionCommandsAdapter) ReadSessionView(_ context.Context, params protocol.SessionReferenceParams) (protocol.SessionView, error) {
	if s.app == nil {
		return protocol.SessionView{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	view, ok := s.app.buildSessionView(params.SessionID)
	if !ok {
		return protocol.SessionView{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	return view, nil
}

func (s legacySessionCommandsAdapter) StartInput(_ context.Context, params ports.SessionInputParams) (protocol.SessionInputResult, error) {
	if s.app == nil {
		return protocol.SessionInputResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().StartSessionInput(params.SessionID, params.Input, TurnOptions{
		Model:           params.Model,
		ReasoningEffort: params.ReasoningEffort,
		PermissionMode:  params.PermissionMode,
		Attachments:     params.Attachments,
	})
	if err != nil {
		return protocol.SessionInputResult{}, err
	}
	return decodeLegacyResult[protocol.SessionInputResult](result)
}

func (s legacySessionCommandsAdapter) CancelQueue(_ context.Context, params protocol.QueueControlParams) (protocol.QueueControlResult, error) {
	if s.app == nil {
		return protocol.QueueControlResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().CancelControlQueuedTurn(params)
	if err != nil {
		return protocol.QueueControlResult{}, err
	}
	return decodeLegacyResult[protocol.QueueControlResult](result)
}

func (s legacySessionCommandsAdapter) SteerQueue(_ context.Context, params protocol.QueueControlParams) (protocol.QueueControlResult, error) {
	if s.app == nil {
		return protocol.QueueControlResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().SteerControlQueuedTurn(params)
	if err != nil {
		return protocol.QueueControlResult{}, err
	}
	return decodeLegacyResult[protocol.QueueControlResult](result)
}

func (s legacySessionCommandsAdapter) CancelTurn(_ context.Context, params protocol.SessionReferenceParams) (protocol.OkResult, error) {
	if s.app == nil {
		return protocol.OkResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().InterruptSession(params.SessionID)
	if err != nil {
		return protocol.OkResult{}, err
	}
	return decodeLegacyResult[protocol.OkResult](result)
}

func (s legacySessionCommandsAdapter) RespondInteraction(_ context.Context, params protocol.InteractionRespondParams) (protocol.OkResult, error) {
	if s.app == nil {
		return protocol.OkResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().RespondInteraction(params.InteractionID, params.Response)
	if err != nil {
		return protocol.OkResult{}, err
	}
	return decodeLegacyResult[protocol.OkResult](result)
}

func (s legacySessionCommandsAdapter) ForkSession(_ context.Context, params protocol.SessionForkControlParams) (protocol.ForkSessionResponse, error) {
	if s.app == nil {
		return protocol.ForkSessionResponse{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	return s.app.sessionControlPlane().ForkSession(params.SessionID, protocol.ForkSessionRequest{EventSeq: params.EventSeq})
}

func (s legacySessionCommandsAdapter) EditLastUserMessage(_ context.Context, params protocol.SessionEditParams) (protocol.OkResult, error) {
	if s.app == nil {
		return protocol.OkResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	result, err := s.app.sessionControlPlane().EditLastUserMessage(params.SessionID, protocol.EditLastUserMessageRequest{
		EventSeq:        params.EventSeq,
		Input:           params.Input,
		Model:           params.Model,
		ReasoningEffort: params.ReasoningEffort,
		PermissionMode:  params.PermissionMode,
	})
	if err != nil {
		return protocol.OkResult{}, err
	}
	return decodeLegacyResult[protocol.OkResult](result)
}

func (s legacySessionCommandsAdapter) DeleteSession(_ context.Context, params protocol.SessionDeleteParams) (protocol.SessionDeleteResult, error) {
	if s.app == nil {
		return protocol.SessionDeleteResult{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	return s.app.sessionControlPlane().DeleteSessionByID(params.SessionID)
}

func (s legacySessionCommandsAdapter) ListCommands(_ context.Context, params protocol.SessionReferenceParams) (protocol.SessionCommandListResponse, error) {
	if s.app == nil {
		return protocol.SessionCommandListResponse{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	commands, ok := s.app.listSessionCommands(params.SessionID)
	if !ok {
		return protocol.SessionCommandListResponse{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	return protocol.SessionCommandListResponse{Commands: commands}, nil
}

func (s legacySessionCommandsAdapter) RunCommand(_ context.Context, params ports.SessionCommandRunParams) (protocol.SessionCommandResponse, error) {
	if s.app == nil {
		return protocol.SessionCommandResponse{}, newActionError(http.StatusServiceUnavailable, "session_commands_unavailable", "session commands are not initialized")
	}
	return s.app.runSessionCommandResult(params.SessionID, params.CommandID, params.Request)
}

func (a *app) runSessionCommandResult(sessionID, commandID string, req protocol.SessionCommandRequest) (protocol.SessionCommandResponse, error) {
	commands, ok := a.listSessionCommands(sessionID)
	if !ok {
		return protocol.SessionCommandResponse{}, newActionError(http.StatusNotFound, "session_not_found", "session not found")
	}
	var command protocol.SessionCommand
	for _, item := range commands {
		if item.ID == commandID {
			command = item
			break
		}
	}
	if command.ID == "" {
		return protocol.SessionCommandResponse{}, newActionError(http.StatusNotFound, "session_command_not_found", "command not found")
	}
	if !command.Enabled {
		return protocol.SessionCommandResponse{}, newActionError(http.StatusConflict, "session_command_disabled", firstString(command.DisabledReason, "command is disabled"))
	}
	ss, _ := a.store.getSession(sessionID)
	ws, _ := a.store.getWorkspace(ss.WorkspaceID)
	if command.Kind != commandKindClient && command.ID != "status" {
		linked, err := a.linkSessionForCommand(ss)
		if err != nil {
			return protocol.SessionCommandResponse{}, err
		}
		ss = linked
		ws, _ = a.store.getWorkspace(ss.WorkspaceID)
	}
	switch command.Kind {
	case commandKindPrompt:
		input := firstString(mapValue(command.Payload)["input"], "/"+strings.TrimPrefix(command.ID, "claude:"))
		return a.startSessionPromptCommandResult(ss, ws, input)
	case commandKindAction:
		if command.ID == "status" {
			a.emitSessionStatusSnapshot(ss)
			return protocol.SessionCommandResponse{OK: true}, nil
		}
		runtime, ok := a.runtimes[ss.Agent]
		if !ok {
			return protocol.SessionCommandResponse{}, newActionError(http.StatusNotImplemented, "runtime_not_implemented", "agent runtime is not implemented")
		}
		runner, ok := runtime.(CommandRunner)
		if !ok {
			if ss.Agent == AgentClaude && command.ID == "compact" {
				return a.startSessionPromptCommandResult(ss, ws, "/compact")
			}
			return protocol.SessionCommandResponse{}, newActionError(http.StatusNotImplemented, "session_command_not_implemented", "command is not implemented for this agent")
		}
		if err := runner.RunCommand(ss, ws, command.ID, req.Args); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrSessionRunning) {
				status = http.StatusConflict
			}
			return protocol.SessionCommandResponse{}, newActionError(status, "session_command_failed", err.Error())
		}
		return protocol.SessionCommandResponse{OK: true}, nil
	default:
		return protocol.SessionCommandResponse{}, newActionError(http.StatusBadRequest, "session_command_client_only", "client command cannot be executed by daemon")
	}
}

func (a *app) startSessionPromptCommandResult(ss Session, _ Workspace, input string) (protocol.SessionCommandResponse, error) {
	result, err := a.sessionControlPlane().StartSessionInput(ss.ID, input, TurnOptions{})
	if err != nil {
		var actionErr *actionError
		if errors.As(err, &actionErr) && actionErr.Code == "runtime_error" {
			return protocol.SessionCommandResponse{}, newActionError(actionErr.Status, "session_command_failed", actionErr.Message)
		}
		return protocol.SessionCommandResponse{}, err
	}
	response := protocol.SessionCommandResponse{OK: true}
	if boolValue(result["queued"]) {
		response.Queued = true
		response.QueueID = stringValue(result["queue_id"])
	}
	if strings.TrimSpace(input) == "/compact" && stringValue(result["mode"]) == "start" {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "memory.compacting", Normalized: eventNormalized("memory.compacting", map[string]any{
			"source":  "astralops",
			"command": "compact",
			"status":  "running",
		})})
	}
	return response, nil
}

type legacyWorkspaceCommandsAdapter struct {
	app *app
}

var _ ports.WorkspaceCommands = legacyWorkspaceCommandsAdapter{}

func (a *app) workspaceCommands() ports.WorkspaceCommands {
	return legacyWorkspaceCommandsAdapter{app: a}
}

func (w legacyWorkspaceCommandsAdapter) CreateWorkspace(_ context.Context, req protocol.CreateWorkspaceRequest) (protocol.Workspace, error) {
	if w.app == nil {
		return protocol.Workspace{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.createWorkspace(createWorkspaceRequest(req))
}

func (w legacyWorkspaceCommandsAdapter) ReadWorkspaces(context.Context) ([]protocol.Workspace, error) {
	if w.app == nil || w.app.store == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.store.listWorkspaces(), nil
}

func (w legacyWorkspaceCommandsAdapter) ReadWorkspace(_ context.Context, params protocol.WorkspaceReferenceParams) (protocol.Workspace, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return protocol.Workspace{}, err
	}
	return ws, nil
}

func (w legacyWorkspaceCommandsAdapter) DeleteWorkspace(_ context.Context, params protocol.WorkspaceReferenceParams) (protocol.OkResult, error) {
	if w.app == nil {
		return protocol.OkResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	if _, err := w.app.deleteWorkspace(params.WorkspaceID); err != nil {
		return protocol.OkResult{}, err
	}
	return protocol.OkResult{OK: true}, nil
}

func (w legacyWorkspaceCommandsAdapter) ListNativeSessions(_ context.Context, params protocol.NativeSessionsReadParams) (protocol.NativeSessionListResponse, error) {
	if w.app == nil || w.app.store == nil {
		return protocol.NativeSessionListResponse{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	if _, ok := w.app.store.getWorkspace(params.WorkspaceID); !ok {
		return protocol.NativeSessionListResponse{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	return protocol.NativeSessionListResponse{Sessions: w.app.store.listNativeSessionCandidates(params.WorkspaceID)}, nil
}

func (w legacyWorkspaceCommandsAdapter) ImportNativeSession(_ context.Context, params protocol.NativeSessionImportParams) (protocol.NativeSessionImportResponse, error) {
	if w.app == nil || w.app.store == nil {
		return protocol.NativeSessionImportResponse{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	if _, ok := w.app.store.getWorkspace(params.WorkspaceID); !ok {
		return protocol.NativeSessionImportResponse{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	session, err := w.app.store.importNativeSession(params.WorkspaceID, params.SessionID)
	if err != nil {
		return protocol.NativeSessionImportResponse{}, newActionError(http.StatusNotFound, "native_session_not_found", err.Error())
	}
	w.app.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "session.started", Normalized: eventNormalized("session.started", session)})
	return protocol.NativeSessionImportResponse{Session: session}, nil
}

func (w legacyWorkspaceCommandsAdapter) BrowseHostFileSystem(ctx context.Context, params protocol.HostFileSystemBrowseParams) (protocol.HostFileSystemBrowseResult, error) {
	if w.app == nil {
		return protocol.HostFileSystemBrowseResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.browseHostFileSystem(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) ReadLegacyWorkspaceFiles(ctx context.Context, params ports.LegacyWorkspaceFilesParams) (ports.LegacyWorkspaceFilesResult, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return ports.LegacyWorkspaceFilesResult{}, err
	}
	return w.app.legacyWorkspaceFilesResult(ctx, ws, params.Path)
}

func (w legacyWorkspaceCommandsAdapter) ExecLegacyWorkspaceCommand(ctx context.Context, params ports.LegacyWorkspaceExecParams) (map[string]any, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return w.app.legacyWorkspaceExecResult(ctx, ws, params.Command)
}

func (w legacyWorkspaceCommandsAdapter) ReadWorkspaceConnection(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return protocol.WorkspaceConnection{}, err
	}
	ssh := w.app.sshService()
	if ssh == nil {
		return protocol.WorkspaceConnection{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "ssh manager unavailable")
	}
	return ssh.Connection(ctx, ws), nil
}

func (w legacyWorkspaceCommandsAdapter) ConnectWorkspace(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return protocol.WorkspaceConnection{}, err
	}
	ssh := w.app.sshService()
	if ssh == nil {
		return protocol.WorkspaceConnection{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "ssh manager unavailable")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return ssh.Connect(connectCtx, ws)
}

func (w legacyWorkspaceCommandsAdapter) DisconnectWorkspace(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	ws, err := w.workspace(params.WorkspaceID)
	if err != nil {
		return protocol.WorkspaceConnection{}, err
	}
	ssh := w.app.sshService()
	if ssh == nil {
		return protocol.WorkspaceConnection{}, newActionError(http.StatusServiceUnavailable, "ssh_unavailable", "ssh manager unavailable")
	}
	return ssh.Disconnect(ctx, ws), nil
}

func (w legacyWorkspaceCommandsAdapter) ReadFiles(ctx context.Context, params protocol.WorkspaceFilesReadParams) (protocol.WorkspaceFilesReadResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFilesReadResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.readControlWorkspaceFiles(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) WriteFiles(ctx context.Context, params protocol.WorkspaceFilesWriteParams) (protocol.WorkspaceFilesWriteResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFilesWriteResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.writeControlWorkspaceFile(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) ApplyPatch(ctx context.Context, params protocol.WorkspaceFilesApplyPatchParams) (protocol.WorkspaceFilesApplyPatchResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFilesApplyPatchResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.applyControlWorkspacePatch(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) DeleteFiles(ctx context.Context, params protocol.WorkspaceFilesDeleteParams) (protocol.WorkspaceFilesDeleteResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFilesDeleteResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.deleteControlWorkspacePath(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) MoveFiles(ctx context.Context, params protocol.WorkspaceFilesMoveParams) (protocol.WorkspaceFilesMoveResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFilesMoveResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.moveControlWorkspacePath(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) StreamFile(ctx context.Context, params protocol.WorkspaceFilesStreamParams) (protocol.WorkspaceFileStreamResult, error) {
	if w.app == nil {
		return protocol.WorkspaceFileStreamResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.prepareControlWorkspaceFileStream(ctx, params)
}

func (w legacyWorkspaceCommandsAdapter) CancelFileStream(context.Context, protocol.WorkspaceFileStreamCancelParams) (protocol.WorkspaceFileStreamCancelResult, error) {
	return protocol.WorkspaceFileStreamCancelResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "workspace_file.stream.cancel requires an encrypted control connection")
}

func (w legacyWorkspaceCommandsAdapter) Exec(ctx context.Context, params protocol.WorkspaceExecParams) (protocol.WorkspaceExecResult, error) {
	if w.app == nil {
		return protocol.WorkspaceExecResult{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	return w.app.executeControlWorkspaceCommand(ctx, params, TrustGrant{})
}

func (w legacyWorkspaceCommandsAdapter) workspace(workspaceID string) (protocol.Workspace, error) {
	if w.app == nil || w.app.store == nil {
		return protocol.Workspace{}, newActionError(http.StatusServiceUnavailable, "workspace_commands_unavailable", "workspace commands are not initialized")
	}
	ws, ok := w.app.store.getWorkspace(workspaceID)
	if !ok {
		return protocol.Workspace{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	return ws, nil
}

type legacyTerminalCommandsAdapter struct {
	app *app
}

var _ ports.TerminalCommands = legacyTerminalCommandsAdapter{}

func (a *app) terminalCommands() ports.TerminalCommands {
	return legacyTerminalCommandsAdapter{app: a}
}

func (t legacyTerminalCommandsAdapter) ListTerminals(context.Context) ([]protocol.TerminalTab, error) {
	if t.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().List(context.Background())
}

func (t legacyTerminalCommandsAdapter) OpenTerminal(ctx context.Context, params protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	if t.app == nil {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().Open(ctx, params)
}

func (t legacyTerminalCommandsAdapter) AttachTerminal(context.Context, protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error) {
	return protocol.TerminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.attach requires an encrypted control connection")
}

func (t legacyTerminalCommandsAdapter) DetachTerminal(context.Context, protocol.TerminalDetachParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.detach requires an encrypted control connection")
}

func (t legacyTerminalCommandsAdapter) InputTerminal(ctx context.Context, params protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	if t.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().Input(ctx, params)
}

func (t legacyTerminalCommandsAdapter) ResizeTerminal(ctx context.Context, params protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	if t.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().Resize(ctx, params)
}

func (t legacyTerminalCommandsAdapter) CloseTerminal(ctx context.Context, params protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	if t.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().Close(ctx, params)
}

func (t legacyTerminalCommandsAdapter) AckTerminalHeartbeat(context.Context, protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.heartbeat_ack requires an encrypted control connection")
}

func (t legacyTerminalCommandsAdapter) OpenLegacyWorkspaceTerminal(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error) {
	if t.app == nil {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().OpenLegacyWorkspace(ctx, params)
}

func (t legacyTerminalCommandsAdapter) CloseLegacyWorkspaceTerminal(ctx context.Context, params ports.LegacyWorkspaceTerminalCloseParams) (protocol.TerminalAckResult, error) {
	if t.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return t.app.terminalService().CloseLegacyWorkspace(ctx, params.WorkspaceID, params.TerminalID)
}

type legacyWorkspacePassthroughAdapter struct {
	app *app
}

var _ ports.LegacyWorkspacePassthrough = legacyWorkspacePassthroughAdapter{}

func (a *app) workspacePassthrough() ports.LegacyWorkspacePassthrough {
	return legacyWorkspacePassthroughAdapter{app: a}
}

func (p legacyWorkspacePassthroughAdapter) ServeWorkspacePTY(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ws, ok := p.workspace(w, workspaceID)
	if !ok {
		return
	}
	p.app.handleWorkspacePTY(w, r, ws)
}

func (p legacyWorkspacePassthroughAdapter) ServeClaudeRemoteTool(w http.ResponseWriter, r *http.Request, workspaceID string) {
	ws, ok := p.workspace(w, workspaceID)
	if !ok {
		return
	}
	p.app.handleClaudeRemoteTool(w, r, ws)
}

func (p legacyWorkspacePassthroughAdapter) workspace(w http.ResponseWriter, workspaceID string) (protocol.Workspace, bool) {
	if p.app == nil || p.app.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace commands are not initialized"})
		return protocol.Workspace{}, false
	}
	ws, ok := p.app.store.getWorkspace(workspaceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return protocol.Workspace{}, false
	}
	return ws, true
}

type legacyPairingCommandsAdapter struct {
	app *app
}

var _ ports.PairingCommands = legacyPairingCommandsAdapter{}

func (a *app) pairingCommands() ports.PairingCommands {
	return legacyPairingCommandsAdapter{app: a}
}

func (p legacyPairingCommandsAdapter) ListPairingRequests(context.Context) (protocol.PairingRequestListResult, error) {
	if p.app == nil || p.app.store == nil {
		return protocol.PairingRequestListResult{}, newActionError(http.StatusServiceUnavailable, "pairing_commands_unavailable", "pairing commands are not initialized")
	}
	return protocol.PairingRequestListResult{Requests: p.app.store.listPairingRequests()}, nil
}

func (p legacyPairingCommandsAdapter) SubmitPairingRequest(_ context.Context, req ports.PairingRequestInput) (ports.PairingRequestSubmitResult, error) {
	if p.app == nil || p.app.store == nil {
		return ports.PairingRequestSubmitResult{}, newActionError(http.StatusServiceUnavailable, "pairing_commands_unavailable", "pairing commands are not initialized")
	}
	request, err := p.app.store.submitPairingRequest(pairingRequestInput(req))
	if err != nil {
		return ports.PairingRequestSubmitResult{}, err
	}
	p.app.emitPairingRequested(request)
	return ports.PairingRequestSubmitResult{Request: request}, nil
}

func (p legacyPairingCommandsAdapter) ReadPairingRequest(_ context.Context, params protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	if p.app == nil || p.app.store == nil {
		return protocol.PairingRequestResolveResult{}, newActionError(http.StatusServiceUnavailable, "pairing_commands_unavailable", "pairing commands are not initialized")
	}
	request, found := p.app.store.pairingRequest(params.RequestID)
	if !found {
		return protocol.PairingRequestResolveResult{}, newActionError(http.StatusNotFound, "pairing_request_not_found", "pairing request not found")
	}
	return protocol.PairingRequestResolveResult{Request: request}, nil
}

func (p legacyPairingCommandsAdapter) ApprovePairingRequest(_ context.Context, params protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	if p.app == nil {
		return protocol.PairingRequestResolveResult{}, newActionError(http.StatusServiceUnavailable, "pairing_commands_unavailable", "pairing commands are not initialized")
	}
	return p.app.approvePairingRequest(params.RequestID)
}

func (p legacyPairingCommandsAdapter) DenyPairingRequest(_ context.Context, params protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	if p.app == nil {
		return protocol.PairingRequestResolveResult{}, newActionError(http.StatusServiceUnavailable, "pairing_commands_unavailable", "pairing commands are not initialized")
	}
	return p.app.denyPairingRequest(params.RequestID)
}

type legacyTrustCommandsAdapter struct {
	app *app
}

var _ ports.TrustCommands = legacyTrustCommandsAdapter{}

func (a *app) trustCommands() ports.TrustCommands {
	return legacyTrustCommandsAdapter{app: a}
}

func (t legacyTrustCommandsAdapter) ListTrustedDevices(context.Context) (protocol.HostTrustListResult, error) {
	if t.app == nil || t.app.store == nil {
		return protocol.HostTrustListResult{}, newActionError(http.StatusServiceUnavailable, "trust_commands_unavailable", "trust commands are not initialized")
	}
	return protocol.HostTrustListResult{Grants: t.app.store.listTrustGrants()}, nil
}

func (t legacyTrustCommandsAdapter) TrustDevice(_ context.Context, req ports.TrustDeviceRequest) (protocol.TrustGrant, error) {
	if t.app == nil || t.app.store == nil {
		return protocol.TrustGrant{}, newActionError(http.StatusServiceUnavailable, "trust_commands_unavailable", "trust commands are not initialized")
	}
	grant, err := t.app.store.trustDevice(trustDeviceRequest(req))
	if err != nil {
		return protocol.TrustGrant{}, err
	}
	t.app.emit(AstralEvent{
		Kind: "control.trust.granted",
		Normalized: eventNormalized("control.trust.granted",
			map[string]any{
				"host_device_id":       grant.HostDeviceID,
				"controller_device_id": grant.ControllerDeviceID,
				"scope":                grant.Scope,
				"capabilities":         grant.Capabilities,
			}),
	})
	return grant, nil
}

func (t legacyTrustCommandsAdapter) RevokeTrustedDevice(_ context.Context, params protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error) {
	if t.app == nil {
		return protocol.HostTrustRevokeResult{}, newActionError(http.StatusServiceUnavailable, "trust_commands_unavailable", "trust commands are not initialized")
	}
	return t.app.revokeTrustedControlDevice(params.ControllerDeviceID, "")
}

type legacyMeshCommandsAdapter struct {
	app *app
}

var _ ports.MeshCommands = legacyMeshCommandsAdapter{}

func (a *app) meshCommands() ports.MeshCommands {
	return legacyMeshCommandsAdapter{app: a}
}

func (m legacyMeshCommandsAdapter) ReadMeshState(ctx context.Context, params ports.MeshStateParams) (any, error) {
	if m.app == nil || m.app.mesh == nil {
		return nil, newActionError(http.StatusInternalServerError, "mesh_state_unavailable", "mesh state is not initialized")
	}
	return m.app.mesh.refresh(ctx, params.Discover)
}

func (m legacyMeshCommandsAdapter) ServeMeshStateStream(w http.ResponseWriter, r *http.Request) {
	if m.app == nil || m.app.mesh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mesh state is not initialized"})
		return
	}
	m.app.mesh.stream(w, r)
}

type legacyRemoteHostCommandsAdapter struct {
	app *app
}

var _ ports.RemoteHostCommands = legacyRemoteHostCommandsAdapter{}

func (a *app) remoteHostCommands() ports.RemoteHostCommands {
	return legacyRemoteHostCommandsAdapter{app: a}
}

func (h legacyRemoteHostCommandsAdapter) ListRemoteHosts(ctx context.Context, params ports.RemoteHostsListParams) (any, error) {
	if h.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "remote_host_commands_unavailable", "remote host commands are not initialized")
	}
	if h.app.mesh != nil {
		state, err := h.app.mesh.refresh(ctx, params.Discover)
		if err != nil {
			return nil, err
		}
		return remoteHostsResponse{Hosts: state.Hosts}, nil
	}
	return remoteHostsResponse{Hosts: h.app.buildRemoteHostRecords(ctx, params.Discover)}, nil
}

func (h legacyRemoteHostCommandsAdapter) ServeRemoteHostAction(w http.ResponseWriter, r *http.Request) {
	if h.app == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "remote host commands are not initialized"})
		return
	}
	h.app.handleRemoteHostAction(w, r)
}

type legacyCloudCommandsAdapter struct {
	app *app
}

var _ ports.CloudCommands = legacyCloudCommandsAdapter{}

func (a *app) cloudCommands() ports.CloudCommands {
	return legacyCloudCommandsAdapter{app: a}
}

func (c legacyCloudCommandsAdapter) MeshState(ctx context.Context) (any, error) {
	return legacyMeshCommandsAdapter{app: c.app}.ReadMeshState(ctx, ports.MeshStateParams{Discover: true})
}

func (c legacyCloudCommandsAdapter) ApplySettings(_ context.Context, value any) (any, error) {
	if c.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "cloud_commands_unavailable", "cloud commands are not initialized")
	}
	var settings CloudSettings
	switch typed := value.(type) {
	case CloudSettings:
		settings = typed
	default:
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &settings); err != nil {
			return nil, err
		}
	}
	if err := c.app.applyCloudSettings(settings); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (c legacyCloudCommandsAdapter) Logout(ctx context.Context) (any, error) {
	if c.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "cloud_commands_unavailable", "cloud commands are not initialized")
	}
	return c.app.logoutCloudMesh(ctx, true)
}

func (c legacyCloudCommandsAdapter) ResolvePairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error) {
	return protocol.PairingRequestResolveResult{}, newActionError(http.StatusNotImplemented, "cloud_pairing_resolve_unavailable", "cloud pairing resolution requires an explicit status")
}

func (c legacyCloudCommandsAdapter) RevokeTrust(_ context.Context, params protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error) {
	if c.app == nil {
		return protocol.HostTrustRevokeResult{}, newActionError(http.StatusServiceUnavailable, "cloud_commands_unavailable", "cloud commands are not initialized")
	}
	return c.app.revokeTrustedControlDevice(params.ControllerDeviceID, "")
}

func (c legacyCloudCommandsAdapter) ServeCloudAuthAction(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudAuthAction(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudAccount(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudAccount(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudAccountRelay(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudAccountRelay(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudRelays(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudRelays(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudDevices(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudDevices(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudDeviceAction(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudDeviceAction(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudHeartbeat(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudHeartbeat(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudPairingRequests(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudPairingRequests(w, r)
}

func (c legacyCloudCommandsAdapter) ServeCloudPairingRequestAction(w http.ResponseWriter, r *http.Request) {
	c.app.handleCloudPairingRequestAction(w, r)
}

type legacyMediaCommandsAdapter struct {
	app *app
}

var _ ports.MediaCommands = legacyMediaCommandsAdapter{}

func (a *app) mediaCommands() ports.MediaCommands {
	return legacyMediaCommandsAdapter{app: a}
}

func (m legacyMediaCommandsAdapter) IngestAttachment(_ context.Context, params protocol.AttachmentIngestParams) (protocol.AttachmentIngestResult, error) {
	return m.app.ingestControlAttachment(params)
}

func (m legacyMediaCommandsAdapter) StartAttachmentIngest(_ context.Context, params protocol.AttachmentIngestStartParams) (protocol.AttachmentIngestStartResult, error) {
	return m.app.startControlAttachmentIngest(params)
}

func (m legacyMediaCommandsAdapter) AppendAttachmentChunk(_ context.Context, params protocol.AttachmentIngestChunkParams) (protocol.AttachmentIngestChunkResult, error) {
	return m.app.appendControlAttachmentChunk(params)
}

func (m legacyMediaCommandsAdapter) FinishAttachmentIngest(_ context.Context, params protocol.AttachmentIngestFinishParams) (protocol.AttachmentIngestFinishResult, error) {
	return m.app.finishControlAttachmentIngest(params)
}

func (m legacyMediaCommandsAdapter) ReadMedia(_ context.Context, params protocol.MediaReadParams) (protocol.MediaReadResult, error) {
	return m.app.readControlMedia(params, false)
}

func (m legacyMediaCommandsAdapter) ResolveSessionMedia(_ context.Context, params ports.SessionMediaParams) (ports.SessionMedia, error) {
	media, err := m.app.resolveSessionMedia(params.SessionID, params.EventSeq, params.MediaID)
	if err != nil {
		return ports.SessionMedia{}, err
	}
	return ports.SessionMedia{Path: media.Path, Name: media.Name, MIMEType: media.MIMEType}, nil
}

func (m legacyMediaCommandsAdapter) StreamMedia(_ context.Context, params protocol.MediaStreamParams) (protocol.MediaStreamResult, error) {
	return m.app.prepareControlMediaStream(params)
}

func (m legacyMediaCommandsAdapter) CancelMediaStream(_ context.Context, params protocol.MediaStreamCancelParams) (protocol.MediaStreamCancelResult, error) {
	return protocol.MediaStreamCancelResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "media.stream.cancel requires an encrypted control connection")
}

func decodeLegacyResult[T any](value any) (T, error) {
	var out T
	body, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	return out, nil
}

type legacyEventCommandsAdapter struct {
	app *app
}

var _ ports.EventCommands = legacyEventCommandsAdapter{}

func (a *app) eventCommands() ports.EventCommands {
	return legacyEventCommandsAdapter{app: a}
}

func (e legacyEventCommandsAdapter) QueryEvents(_ context.Context, params protocol.EventWindowParams) ([]protocol.AstralEvent, error) {
	if e.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "event_commands_unavailable", "event commands are not initialized")
	}
	return e.app.sessionProjections().QueryEventsWindow(params.WorkspaceID, params.SessionID, params.AfterSeq, params.BeforeSeq, params.Limit), nil
}

func (e legacyEventCommandsAdapter) ReplayEvents(_ context.Context, params ports.EventStreamParams) ([]protocol.AstralEvent, error) {
	if e.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "event_commands_unavailable", "event commands are not initialized")
	}
	return e.app.eventStreamReplayEvents(params.WorkspaceID, params.SessionID, params.AfterSeq), nil
}

func (e legacyEventCommandsAdapter) Subscribe(_ context.Context) (ports.EventSubscription, error) {
	if e.app == nil || e.app.hub == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "event_commands_unavailable", "event commands are not initialized")
	}
	client := e.app.hub.addSSE()
	return legacyEventSubscription{hub: e.app.hub, client: client}, nil
}

type legacyEventSubscription struct {
	hub    *eventHub
	client *sseClient
}

func (s legacyEventSubscription) Events() <-chan protocol.AstralEvent {
	return s.client.ch
}

func (s legacyEventSubscription) Close() {
	if s.hub != nil && s.client != nil {
		s.hub.removeSSE(s.client)
	}
}
