package main

import (
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestRuntimeEventBridgeRoutesActiveSessionEventsAndFallsBackAfterTerminal(t *testing.T) {
	var fallback []AstralEvent
	bridge := newRuntimeEventBridge(func(ev AstralEvent) {
		fallback = append(fallback, ev)
	})

	events := bridge.Open("sess_1")
	bridge.Emit(AstralEvent{SessionID: "sess_1", Kind: "message.assistant"})
	select {
	case event := <-events:
		if event.Event.Kind != "message.assistant" {
			t.Fatalf("event kind = %q, want message.assistant", event.Event.Kind)
		}
	default:
		t.Fatal("active session event was not routed to runtime stream")
	}

	bridge.Emit(AstralEvent{SessionID: "sess_1", Kind: "turn.completed"})
	select {
	case event := <-events:
		if event.Event.Kind != "turn.completed" {
			t.Fatalf("terminal event kind = %q, want turn.completed", event.Event.Kind)
		}
	default:
		t.Fatal("terminal event was not routed to runtime stream")
	}
	if bridge.HasSink("sess_1") {
		t.Fatal("terminal event should remove active runtime sink")
	}

	bridge.Emit(AstralEvent{SessionID: "sess_1", Kind: protocol.AstralEventKind("control.warning")})
	if len(fallback) != 1 || fallback[0].Kind != "control.warning" {
		t.Fatalf("fallback = %#v, want post-terminal warning", fallback)
	}
}
