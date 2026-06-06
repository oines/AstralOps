package eventlog

import (
	"context"
	"errors"
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestPublishAppendsProjectsBroadcastsAndBuildsNotification(t *testing.T) {
	store := &fakeStore{}
	projections := &fakeProjectionSink{}
	broadcaster := &fakeBroadcaster{}
	notifications := fakeNotificationPolicy{build: func(source protocol.AstralEvent, title string, sessionID string, events []protocol.AstralEvent) (protocol.AstralEvent, bool) {
		if title != "Session title" || sessionID != source.SessionID {
			t.Fatalf("notification target = %q/%q", title, sessionID)
		}
		if len(events) != 1 || events[0].Kind != "turn.completed" {
			t.Fatalf("notification events = %#v", events)
		}
		return protocol.AstralEvent{
			WorkspaceID: source.WorkspaceID,
			SessionID:   sessionID,
			Agent:       source.Agent,
			Kind:        "control.notification",
			Normalized: protocol.EventNormalized("control.notification",
				map[string]any{"reason": "turn_completed"}),
		}, true
	}}
	service := New(Options{
		Store:         store,
		Projections:   projections,
		Broadcaster:   broadcaster,
		Notifications: notifications,
	})

	saved, err := service.Publish(context.Background(), protocol.AstralEvent{
		WorkspaceID: "ws",
		SessionID:   "sess",
		Agent:       protocol.AgentCodex,
		Kind:        "turn.completed",
		Normalized: protocol.EventNormalized("turn.completed",
			map[string]any{"ok": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Seq != 1 {
		t.Fatalf("saved seq = %d, want 1", saved.Seq)
	}
	if len(store.events) != 2 || store.events[1].Kind != "control.notification" {
		t.Fatalf("stored events = %#v", store.events)
	}
	if len(projections.events) != 2 || len(broadcaster.events) != 2 {
		t.Fatalf("projection/broadcast counts = %d/%d, want 2/2", len(projections.events), len(broadcaster.events))
	}
}

func TestPublishReturnsAppendError(t *testing.T) {
	wantErr := errors.New("append failed")
	service := New(Options{Store: &fakeStore{err: wantErr}})
	if _, err := service.Publish(context.Background(), protocol.AstralEvent{Kind: "turn.completed"}); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

type fakeStore struct {
	events []protocol.AstralEvent
	err    error
}

func (s *fakeStore) AppendEvent(event protocol.AstralEvent) (protocol.AstralEvent, error) {
	if s.err != nil {
		return protocol.AstralEvent{}, s.err
	}
	event.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}

type fakeProjectionSink struct {
	events []protocol.AstralEvent
}

func (s *fakeProjectionSink) Apply(event protocol.AstralEvent) {
	s.events = append(s.events, event)
}

type fakeBroadcaster struct {
	events []protocol.AstralEvent
}

func (b *fakeBroadcaster) Broadcast(event protocol.AstralEvent) {
	b.events = append(b.events, event)
}

type fakeNotificationPolicy struct {
	build func(protocol.AstralEvent, string, string, []protocol.AstralEvent) (protocol.AstralEvent, bool)
}

func (p fakeNotificationPolicy) Target(event protocol.AstralEvent) (string, string) {
	return "Session title", event.SessionID
}

func (p fakeNotificationPolicy) Build(source protocol.AstralEvent, title string, sessionID string, events []protocol.AstralEvent) (protocol.AstralEvent, bool) {
	return p.build(source, title, sessionID, events)
}
