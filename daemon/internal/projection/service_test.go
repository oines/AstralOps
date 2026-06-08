package projection

import (
	"testing"

	"github.com/oines/astralops/pkg/protocol"
)

func TestReplayMergesCurrentContextWithAggregateMetadata(t *testing.T) {
	service := New(Options{})
	service.Replay([]protocol.AstralEvent{
		event(2, "sess", "control.context", map[string]any{
			"source":                  "claude",
			"scope":                   "aggregate",
			"total_tokens":            658200,
			"cumulative_total_tokens": 658200,
			"model_context_window":    200000,
		}),
		event(1, "sess", "control.context", map[string]any{
			"source":       "claude",
			"scope":        "current",
			"total_tokens": 30000,
		}),
		event(2, "sess", "control.context", map[string]any{
			"source":                  "claude",
			"scope":                   "aggregate",
			"total_tokens":            658200,
			"cumulative_total_tokens": 658200,
			"model_context_window":    200000,
		}),
	})
	context := service.LatestContext("sess")
	if got := stringValue(context["scope"]); got != "current" {
		t.Fatalf("scope = %q, want current", got)
	}
	if got := numberValue(context["total_tokens"]); got != 30000 {
		t.Fatalf("total_tokens = %v, want 30000", got)
	}
	if got := numberValue(context["cumulative_total_tokens"]); got != 658200 {
		t.Fatalf("cumulative_total_tokens = %v, want 658200", got)
	}
	if got := numberValue(context["used_percent"]); got != 15 {
		t.Fatalf("used_percent = %v, want 15", got)
	}
}

func TestReplayKeepsCompactedContextInvalidUntilCurrentUsage(t *testing.T) {
	service := New(Options{})
	service.Replay([]protocol.AstralEvent{
		event(1, "sess", "control.context", map[string]any{
			"source":               "claude",
			"scope":                "current",
			"total_tokens":         30000,
			"model_context_window": 200000,
		}),
		event(2, "sess", "memory.compacted", map[string]any{"source": "claude"}),
		event(3, "sess", "control.context", map[string]any{
			"source":               "astralops",
			"total_tokens":         30000,
			"model_context_window": 200000,
		}),
		event(4, "sess", "control.context", map[string]any{
			"source":                  "claude",
			"scope":                   "aggregate",
			"total_tokens":            658200,
			"cumulative_total_tokens": 658200,
			"model_context_window":    200000,
		}),
	})
	if context := service.LatestContext("sess"); len(context) > 0 {
		t.Fatalf("context = %#v, want compacted session to ignore aggregate-only usage", context)
	}
	service.Apply(event(5, "sess", "control.context", map[string]any{
		"source":       "claude",
		"scope":        "current",
		"total_tokens": 12000,
	}))
	context := service.LatestContext("sess")
	if got := numberValue(context["total_tokens"]); got != 12000 {
		t.Fatalf("total_tokens = %v, want 12000", got)
	}
}

func TestSessionViewCacheInvalidatesOnCommittedEvent(t *testing.T) {
	service := New(Options{})
	builds := 0
	builder := func() (protocol.SessionView, bool) {
		builds++
		return protocol.SessionView{Title: "view"}, true
	}
	if _, ok := service.SessionView("sess", "key", builder); !ok {
		t.Fatal("SessionView = false, want true")
	}
	if _, ok := service.SessionView("sess", "key", builder); !ok {
		t.Fatal("SessionView cached = false, want true")
	}
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}
	service.Apply(event(1, "sess", "message.user", map[string]any{"text": "hi"}))
	if _, ok := service.SessionView("sess", "key", builder); !ok {
		t.Fatal("SessionView after invalidation = false, want true")
	}
	if builds != 2 {
		t.Fatalf("builds after invalidation = %d, want 2", builds)
	}
}

func TestClaudeSlashCommandsAreProjectedThroughInjectedParser(t *testing.T) {
	service := New(Options{ClaudeSlashCommands: func(ev protocol.AstralEvent) []string {
		if ev.Kind == "session.native" {
			return []string{"compact", "clear"}
		}
		return nil
	}})
	service.Apply(event(1, "sess", "session.native", map[string]any{"source": "claude"}))
	commands := service.ClaudeSlashCommands("sess")
	if len(commands) != 2 || commands[0] != "compact" || commands[1] != "clear" {
		t.Fatalf("commands = %#v, want compact/clear", commands)
	}
}

func TestPendingInteractionProjectsLatestUnresolvedRequest(t *testing.T) {
	pending := PendingInteraction([]protocol.AstralEvent{
		event(1, "sess", "approval.requested", map[string]any{
			"approval_id": "approval_old",
			"kind":        "command",
			"command":     "go test ./...",
		}),
		event(2, "sess", "approval.responded", map[string]any{"approval_id": "approval_old"}),
		event(3, "sess", "ask.requested", map[string]any{
			"ask_id": "ask_live",
			"kind":   "AskUserQuestion",
			"params": map[string]any{"question": "Need input?"},
		}),
	})
	if pending == nil || pending.ID != "ask_live" || pending.Kind != "ask" || pending.Title != "Need input?" {
		t.Fatalf("pending = %#v, want live ask", pending)
	}
}

func TestQueuedInputsProjectsOnlyVisiblePendingInputs(t *testing.T) {
	inputs := QueuedInputs([]protocol.AstralEvent{
		event(1, "sess", "queue.queued", map[string]any{"queue_id": "q1", "text": "first"}),
		event(2, "sess", "queue.queued", map[string]any{"queue_id": "q2", "text": "internal", "internal": true}),
		event(3, "sess", "queue.queued", map[string]any{"queue_id": "q3", "text": "third"}),
		event(4, "sess", "queue.cancelled", map[string]any{"queue_id": "q1"}),
	})
	if len(inputs) != 1 || inputs[0].ID != "q3" || inputs[0].Text != "third" {
		t.Fatalf("inputs = %#v, want only q3", inputs)
	}
}

func TestEditableUserMessageIgnoresReplacedTranscript(t *testing.T) {
	editable := EditableUserMessage(protocol.Session{ID: "sess", Agent: protocol.AgentCodex}, []protocol.AstralEvent{
		event(1, "sess", "message.user", map[string]any{"text": "old"}),
		event(2, "sess", "turn.replaced", map[string]any{"start_seq": 1, "end_seq": 1}),
		event(3, "sess", "message.user", map[string]any{"text": "new"}),
	}, "idle")
	if editable == nil || editable.EventSeq != 3 || editable.Text != "new" {
		t.Fatalf("editable = %#v, want latest unreplaced message", editable)
	}
}

func event(seq int64, sessionID string, kind protocol.AstralEventKind, normalized map[string]any) protocol.AstralEvent {
	return protocol.AstralEvent{
		Seq:        seq,
		SessionID:  sessionID,
		Kind:       kind,
		Normalized: protocol.EventNormalized(kind, normalized),
	}
}
