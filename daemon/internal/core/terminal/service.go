package terminal

import (
	"context"

	"github.com/oines/astralops/pkg/protocol"
)

type Delegate interface {
	List(context.Context) ([]protocol.TerminalTab, error)
	Open(context.Context, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error)
	OpenResult(context.Context, string) (protocol.TerminalOpenResult, bool)
	OpenForController(context.Context, string, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error)
	AttachForController(context.Context, string, any, protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error)
	DetachForController(context.Context, string, any, protocol.TerminalDetachParams) (protocol.TerminalAttachResult, error)
	Input(context.Context, protocol.TerminalInputParams) (protocol.TerminalAckResult, error)
	InputForController(context.Context, string, protocol.TerminalInputParams) (protocol.TerminalAckResult, error)
	Resize(context.Context, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error)
	ResizeForController(context.Context, string, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error)
	HeartbeatAckForController(context.Context, string, protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error)
	Close(context.Context, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error)
	CloseForController(context.Context, string, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error)
	OpenLegacyWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error)
	CloseLegacyWorkspace(context.Context, string, string) (protocol.TerminalAckResult, error)
	CloseWorkspace(context.Context, string, string)
	DetachConnection(context.Context, string, string)
}

type Service struct {
	delegate Delegate
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

func (s *Service) List(ctx context.Context) ([]protocol.TerminalTab, error) {
	if s == nil || s.delegate == nil {
		return nil, nil
	}
	return s.delegate.List(ctx)
}

func (s *Service) Open(ctx context.Context, params protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalOpenResult{}, nil
	}
	return s.delegate.Open(ctx, params)
}

func (s *Service) OpenResult(ctx context.Context, terminalID string) (protocol.TerminalOpenResult, bool) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalOpenResult{}, false
	}
	return s.delegate.OpenResult(ctx, terminalID)
}

func (s *Service) OpenForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalOpenResult{}, nil
	}
	return s.delegate.OpenForController(ctx, controllerDeviceID, params)
}

func (s *Service) AttachForController(ctx context.Context, controllerDeviceID string, conn any, params protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAttachResult{}, nil
	}
	return s.delegate.AttachForController(ctx, controllerDeviceID, conn, params)
}

func (s *Service) DetachForController(ctx context.Context, controllerDeviceID string, conn any, params protocol.TerminalDetachParams) (protocol.TerminalAttachResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAttachResult{}, nil
	}
	return s.delegate.DetachForController(ctx, controllerDeviceID, conn, params)
}

func (s *Service) Input(ctx context.Context, params protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.Input(ctx, params)
}

func (s *Service) InputForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.InputForController(ctx, controllerDeviceID, params)
}

func (s *Service) Resize(ctx context.Context, params protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.Resize(ctx, params)
}

func (s *Service) ResizeForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.ResizeForController(ctx, controllerDeviceID, params)
}

func (s *Service) HeartbeatAckForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.HeartbeatAckForController(ctx, controllerDeviceID, params)
}

func (s *Service) Close(ctx context.Context, params protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.Close(ctx, params)
}

func (s *Service) CloseForController(ctx context.Context, controllerDeviceID string, params protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.CloseForController(ctx, controllerDeviceID, params)
}

func (s *Service) OpenLegacyWorkspace(ctx context.Context, params protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalOpenResult{}, nil
	}
	return s.delegate.OpenLegacyWorkspace(ctx, params)
}

func (s *Service) CloseLegacyWorkspace(ctx context.Context, workspaceID string, terminalID string) (protocol.TerminalAckResult, error) {
	if s == nil || s.delegate == nil {
		return protocol.TerminalAckResult{}, nil
	}
	return s.delegate.CloseLegacyWorkspace(ctx, workspaceID, terminalID)
}

func (s *Service) CloseWorkspace(ctx context.Context, workspaceID string, reason string) {
	if s == nil || s.delegate == nil {
		return
	}
	s.delegate.CloseWorkspace(ctx, workspaceID, reason)
}

func (s *Service) DetachConnection(ctx context.Context, connectionID string, reason string) {
	if s == nil || s.delegate == nil {
		return
	}
	s.delegate.DetachConnection(ctx, connectionID, reason)
}
