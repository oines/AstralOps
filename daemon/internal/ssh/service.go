package ssh

import (
	"context"

	"github.com/oines/astralops/pkg/protocol"
)

type Delegate interface {
	RestorePersistedConnections(context.Context)
	Connection(context.Context, protocol.Workspace) protocol.WorkspaceConnection
	Connect(context.Context, protocol.Workspace) (protocol.WorkspaceConnection, error)
	Disconnect(context.Context, protocol.Workspace) protocol.WorkspaceConnection
	RemoteWorkspaceRuntimeDir(protocol.Workspace) string
	ProxyFor(context.Context, protocol.Workspace) (protocol.WorkspaceConnection, error)
	Call(context.Context, protocol.Workspace, string, any, any) error
	CallBrowse(context.Context, protocol.Workspace, string, any, any) error
	StartExec(context.Context, protocol.Workspace, string, map[string]any) (<-chan Event, func(), map[string]any, error)
	StartPTY(context.Context, protocol.Workspace, string, map[string]any) (<-chan Event, func(), map[string]any, error)
}

type Service struct {
	delegate Delegate
}

type Event struct {
	ID     string
	Event  string
	Result map[string]any
}

func New(delegate Delegate) *Service {
	return &Service{delegate: delegate}
}

func (s *Service) Delegate() Delegate {
	if s == nil {
		return nil
	}
	return s.delegate
}

func (s *Service) Call(ctx context.Context, workspace protocol.Workspace, action string, params any, out any) error {
	if s == nil || s.delegate == nil {
		return nil
	}
	return s.delegate.Call(ctx, workspace, action, params, out)
}

func (s *Service) RestorePersistedConnections(ctx context.Context) {
	if s == nil || s.delegate == nil {
		return
	}
	s.delegate.RestorePersistedConnections(ctx)
}

func (s *Service) Connection(ctx context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	if s == nil || s.delegate == nil {
		return protocol.WorkspaceConnection{}
	}
	return s.delegate.Connection(ctx, workspace)
}

func (s *Service) Connect(ctx context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	if s == nil || s.delegate == nil {
		return protocol.WorkspaceConnection{}, nil
	}
	return s.delegate.Connect(ctx, workspace)
}

func (s *Service) Disconnect(ctx context.Context, workspace protocol.Workspace) protocol.WorkspaceConnection {
	if s == nil || s.delegate == nil {
		return protocol.WorkspaceConnection{}
	}
	return s.delegate.Disconnect(ctx, workspace)
}

func (s *Service) RemoteWorkspaceRuntimeDir(workspace protocol.Workspace) string {
	if s == nil || s.delegate == nil {
		return ""
	}
	return s.delegate.RemoteWorkspaceRuntimeDir(workspace)
}

func (s *Service) ProxyFor(ctx context.Context, workspace protocol.Workspace) (protocol.WorkspaceConnection, error) {
	if s == nil || s.delegate == nil {
		return protocol.WorkspaceConnection{}, nil
	}
	return s.delegate.ProxyFor(ctx, workspace)
}

func (s *Service) CallBrowse(ctx context.Context, workspace protocol.Workspace, action string, params any, out any) error {
	if s == nil || s.delegate == nil {
		return nil
	}
	return s.delegate.CallBrowse(ctx, workspace, action, params, out)
}

func (s *Service) StartExec(ctx context.Context, workspace protocol.Workspace, id string, params map[string]any) (<-chan Event, func(), map[string]any, error) {
	if s == nil || s.delegate == nil {
		return nil, func() {}, nil, nil
	}
	return s.delegate.StartExec(ctx, workspace, id, params)
}

func (s *Service) StartPTY(ctx context.Context, workspace protocol.Workspace, id string, params map[string]any) (<-chan Event, func(), map[string]any, error) {
	if s == nil || s.delegate == nil {
		return nil, func() {}, nil, nil
	}
	return s.delegate.StartPTY(ctx, workspace, id, params)
}
