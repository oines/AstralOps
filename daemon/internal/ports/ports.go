package ports

import (
	"context"

	"github.com/oines/astralops/pkg/protocol"
)

type EventPublisher interface {
	Publish(context.Context, protocol.AstralEvent) (protocol.AstralEvent, error)
}

type WorkspaceStore interface {
	GetWorkspace(context.Context, string) (protocol.Workspace, bool, error)
	ListWorkspaces(context.Context) ([]protocol.Workspace, error)
}

type SessionStore interface {
	GetSession(context.Context, string) (protocol.Session, bool, error)
	ListSessions(context.Context, string) ([]protocol.Session, error)
	CreateSession(context.Context, protocol.Workspace, protocol.AgentKind) (protocol.Session, error)
	DeleteSession(context.Context, string) error
	UpdateSessionStatus(context.Context, string, string) error
}

type EventStore interface {
	AppendEvent(context.Context, protocol.AstralEvent) (protocol.AstralEvent, error)
	QueryEvents(context.Context, string, string, int64) ([]protocol.AstralEvent, error)
	QueryEventsWindow(context.Context, string, string, int64, int64, int) ([]protocol.AstralEvent, error)
}

type RuntimeRegistry interface {
	Runtime(protocol.AgentKind) (any, bool)
}

type SSHService interface {
	ConnectionState(context.Context, protocol.Workspace) (any, error)
}

type ControlDispatcher interface {
	Dispatch(context.Context, protocol.ControlRequest) (protocol.ControlResponse, error)
}

type AttachmentStore interface {
	LoadAttachment(context.Context, string, string) (protocol.InputAttachment, error)
}

type MediaResolver interface {
	ResolveMedia(context.Context, protocol.MediaReadParams) (any, error)
}
