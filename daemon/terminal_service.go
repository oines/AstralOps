package main

import (
	"context"
	"net/http"

	internalterminal "github.com/oines/astralops/daemon/internal/core/terminal"
	"github.com/oines/astralops/pkg/protocol"
)

type terminalControlConnectionAdapter struct {
	conn controlConnection
}

type terminalWorkspaceAdapter struct {
	store *store
}

func (a terminalWorkspaceAdapter) GetWorkspace(id string) (protocol.Workspace, bool) {
	if a.store == nil {
		return protocol.Workspace{}, false
	}
	return a.store.getWorkspace(id)
}

type terminalEventEmitter struct {
	emitFn func(protocol.AstralEvent)
}

func (e terminalEventEmitter) Emit(ev protocol.AstralEvent) {
	if e.emitFn != nil {
		e.emitFn(ev)
	}
}

func (c terminalControlConnectionAdapter) ConnectionID() string {
	if c.conn == nil {
		return ""
	}
	return c.conn.connectionID()
}

func (c terminalControlConnectionAdapter) ControllerID() string {
	if c.conn == nil {
		return ""
	}
	return c.conn.controllerID()
}

func (c terminalControlConnectionAdapter) RequestContext() context.Context {
	if c.conn == nil {
		return context.Background()
	}
	return c.conn.requestContext()
}

func (c terminalControlConnectionAdapter) WriteTerminalFrame(frameType string, frame any) {
	if c.conn == nil {
		return
	}
	payload, _ := frame.(terminalStreamFrame)
	c.conn.writePlain(controlPlainFrame{Type: frameType, Terminal: &payload})
}

func (c terminalControlConnectionAdapter) WriteTerminalError(code string, message string) {
	if c.conn == nil {
		return
	}
	c.conn.writePlain(controlPlainFrame{
		Type: "response",
		Response: &ControlResponse{
			OK: false,
			Error: &ControlError{
				Status:  http.StatusServiceUnavailable,
				Code:    ControlErrorCode(code),
				Message: message,
			},
		},
	})
}

func (c terminalControlConnectionAdapter) TerminateTerminalConnection(code string, message string) {
	if terminator, ok := c.conn.(interface{ terminateControlConnection(string, string) }); ok {
		terminator.terminateControlConnection(code, message)
	}
}

func (a *app) detachTerminalViewersForControlSession(connectionID, reason string) {
	a.terminalService().DetachConnection(context.Background(), connectionID, reason)
}

func (a *app) releaseTerminalWritersForDevice(controllerDeviceID string) int {
	// Compatibility metric for older control responses; shared-input terminals no longer track per-controller writers.
	return 0
}

func (a *app) terminalService() *internalterminal.Service {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	if a.terminalCore == nil {
		a.terminalCore = internalterminal.New(&terminalCoreDelegate{
			app:     a,
			manager: internalterminal.NewManager(terminalManagerDeps(a)),
		})
	}
	if delegate, _ := a.terminalCore.Delegate().(*terminalCoreDelegate); delegate != nil && delegate.manager != nil {
		delegate.manager.UpdateDependencies(terminalManagerDeps(a))
	}
	return a.terminalCore
}

func terminalManagerDeps(a *app) internalterminal.Deps {
	return internalterminal.Deps{
		Workspaces: terminalWorkspaceAdapter{store: a.store},
		Events:     terminalEventEmitter{emitFn: a.emit},
		SSH:        a.sshService(),
	}
}

type terminalCoreDelegate struct {
	app     *app
	manager *internalterminal.Manager
}

func (d terminalCoreDelegate) List(context.Context) ([]protocol.TerminalTab, error) {
	if d.app == nil {
		return nil, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.ListTabs(), nil
}

func (d terminalCoreDelegate) Open(ctx context.Context, params protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.OpenForController(ctx, d.app.store.hostInfo().Identity.DeviceID, params)
}

func (d terminalCoreDelegate) OpenResult(_ context.Context, terminalID string) (protocol.TerminalOpenResult, bool) {
	if d.app == nil {
		return protocol.TerminalOpenResult{}, false
	}
	return d.manager.OpenTerminalResult(terminalID)
}

func (d terminalCoreDelegate) OpenForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	if d.app == nil {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.Open(ctx, controllerDeviceID, params)
}

func (d terminalCoreDelegate) AttachForController(_ context.Context, controllerDeviceID string, conn any, params protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error) {
	if d.app == nil {
		return protocol.TerminalAttachResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	controlConn, ok := conn.(controlConnection)
	if !ok || controlConn == nil {
		return protocol.TerminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.attach requires an encrypted control connection")
	}
	return d.manager.Attach(controllerDeviceID, terminalControlConnectionAdapter{conn: controlConn}, params)
}

func (d terminalCoreDelegate) DetachForController(_ context.Context, controllerDeviceID string, conn any, params protocol.TerminalDetachParams) (protocol.TerminalAttachResult, error) {
	if d.app == nil {
		return protocol.TerminalAttachResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	controlConn, ok := conn.(controlConnection)
	if !ok || controlConn == nil {
		return protocol.TerminalAttachResult{}, newActionError(http.StatusBadRequest, "control_connection_required", "terminal.detach requires an encrypted control connection")
	}
	return d.manager.Detach(controllerDeviceID, terminalControlConnectionAdapter{conn: controlConn}, params)
}

func (d terminalCoreDelegate) Input(ctx context.Context, params protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.InputForController(ctx, d.app.store.hostInfo().Identity.DeviceID, params)
}

func (d terminalCoreDelegate) InputForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	if d.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.Input(ctx, controllerDeviceID, params)
}

func (d terminalCoreDelegate) Resize(ctx context.Context, params protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.ResizeForController(ctx, d.app.store.hostInfo().Identity.DeviceID, params)
}

func (d terminalCoreDelegate) ResizeForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	if d.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.Resize(ctx, controllerDeviceID, params)
}

func (d terminalCoreDelegate) HeartbeatAckForController(_ context.Context, controllerDeviceID string, params protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error) {
	if d.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.HeartbeatAck(controllerDeviceID, params)
}

func (d terminalCoreDelegate) Close(ctx context.Context, params protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.CloseForController(ctx, d.app.store.hostInfo().Identity.DeviceID, params)
}

func (d terminalCoreDelegate) CloseForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	if d.app == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	return d.manager.Close(ctx, controllerDeviceID, params)
}

func (d terminalCoreDelegate) OpenLegacyWorkspace(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	ws, ok := d.app.store.getWorkspace(params.WorkspaceID)
	if !ok {
		return protocol.TerminalOpenResult{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	return d.manager.Open(ctx, d.app.store.hostInfo().Identity.DeviceID, protocol.TerminalOpenParams{WorkspaceID: ws.ID, Cols: internalterminal.DefaultCols, Rows: internalterminal.DefaultRows})
}

func (d terminalCoreDelegate) CloseLegacyWorkspace(ctx context.Context, workspaceID string, terminalID string) (protocol.TerminalAckResult, error) {
	if d.app == nil || d.app.store == nil {
		return protocol.TerminalAckResult{}, newActionError(http.StatusServiceUnavailable, "terminal_commands_unavailable", "terminal commands are not initialized")
	}
	ws, ok := d.app.store.getWorkspace(workspaceID)
	if !ok {
		return protocol.TerminalAckResult{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	open, ok := d.manager.OpenTerminalResult(terminalID)
	if !ok || open.WorkspaceID != ws.ID {
		return protocol.TerminalAckResult{}, newActionError(http.StatusNotFound, "terminal_not_found", "terminal not found")
	}
	return d.manager.Close(ctx, d.app.store.hostInfo().Identity.DeviceID, protocol.TerminalCloseParams{TerminalID: terminalID})
}

func (d terminalCoreDelegate) CloseWorkspace(ctx context.Context, workspaceID string, reason string) {
	if d.app == nil {
		return
	}
	d.manager.CloseWorkspace(ctx, workspaceID, reason)
}

func (d terminalCoreDelegate) DetachConnection(_ context.Context, connectionID string, reason string) {
	if d.app == nil {
		return
	}
	d.manager.DetachConnection(connectionID, reason)
}
