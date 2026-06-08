package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

func TestSessionsHandlerStartInputUsesCommandFacade(t *testing.T) {
	sessions := &fakeSessionCommands{
		startInputResult: protocol.SessionInputResult{OK: true, Mode: "queue", Queued: true, QueueID: "queue_1"},
	}
	handler := NewSessionsHandler(sessions, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/input", strings.NewReader(`{"input":"hello","model":"gpt-test","reasoning_effort":"low","permission_mode":"auto","attachments":[{"id":"att_1","path":"/tmp/a.png","name":"a.png","kind":"image"}]}`))
	rr := httptest.NewRecorder()

	handler.HandleSessionAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if sessions.startInput.SessionID != "sess_1" || sessions.startInput.Input != "hello" || sessions.startInput.Model != "gpt-test" {
		t.Fatalf("start input params = %#v", sessions.startInput)
	}
	if len(sessions.startInput.Attachments) != 1 || sessions.startInput.Attachments[0].ID != "att_1" {
		t.Fatalf("attachments = %#v, want one attachment", sessions.startInput.Attachments)
	}
	var body protocol.SessionInputResult
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Mode != "queue" || body.QueueID != "queue_1" {
		t.Fatalf("body = %#v, want queued result", body)
	}
}

func TestSessionsHandlerDeletePreservesLegacyOKShape(t *testing.T) {
	handler := NewSessionsHandler(&fakeSessionCommands{
		deleteResult: protocol.SessionDeleteResult{OK: true, SessionID: "sess_1"},
	}, nil)
	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess_1", nil)
	rr := httptest.NewRecorder()

	handler.HandleSessionAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["session_id"] != nil {
		t.Fatalf("body = %#v, want legacy ok-only delete response", body)
	}
}

func TestSessionsHandlerWritesTypedCommandErrors(t *testing.T) {
	handler := NewSessionsHandler(&fakeSessionCommands{
		viewErr: typedTestError{status: http.StatusNotFound, code: "session_not_found", message: "session not found"},
	}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/view", nil)
	rr := httptest.NewRecorder()

	handler.HandleSessionAction(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "session_not_found" || body["message"] != "session not found" || body["error"] != "session not found" {
		t.Fatalf("body = %#v, want typed error envelope", body)
	}
}

func TestSessionsHandlerServesSessionMediaThroughMediaCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.png")
	if err := os.WriteFile(path, []byte("png-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	media := &fakeMediaCommands{
		sessionMedia: ports.SessionMedia{Path: path, Name: "clip.png", MIMEType: "image/png"},
	}
	handler := NewSessionsHandler(&fakeSessionCommands{}, media)
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_1/media/12/media_1?download=1", nil)
	rr := httptest.NewRecorder()

	handler.HandleSessionAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if media.sessionMediaParams.SessionID != "sess_1" || media.sessionMediaParams.EventSeq != 12 || media.sessionMediaParams.MediaID != "media_1" {
		t.Fatalf("media params = %#v", media.sessionMediaParams)
	}
	if rr.Body.String() != "png-data" {
		t.Fatalf("body = %q, want fixture media", rr.Body.String())
	}
	if contentType := rr.Header().Get("Content-Type"); !strings.Contains(contentType, "image/png") {
		t.Fatalf("content-type = %q, want image/png", contentType)
	}
	if disposition := rr.Header().Get("Content-Disposition"); !strings.Contains(disposition, "clip.png") {
		t.Fatalf("content-disposition = %q, want filename", disposition)
	}
}

type typedTestError struct {
	status  int
	code    protocol.ControlErrorCode
	message string
}

func (e typedTestError) Error() string {
	return e.message
}

func (e typedTestError) ControlError() *protocol.ControlError {
	return &protocol.ControlError{Status: e.status, Code: e.code, Message: e.message}
}

type fakeSessionCommands struct {
	startInput       ports.SessionInputParams
	startInputResult protocol.SessionInputResult
	viewErr          error
	deleteResult     protocol.SessionDeleteResult
}

func (f *fakeSessionCommands) CreateSession(context.Context, protocol.CreateSessionRequest) (protocol.Session, error) {
	return protocol.Session{ID: "sess_1"}, nil
}

func (f *fakeSessionCommands) ReadSessions(context.Context, protocol.SessionsReadParams) ([]protocol.Session, error) {
	return []protocol.Session{{ID: "sess_1"}}, nil
}

func (f *fakeSessionCommands) ReadSessionView(context.Context, protocol.SessionReferenceParams) (protocol.SessionView, error) {
	if f.viewErr != nil {
		return protocol.SessionView{}, f.viewErr
	}
	return protocol.SessionView{Session: protocol.Session{ID: "sess_1"}}, nil
}

func (f *fakeSessionCommands) StartInput(_ context.Context, params ports.SessionInputParams) (protocol.SessionInputResult, error) {
	f.startInput = params
	return f.startInputResult, nil
}

func (f *fakeSessionCommands) CancelQueue(context.Context, protocol.QueueControlParams) (protocol.QueueControlResult, error) {
	return protocol.QueueControlResult{OK: true}, nil
}

func (f *fakeSessionCommands) SteerQueue(context.Context, protocol.QueueControlParams) (protocol.QueueControlResult, error) {
	return protocol.QueueControlResult{OK: true}, nil
}

func (f *fakeSessionCommands) CancelTurn(context.Context, protocol.SessionReferenceParams) (protocol.OkResult, error) {
	return protocol.OkResult{OK: true}, nil
}

func (f *fakeSessionCommands) RespondInteraction(context.Context, protocol.InteractionRespondParams) (protocol.OkResult, error) {
	return protocol.OkResult{OK: true}, nil
}

func (f *fakeSessionCommands) ForkSession(context.Context, protocol.SessionForkControlParams) (protocol.ForkSessionResponse, error) {
	return protocol.ForkSessionResponse{Session: protocol.Session{ID: "fork_1"}}, nil
}

func (f *fakeSessionCommands) EditLastUserMessage(context.Context, protocol.SessionEditParams) (protocol.OkResult, error) {
	return protocol.OkResult{OK: true}, nil
}

func (f *fakeSessionCommands) DeleteSession(context.Context, protocol.SessionDeleteParams) (protocol.SessionDeleteResult, error) {
	return f.deleteResult, nil
}

func (f *fakeSessionCommands) ListCommands(context.Context, protocol.SessionReferenceParams) (protocol.SessionCommandListResponse, error) {
	return protocol.SessionCommandListResponse{Commands: []protocol.SessionCommand{{ID: "status", Enabled: true}}}, nil
}

func (f *fakeSessionCommands) RunCommand(context.Context, ports.SessionCommandRunParams) (protocol.SessionCommandResponse, error) {
	return protocol.SessionCommandResponse{OK: true}, nil
}

type fakeMediaCommands struct {
	sessionMedia       ports.SessionMedia
	sessionMediaParams ports.SessionMediaParams
}

func (f *fakeMediaCommands) IngestAttachment(context.Context, protocol.AttachmentIngestParams) (protocol.AttachmentIngestResult, error) {
	return protocol.AttachmentIngestResult{}, nil
}

func (f *fakeMediaCommands) StartAttachmentIngest(context.Context, protocol.AttachmentIngestStartParams) (protocol.AttachmentIngestStartResult, error) {
	return protocol.AttachmentIngestStartResult{}, nil
}

func (f *fakeMediaCommands) AppendAttachmentChunk(context.Context, protocol.AttachmentIngestChunkParams) (protocol.AttachmentIngestChunkResult, error) {
	return protocol.AttachmentIngestChunkResult{}, nil
}

func (f *fakeMediaCommands) FinishAttachmentIngest(context.Context, protocol.AttachmentIngestFinishParams) (protocol.AttachmentIngestFinishResult, error) {
	return protocol.AttachmentIngestFinishResult{}, nil
}

func (f *fakeMediaCommands) ReadMedia(context.Context, protocol.MediaReadParams) (protocol.MediaReadResult, error) {
	return protocol.MediaReadResult{}, nil
}

func (f *fakeMediaCommands) ResolveSessionMedia(_ context.Context, params ports.SessionMediaParams) (ports.SessionMedia, error) {
	f.sessionMediaParams = params
	return f.sessionMedia, nil
}

func (f *fakeMediaCommands) StreamMedia(context.Context, protocol.MediaStreamParams) (protocol.MediaStreamResult, error) {
	return protocol.MediaStreamResult{}, nil
}

func (f *fakeMediaCommands) CancelMediaStream(context.Context, protocol.MediaStreamCancelParams) (protocol.MediaStreamCancelResult, error) {
	return protocol.MediaStreamCancelResult{}, nil
}
