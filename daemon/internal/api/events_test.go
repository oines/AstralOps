package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oines/astralops/daemon/internal/ports"
	"github.com/oines/astralops/pkg/protocol"
)

func TestEventsHandlerQueriesEventsThroughCommandFacade(t *testing.T) {
	events := &fakeEventCommands{
		queryEvents: []protocol.AstralEvent{{Seq: 2, WorkspaceID: "ws_1", SessionID: "sess_1", Kind: "turn.completed"}},
	}
	handler := NewEventsHandler(events)
	req := httptest.NewRequest(http.MethodGet, "/v1/events?workspace_id=ws_1&session_id=sess_1&after_seq=1&before_seq=5&limit=10", nil)
	rr := httptest.NewRecorder()

	handler.HandleEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if events.queryParams.WorkspaceID != "ws_1" || events.queryParams.SessionID != "sess_1" || events.queryParams.AfterSeq != 1 || events.queryParams.BeforeSeq != 5 || events.queryParams.Limit != 10 {
		t.Fatalf("query params = %#v", events.queryParams)
	}
	var body []protocol.AstralEvent
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0].Seq != 2 {
		t.Fatalf("body = %#v, want one event", body)
	}
}

type fakeEventCommands struct {
	queryParams protocol.EventWindowParams
	queryEvents []protocol.AstralEvent
}

func (f *fakeEventCommands) QueryEvents(_ context.Context, params protocol.EventWindowParams) ([]protocol.AstralEvent, error) {
	f.queryParams = params
	return f.queryEvents, nil
}

func (f *fakeEventCommands) ReplayEvents(context.Context, ports.EventStreamParams) ([]protocol.AstralEvent, error) {
	return nil, nil
}

func (f *fakeEventCommands) Subscribe(context.Context) (ports.EventSubscription, error) {
	return fakeEventSubscription{ch: make(chan protocol.AstralEvent)}, nil
}

type fakeEventSubscription struct {
	ch chan protocol.AstralEvent
}

func (s fakeEventSubscription) Events() <-chan protocol.AstralEvent {
	return s.ch
}

func (s fakeEventSubscription) Close() {
	close(s.ch)
}
