package main

import (
	"context"

	internalssh "github.com/oines/astralops/daemon/internal/ssh"
	"github.com/oines/astralops/pkg/protocol"
)

func (a *app) sshService() *internalssh.Service {
	if a == nil {
		return nil
	}
	internalssh.ConfigureDiagnostics(internalssh.Diagnostics{
		Enabled:            daemonDiagnosticLoggingEnabled,
		SpanStart:          logDiagnosticSpanStart,
		SpanCompleted:      logDiagnosticSpanCompleted,
		SpanFailed:         logDiagnosticSpanFailed,
		ProxyCallStart:     logSSHProxyCallStart,
		ProxyCallCompleted: logSSHProxyCallCompleted,
		ProxyCallFailed:    logSSHProxyCallFailed,
		LogTail:            diagnosticLogTail,
		CopyFields:         copyDiagnosticFields,
	})
	if a.sshCore == nil {
		a.sshCore = internalssh.New(&sshCoreDelegate{app: a, manager: newSSHManager(a)})
	}
	if delegate, _ := a.sshCore.Delegate().(*sshCoreDelegate); delegate != nil {
		delegate.app = a
		if delegate.manager == nil {
			delegate.manager = newSSHManager(a)
		} else {
			delegate.manager.UpdateDeps(sshDepsFromApp(a))
		}
	}
	return a.sshCore
}

type sshCoreDelegate struct {
	app     *app
	manager *internalssh.Manager
}

func (d sshCoreDelegate) RestorePersistedConnections(ctx context.Context) {
	if d.manager == nil {
		return
	}
	d.manager.RestorePersistedConnections(ctx)
}

func (d sshCoreDelegate) Call(ctx context.Context, workspace protocol.Workspace, action string, params any, out any) error {
	if d.manager == nil {
		return newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return d.manager.Call(ctx, workspace, action, params, out)
}

func (d sshCoreDelegate) Connection(_ context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	if d.manager == nil {
		return protocol.WorkspaceConnection{}
	}
	return d.manager.Connection(workspace)
}

func (d sshCoreDelegate) Connect(ctx context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	if d.manager == nil {
		return protocol.WorkspaceConnection{}, newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return d.manager.Connect(ctx, workspace)
}

func (d sshCoreDelegate) Disconnect(_ context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	if d.manager == nil {
		return protocol.WorkspaceConnection{}
	}
	return d.manager.Disconnect(workspace)
}

func (d sshCoreDelegate) RemoteWorkspaceRuntimeDir(workspace protocol.Workspace) string {
	if d.manager == nil {
		return ""
	}
	return d.manager.RemoteWorkspaceRuntimeDir(workspace)
}

func (d sshCoreDelegate) ProxyFor(ctx context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	if d.manager == nil {
		return protocol.WorkspaceConnection{}, newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	_, state, err := d.manager.ProxyFor(ctx, workspace)
	return state, err
}

func (d sshCoreDelegate) CallBrowse(ctx context.Context, workspace protocol.Workspace, action string, params any, out any) error {
	if d.manager == nil {
		return newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return d.manager.CallBrowse(ctx, workspace, action, params, out)
}

func (d sshCoreDelegate) StartExec(ctx context.Context, workspace protocol.Workspace, id string, params map[string]any) (<-chan internalssh.Event, func(), map[string]any, error) {
	if d.manager == nil {
		return nil, func() {}, nil, newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return d.manager.StartExec(ctx, workspace, id, params)
}

func (d sshCoreDelegate) StartPTY(ctx context.Context, workspace protocol.Workspace, id string, params map[string]any) (<-chan internalssh.Event, func(), map[string]any, error) {
	if d.manager == nil {
		return nil, func() {}, nil, newActionError(501, "ssh_unavailable", "ssh manager unavailable")
	}
	return d.manager.StartPTY(ctx, workspace, id, params)
}
