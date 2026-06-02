package workspace

import (
	"context"

	"github.com/oines/astralops/pkg/protocol"
)

type Store interface {
	GetWorkspace(id string) (protocol.Workspace, bool)
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
}

func New(store Store, ssh SSHService, runRemoteExecAt RemoteExecRunner) *Service {
	return &Service{store: store, ssh: ssh, runRemoteExecAt: runRemoteExecAt}
}
