package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

func TestWorkspacesHandlerRoutesThroughWorkspaceCommands(t *testing.T) {
	workspaces := &fakeWorkspaceCommands{
		workspaces: []protocol.Workspace{{ID: "ws_1", Name: "Local", Target: "local"}},
	}
	handler := NewWorkspacesHandler(workspaces, &fakeTerminalCommands{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil)
	rr := httptest.NewRecorder()

	handler.HandleWorkspaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !workspaces.readWorkspacesCalled {
		t.Fatal("ReadWorkspaces was not called")
	}
	var body []protocol.Workspace
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0].ID != "ws_1" {
		t.Fatalf("body = %#v, want workspace list", body)
	}
}

func TestWorkspacesHandlerCreatePreservesCreatedStatus(t *testing.T) {
	workspaces := &fakeWorkspaceCommands{
		createResult: protocol.Workspace{ID: "ws_new", Name: "New", Target: "local"},
	}
	handler := NewWorkspacesHandler(workspaces, &fakeTerminalCommands{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(`{"name":"New","target":"local","agent":"codex","local_cwd":"/tmp/project"}`))
	rr := httptest.NewRecorder()

	handler.HandleWorkspaces(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	if workspaces.createReq.Name != "New" || workspaces.createReq.Target != "local" || workspaces.createReq.LocalCWD != "/tmp/project" {
		t.Fatalf("create request = %#v", workspaces.createReq)
	}
}

func TestWorkspacesHandlerPreservesLegacyFilesShape(t *testing.T) {
	workspaces := &fakeWorkspaceCommands{
		legacyFiles: ports.LegacyWorkspaceFilesResult{
			Path: "src",
			Root: "/tmp/project",
			Entries: []ports.LegacyWorkspaceFileEntry{{
				Name: "main.go",
				Path: "src/main.go",
				Kind: "file",
				Size: 12,
			}},
		},
	}
	handler := NewWorkspacesHandler(workspaces, &fakeTerminalCommands{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/files?path=src", nil)
	rr := httptest.NewRecorder()

	handler.HandleWorkspaceAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if workspaces.legacyFilesParams.WorkspaceID != "ws_1" || workspaces.legacyFilesParams.Path != "src" {
		t.Fatalf("files params = %#v", workspaces.legacyFilesParams)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["path"] != "src" || body["root"] != "/tmp/project" {
		t.Fatalf("body = %#v, want legacy path/root shape", body)
	}
	entries, ok := body["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v, want one legacy entry", body["entries"])
	}
}

func TestWorkspacesHandlerConnectErrorPreservesLegacyConnectionShape(t *testing.T) {
	workspaces := &fakeWorkspaceCommands{
		connectResult: protocol.WorkspaceConnection{WorkspaceID: "ws_1", Target: "ssh", Status: "failed"},
		connectErr:    errors.New("dial failed"),
	}
	handler := NewWorkspacesHandler(workspaces, &fakeTerminalCommands{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/connect", nil)
	rr := httptest.NewRecorder()

	handler.HandleWorkspaceAction(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "dial failed" {
		t.Fatalf("body = %#v, want legacy error string", body)
	}
	connection := body["connection"].(map[string]any)
	if connection["status"] != "failed" {
		t.Fatalf("connection = %#v, want failed state", connection)
	}
}

func TestWorkspacesHandlerTerminalRoutesThroughTerminalCommands(t *testing.T) {
	terminals := &fakeTerminalCommands{
		openResult:  protocol.TerminalOpenResult{TerminalID: "term_1", WorkspaceID: "ws_1", Status: "open"},
		closeResult: protocol.TerminalAckResult{TerminalID: "term_1", Status: "closed"},
	}
	handler := NewWorkspacesHandler(&fakeWorkspaceCommands{}, terminals, nil)

	openReq := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/terminal", nil)
	openRR := httptest.NewRecorder()
	handler.HandleWorkspaceAction(openRR, openReq)

	if openRR.Code != http.StatusCreated {
		t.Fatalf("open status = %d, want 201: %s", openRR.Code, openRR.Body.String())
	}
	if terminals.openLegacy.WorkspaceID != "ws_1" {
		t.Fatalf("open params = %#v", terminals.openLegacy)
	}

	closeReq := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_1/terminals/term_1", nil)
	closeRR := httptest.NewRecorder()
	handler.HandleWorkspaceAction(closeRR, closeReq)

	if closeRR.Code != http.StatusOK {
		t.Fatalf("close status = %d, want 200: %s", closeRR.Code, closeRR.Body.String())
	}
	if terminals.closeLegacy.WorkspaceID != "ws_1" || terminals.closeLegacy.TerminalID != "term_1" {
		t.Fatalf("close params = %#v", terminals.closeLegacy)
	}
}

func TestWorkspacesHandlerHostFileSystemBrowseUsesCommandFacade(t *testing.T) {
	workspaces := &fakeWorkspaceCommands{
		browseResult: protocol.HostFileSystemBrowseResult{
			Target:    "local",
			Platform:  "darwin",
			Separator: "/",
			Path:      "/tmp",
		},
	}
	handler := NewWorkspacesHandler(workspaces, &fakeTerminalCommands{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/fs/browse", strings.NewReader(`{"target":"local","path":"/tmp"}`))
	rr := httptest.NewRecorder()

	handler.HandleHostFileSystemBrowse(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if workspaces.browseParams.Target != "local" || workspaces.browseParams.Path != "/tmp" {
		t.Fatalf("browse params = %#v", workspaces.browseParams)
	}
}

type fakeWorkspaceCommands struct {
	readWorkspacesCalled bool
	workspaces           []protocol.Workspace
	createReq            protocol.CreateWorkspaceRequest
	createResult         protocol.Workspace
	legacyFilesParams    ports.LegacyWorkspaceFilesParams
	legacyFiles          ports.LegacyWorkspaceFilesResult
	connectResult        protocol.WorkspaceConnection
	connectErr           error
	browseParams         protocol.HostFileSystemBrowseParams
	browseResult         protocol.HostFileSystemBrowseResult
}

func (f *fakeWorkspaceCommands) CreateWorkspace(_ context.Context, req protocol.CreateWorkspaceRequest) (protocol.Workspace, error) {
	f.createReq = req
	return f.createResult, nil
}

func (f *fakeWorkspaceCommands) ReadWorkspaces(context.Context) ([]protocol.Workspace, error) {
	f.readWorkspacesCalled = true
	return f.workspaces, nil
}

func (f *fakeWorkspaceCommands) ReadWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.Workspace, error) {
	return protocol.Workspace{ID: "ws_1"}, nil
}

func (f *fakeWorkspaceCommands) DeleteWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.OkResult, error) {
	return protocol.OkResult{OK: true}, nil
}

func (f *fakeWorkspaceCommands) ListNativeSessions(context.Context, protocol.NativeSessionsReadParams) (protocol.NativeSessionListResponse, error) {
	return protocol.NativeSessionListResponse{}, nil
}

func (f *fakeWorkspaceCommands) ImportNativeSession(context.Context, protocol.NativeSessionImportParams) (protocol.NativeSessionImportResponse, error) {
	return protocol.NativeSessionImportResponse{}, nil
}

func (f *fakeWorkspaceCommands) BrowseHostFileSystem(_ context.Context, params protocol.HostFileSystemBrowseParams) (protocol.HostFileSystemBrowseResult, error) {
	f.browseParams = params
	return f.browseResult, nil
}

func (f *fakeWorkspaceCommands) ReadLegacyWorkspaceFiles(_ context.Context, params ports.LegacyWorkspaceFilesParams) (ports.LegacyWorkspaceFilesResult, error) {
	f.legacyFilesParams = params
	return f.legacyFiles, nil
}

func (f *fakeWorkspaceCommands) ExecLegacyWorkspaceCommand(context.Context, ports.LegacyWorkspaceExecParams) (map[string]any, error) {
	return map[string]any{"exit_code": 0}, nil
}

func (f *fakeWorkspaceCommands) ReadWorkspaceConnection(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	return protocol.WorkspaceConnection{WorkspaceID: "ws_1", Status: "connected"}, nil
}

func (f *fakeWorkspaceCommands) ConnectWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	return f.connectResult, f.connectErr
}

func (f *fakeWorkspaceCommands) DisconnectWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error) {
	return protocol.WorkspaceConnection{WorkspaceID: "ws_1", Status: "disconnected"}, nil
}

func (f *fakeWorkspaceCommands) ReadFiles(context.Context, protocol.WorkspaceFilesReadParams) (protocol.WorkspaceFilesReadResult, error) {
	return protocol.WorkspaceFilesReadResult{}, nil
}

func (f *fakeWorkspaceCommands) WriteFiles(context.Context, protocol.WorkspaceFilesWriteParams) (protocol.WorkspaceFilesWriteResult, error) {
	return protocol.WorkspaceFilesWriteResult{}, nil
}

func (f *fakeWorkspaceCommands) ApplyPatch(context.Context, protocol.WorkspaceFilesApplyPatchParams) (protocol.WorkspaceFilesApplyPatchResult, error) {
	return protocol.WorkspaceFilesApplyPatchResult{}, nil
}

func (f *fakeWorkspaceCommands) DeleteFiles(context.Context, protocol.WorkspaceFilesDeleteParams) (protocol.WorkspaceFilesDeleteResult, error) {
	return protocol.WorkspaceFilesDeleteResult{}, nil
}

func (f *fakeWorkspaceCommands) MoveFiles(context.Context, protocol.WorkspaceFilesMoveParams) (protocol.WorkspaceFilesMoveResult, error) {
	return protocol.WorkspaceFilesMoveResult{}, nil
}

func (f *fakeWorkspaceCommands) StreamFile(context.Context, protocol.WorkspaceFilesStreamParams) (protocol.WorkspaceFileStreamResult, error) {
	return protocol.WorkspaceFileStreamResult{}, nil
}

func (f *fakeWorkspaceCommands) CancelFileStream(context.Context, protocol.WorkspaceFileStreamCancelParams) (protocol.WorkspaceFileStreamCancelResult, error) {
	return protocol.WorkspaceFileStreamCancelResult{}, nil
}

func (f *fakeWorkspaceCommands) Exec(context.Context, protocol.WorkspaceExecParams) (protocol.WorkspaceExecResult, error) {
	return protocol.WorkspaceExecResult{}, nil
}

type fakeTerminalCommands struct {
	openLegacy  protocol.WorkspaceReferenceParams
	openResult  protocol.TerminalOpenResult
	closeLegacy ports.LegacyWorkspaceTerminalCloseParams
	closeResult protocol.TerminalAckResult
}

func (f *fakeTerminalCommands) ListTerminals(context.Context) ([]protocol.TerminalTab, error) {
	return nil, nil
}

func (f *fakeTerminalCommands) OpenTerminal(context.Context, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	return protocol.TerminalOpenResult{}, nil
}

func (f *fakeTerminalCommands) AttachTerminal(context.Context, protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error) {
	return protocol.TerminalAttachResult{}, nil
}

func (f *fakeTerminalCommands) DetachTerminal(context.Context, protocol.TerminalDetachParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, nil
}

func (f *fakeTerminalCommands) InputTerminal(context.Context, protocol.TerminalInputParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, nil
}

func (f *fakeTerminalCommands) ResizeTerminal(context.Context, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, nil
}

func (f *fakeTerminalCommands) CloseTerminal(context.Context, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, nil
}

func (f *fakeTerminalCommands) AckTerminalHeartbeat(context.Context, protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error) {
	return protocol.TerminalAckResult{}, nil
}

func (f *fakeTerminalCommands) OpenLegacyWorkspaceTerminal(_ context.Context, params protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error) {
	f.openLegacy = params
	return f.openResult, nil
}

func (f *fakeTerminalCommands) CloseLegacyWorkspaceTerminal(_ context.Context, params ports.LegacyWorkspaceTerminalCloseParams) (protocol.TerminalAckResult, error) {
	f.closeLegacy = params
	return f.closeResult, nil
}
