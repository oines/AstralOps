package hostcore

import "context"

type PairingStore interface {
	ListPairingRequests(context.Context) (any, error)
	ApprovePairingRequest(context.Context, string) (any, error)
	DenyPairingRequest(context.Context, string) (any, error)
	RevokeTrustedController(context.Context, string, string) (any, error)
}

type WorkbenchService interface {
	Snapshot(context.Context, any) (any, error)
	SubscribeWorkbench(context.Context, any) (WorkbenchSubscription, error)
}

type WorkbenchSubscription struct {
	Patches <-chan WorkbenchPatch
	Close   func()
}

type WorkbenchPatch struct {
	Version int64              `json:"version"`
	Ops     []WorkbenchPatchOp `json:"ops"`
	Meta    map[string]any     `json:"meta,omitempty"`
}

type WorkbenchPatchOp struct {
	Op         string `json:"op"`
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Value      any    `json:"value,omitempty"`
}

type TerminalService interface {
	Open(context.Context, string, any) (any, error)
	List(context.Context) (any, error)
	Attach(context.Context, string, any) (any, error)
	Detach(context.Context, string, any) (any, error)
	Input(context.Context, string, any) (any, error)
	Resize(context.Context, string, any) (any, error)
	Close(context.Context, string, any) (any, error)
}

type EventService interface {
	QueryEvents(context.Context, any) (any, error)
	SubscribeEvents(context.Context, any) (EventSubscription, error)
}

type EventSubscription struct {
	Events <-chan any
	Close  func()
}

type ResourceService interface {
	ReadMedia(context.Context, any) (any, error)
	StreamMedia(context.Context, any) (any, error)
	BrowseFileSystem(context.Context, any) (any, error)
	ReadWorkspaceFiles(context.Context, any) (any, error)
	WriteWorkspaceFiles(context.Context, any) (any, error)
	ExecWorkspaceCommand(context.Context, any) (any, error)
}
