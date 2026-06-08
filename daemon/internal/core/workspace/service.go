package workspace

import (
	"context"
	"net/http"

	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/pkg/protocol"
)

type Store interface {
	GetWorkspace(id string) (protocol.Workspace, bool)
	CreateWorkspace(protocol.CreateWorkspaceRequest) (protocol.Workspace, error)
	DeleteWorkspace(id string)
}

type SSHService interface {
	Call(ctx context.Context, ws protocol.Workspace, action string, params any, out any) error
}

type RemoteExecRunner func(context.Context, protocol.Workspace, string, string, int) (map[string]any, error)

type StreamWriter interface {
	WriteWorkspaceFile(frameType string, frame *protocol.WorkspaceFileStreamFrame)
}

type Service struct {
	store           Store
	ssh             SSHService
	runRemoteExecAt RemoteExecRunner
	hooks           LifecycleHooks
}

type LifecycleHooks struct {
	Emit                  func(protocol.AstralEvent)
	StopWorkspaceSessions func(workspaceID string, reason string)
	CloseWorkspaceTerms   func(context.Context, string, string)
	DisconnectWorkspace   func(context.Context, protocol.Workspace)
}

func New(store Store, ssh SSHService, runRemoteExecAt RemoteExecRunner, hooks ...LifecycleHooks) *Service {
	service := &Service{store: store, ssh: ssh, runRemoteExecAt: runRemoteExecAt}
	if len(hooks) > 0 {
		service.hooks = hooks[0]
	}
	return service
}

func (s *Service) CreateWorkspace(req protocol.CreateWorkspaceRequest) (protocol.Workspace, error) {
	if s == nil || s.store == nil {
		return protocol.Workspace{}, apperrors.New(http.StatusServiceUnavailable, "workspace_service_unavailable", "workspace service is not initialized")
	}
	ws, err := s.store.CreateWorkspace(req)
	if err != nil {
		return protocol.Workspace{}, err
	}
	s.emit(protocol.AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.created", Normalized: protocol.EventNormalized("workspace.created", ws)})
	return ws, nil
}

func (s *Service) DeleteWorkspace(ctx context.Context, workspaceID string) (protocol.OkResult, error) {
	if s == nil || s.store == nil {
		return protocol.OkResult{}, apperrors.New(http.StatusServiceUnavailable, "workspace_service_unavailable", "workspace service is not initialized")
	}
	ws, ok := s.store.GetWorkspace(workspaceID)
	if !ok {
		return protocol.OkResult{}, apperrors.New(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	if s.hooks.StopWorkspaceSessions != nil {
		s.hooks.StopWorkspaceSessions(ws.ID, "workspace deleted")
	}
	if s.hooks.CloseWorkspaceTerms != nil {
		s.hooks.CloseWorkspaceTerms(ctx, ws.ID, "workspace_deleted")
	}
	if ws.Target == "ssh" && s.hooks.DisconnectWorkspace != nil {
		s.hooks.DisconnectWorkspace(ctx, ws)
	}
	s.store.DeleteWorkspace(workspaceID)
	s.emit(protocol.AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.removed", Normalized: protocol.EventNormalized("workspace.removed", map[string]any{"workspace_id": ws.ID})})
	return protocol.OkResult{OK: true}, nil
}

func (s *Service) emit(event protocol.AstralEvent) {
	if s != nil && s.hooks.Emit != nil {
		s.hooks.Emit(event)
	}
}
