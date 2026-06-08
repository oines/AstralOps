package workspace

import (
	"context"
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestServiceOwnsWorkspaceLifecycle(t *testing.T) {
	store := &fakeLifecycleStore{workspaces: map[string]protocol.Workspace{}}
	var emitted []protocol.AstralEvent
	var stopped []string
	var closed []string
	var disconnected []string
	service := New(store, nil, nil, LifecycleHooks{
		Emit: func(event protocol.AstralEvent) {
			emitted = append(emitted, event)
		},
		StopWorkspaceSessions: func(workspaceID string, reason string) {
			stopped = append(stopped, workspaceID+":"+reason)
		},
		CloseWorkspaceTerms: func(_ context.Context, workspaceID string, reason string) {
			closed = append(closed, workspaceID+":"+reason)
		},
		DisconnectWorkspace: func(_ context.Context, workspace protocol.Workspace) {
			disconnected = append(disconnected, workspace.ID)
		},
	})

	ws, err := service.CreateWorkspace(protocol.CreateWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  protocol.AgentCodex,
		SSH:    &protocol.SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/srv/app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID == "" || len(emitted) != 1 || emitted[0].Kind != "workspace.created" {
		t.Fatalf("created ws=%#v emitted=%#v, want workspace.created", ws, emitted)
	}

	result, err := service.DeleteWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("delete result = %#v, want ok", result)
	}
	if _, ok := store.GetWorkspace(ws.ID); ok {
		t.Fatal("workspace still exists after delete")
	}
	if len(stopped) != 1 || stopped[0] != ws.ID+":workspace deleted" {
		t.Fatalf("stopped = %#v", stopped)
	}
	if len(closed) != 1 || closed[0] != ws.ID+":workspace_deleted" {
		t.Fatalf("closed = %#v", closed)
	}
	if len(disconnected) != 1 || disconnected[0] != ws.ID {
		t.Fatalf("disconnected = %#v", disconnected)
	}
	if len(emitted) != 2 || emitted[1].Kind != "workspace.removed" {
		t.Fatalf("emitted = %#v, want workspace.removed", emitted)
	}
}

type fakeLifecycleStore struct {
	workspaces map[string]protocol.Workspace
}

func (s *fakeLifecycleStore) GetWorkspace(id string) (protocol.Workspace, bool) {
	workspace, ok := s.workspaces[id]
	return workspace, ok
}

func (s *fakeLifecycleStore) CreateWorkspace(req protocol.CreateWorkspaceRequest) (protocol.Workspace, error) {
	workspace := protocol.Workspace{
		ID:       "ws_lifecycle",
		Name:     req.Name,
		Target:   req.Target,
		Agent:    req.Agent,
		LocalCWD: req.LocalCWD,
		SSH:      req.SSH,
	}
	s.workspaces[workspace.ID] = workspace
	return workspace, nil
}

func (s *fakeLifecycleStore) DeleteWorkspace(id string) {
	delete(s.workspaces, id)
}
