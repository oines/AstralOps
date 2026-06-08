package sessions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oines/astralops/daemon/internal/agents"
	"github.com/oines/astralops/daemon/internal/apperrors"
	"github.com/oines/astralops/daemon/internal/sessiontypes"
	"github.com/oines/astralops/pkg/protocol"
)

func TestServiceStartSessionInputStartsRuntime(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	result, err := service.StartSessionInput("sess_1", "hello", sessiontypes.TurnOptions{Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "start" || result["ok"] != true {
		t.Fatalf("result = %#v, want start", result)
	}
	if len(runtime.started) != 1 || runtime.started[0].input != "hello" || runtime.started[0].options.Model != "gpt-test" {
		t.Fatalf("runtime starts = %#v", runtime.started)
	}
	if len(emitted) != 0 {
		t.Fatalf("emitted = %#v, want none", emitted)
	}
}

func TestServiceStartSessionInputDrainsRuntimeEvents(t *testing.T) {
	store := newFakeStore()
	var emitted []protocol.AstralEvent
	service := newTestService(store, &fakeRuntime{}, &emitted, nil)
	service.agents = map[protocol.AgentKind]agents.Runtime{
		protocol.AgentCodex: fakeEventRuntime{events: []agents.RuntimeEvent{{
			Event: testEvent(1, "sess_1", protocol.AgentCodex, "message.assistant", map[string]any{"text": "hello"}),
		}}},
	}

	result, err := service.StartSessionInput("sess_1", "hello", sessiontypes.TurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "start" {
		t.Fatalf("result = %#v, want start", result)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(emitted) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(emitted) != 1 || emitted[0].Kind != "message.assistant" {
		t.Fatalf("emitted = %#v, want runtime message event", emitted)
	}
}

func TestServiceRuntimeEventsDriveTurnLifecycleStatus(t *testing.T) {
	tests := []struct {
		name       string
		kind       protocol.AstralEventKind
		wantStatus string
	}{
		{name: "started", kind: "turn.started", wantStatus: "running"},
		{name: "completed", kind: "turn.completed", wantStatus: "idle"},
		{name: "cancelled", kind: "turn.cancelled", wantStatus: "idle"},
		{name: "failed", kind: "turn.failed", wantStatus: "failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			var emitted []protocol.AstralEvent
			service := newTestService(store, &fakeRuntime{}, &emitted, nil)
			service.agents = map[protocol.AgentKind]agents.Runtime{
				protocol.AgentCodex: fakeEventRuntime{events: []agents.RuntimeEvent{{
					Event: testEvent(1, "sess_1", protocol.AgentCodex, tc.kind, map[string]any{"turn_id": "turn_1"}),
				}}},
			}

			if _, err := service.StartSessionInput("sess_1", "hello", sessiontypes.TurnOptions{}); err != nil {
				t.Fatal(err)
			}
			waitForServiceEvent(t, &emitted, tc.kind)
			if got := store.sessions["sess_1"].Status; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

func TestServiceRuntimeErrorEventFailsTurn(t *testing.T) {
	store := newFakeStore()
	var emitted []protocol.AstralEvent
	service := newTestService(store, &fakeRuntime{}, &emitted, nil)
	service.agents = map[protocol.AgentKind]agents.Runtime{
		protocol.AgentCodex: fakeEventRuntime{events: []agents.RuntimeEvent{{
			Event: protocol.AstralEvent{WorkspaceID: "ws_1", SessionID: "sess_1", Agent: protocol.AgentCodex},
			Err:   errors.New("runtime crashed"),
		}}},
	}

	if _, err := service.StartSessionInput("sess_1", "hello", sessiontypes.TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	waitForServiceEvent(t, &emitted, "turn.failed")
	if got := store.sessions["sess_1"].Status; got != "failed" {
		t.Fatalf("status = %q, want failed", got)
	}
}

func TestServiceRuntimeCompletedDoesNotStartQueueWithPendingInteraction(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntime{}
	service := newTestService(store, runtime, nil, nil)
	service.EnqueueTurn(store.sessions["sess_1"], "queued", sessiontypes.TurnOptions{})
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "ask.requested", map[string]any{
			"ask_id": "ask_1",
			"kind":   "AskUserQuestion",
		}),
	}

	service.applyRuntimeEvent(agents.RuntimeEvent{Event: testEvent(2, "sess_1", protocol.AgentCodex, "turn.completed", map[string]any{"status": "idle"})})

	if len(runtime.started) != 0 {
		t.Fatalf("runtime started queued turn despite pending interaction: %#v", runtime.started)
	}
	if queued := service.QueueSnapshot("sess_1"); len(queued) != 1 || queued[0].Input != "queued" {
		t.Fatalf("queue = %#v, want queued turn retained", queued)
	}
}

func TestServiceStartSessionInputQueuesWhenRuntimeIsRunning(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntimeNoSteer{startErr: sessiontypes.ErrSessionRunning}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	result, err := service.StartSessionInput("sess_1", "second", sessiontypes.TurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "queue" || result["queued"] != true || !strings.HasPrefix(result["queue_id"].(string), "queue_") {
		t.Fatalf("result = %#v, want queued", result)
	}
	if len(emitted) != 1 || emitted[0].Kind != "queue.queued" {
		t.Fatalf("emitted = %#v, want queue.queued", emitted)
	}
}

func TestServiceStartSessionInputSteersRunningSession(t *testing.T) {
	store := newFakeStore()
	store.sessions["sess_1"] = protocol.Session{ID: "sess_1", WorkspaceID: "ws_1", Agent: protocol.AgentCodex, Status: "running"}
	runtime := &fakeRuntime{}
	service := newTestService(store, runtime, nil, nil)

	result, err := service.StartSessionInput("sess_1", "steer this", sessiontypes.TurnOptions{PermissionMode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "steer" || result["steered"] != true {
		t.Fatalf("result = %#v, want steer", result)
	}
	if len(runtime.steered) != 1 || runtime.steered[0].input != "steer this" || runtime.steered[0].options.PermissionMode != "auto" {
		t.Fatalf("runtime steers = %#v", runtime.steered)
	}
}

func TestServiceCancelControlQueuedTurnRemovesQueuedInput(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntimeNoSteer{startErr: sessiontypes.ErrSessionRunning}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)
	result, err := service.StartSessionInput("sess_1", "queued", sessiontypes.TurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	queueID := result["queue_id"].(string)

	cancelled, err := service.CancelControlQueuedTurn(protocol.QueueControlParams{SessionID: "sess_1", QueueID: queueID})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled["ok"] != true || cancelled["queue_id"] != queueID {
		t.Fatalf("cancelled = %#v, want ok queue id", cancelled)
	}
	if _, ok := service.queue.PeekQueuedTurn("sess_1", queueID); ok {
		t.Fatal("queued turn was not removed")
	}
	if len(emitted) != 2 || emitted[1].Kind != "queue.cancelled" {
		t.Fatalf("emitted = %#v, want queue.cancelled", emitted)
	}
}

func TestServiceDeleteSessionStopsRuntimeAndEmitsFact(t *testing.T) {
	store := newFakeStore()
	var stopped []string
	var emitted []protocol.AstralEvent
	service := newTestService(store, &fakeRuntime{}, &emitted, func(session protocol.Session, reason string) {
		stopped = append(stopped, session.ID+":"+reason)
	})

	result, err := service.DeleteSessionByID("sess_1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.SessionID != "sess_1" {
		t.Fatalf("result = %#v, want deleted session", result)
	}
	if _, ok := store.sessions["sess_1"]; ok {
		t.Fatal("session was not deleted from store")
	}
	if len(stopped) != 1 || stopped[0] != "sess_1:session deleted" {
		t.Fatalf("stopped = %#v", stopped)
	}
	if len(emitted) != 1 || emitted[0].Kind != "session.deleted" {
		t.Fatalf("emitted = %#v, want session.deleted", emitted)
	}
}

func TestServiceCreateSessionDefaultsWorkspaceAgentAndEmitsFact(t *testing.T) {
	store := newFakeStore()
	var emitted []protocol.AstralEvent
	service := newTestService(store, &fakeRuntime{}, &emitted, nil)

	session, err := service.CreateSession("ws_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "created_1" || session.Agent != protocol.AgentCodex {
		t.Fatalf("session = %#v, want created codex", session)
	}
	if len(emitted) != 1 || emitted[0].Kind != "session.started" {
		t.Fatalf("emitted = %#v, want session.started", emitted)
	}
}

func TestServiceSteerControlQueuedTurnRemovesQueueAndEmitsFact(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)
	turn := service.EnqueueTurn(store.sessions["sess_1"], "queued", sessiontypes.TurnOptions{PermissionMode: "auto"})

	result, err := service.SteerControlQueuedTurn(protocol.QueueControlParams{SessionID: "sess_1", QueueID: turn.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["queue_id"] != turn.ID {
		t.Fatalf("result = %#v, want ok queue id", result)
	}
	if _, ok := service.PeekQueuedTurn("sess_1", turn.ID); ok {
		t.Fatal("queued turn was not removed after steer")
	}
	if len(runtime.steered) != 1 || runtime.steered[0].input != "queued" {
		t.Fatalf("steered = %#v, want queued", runtime.steered)
	}
	if len(emitted) != 2 || emitted[1].Kind != "queue.steered" {
		t.Fatalf("emitted = %#v, want queue.steered", emitted)
	}
}

func TestServiceStartNextQueuedTurnStartsRuntimeAndDequeues(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)
	turn := service.EnqueueTurn(store.sessions["sess_1"], "next", sessiontypes.TurnOptions{})
	service.agents = map[protocol.AgentKind]agents.Runtime{
		protocol.AgentCodex: fakeEventRuntime{events: []agents.RuntimeEvent{{
			Event: testEvent(2, "sess_1", protocol.AgentCodex, "message.assistant", map[string]any{"text": "next done"}),
		}}},
	}

	service.StartNextQueuedTurn("sess_1")
	if len(runtime.started) != 0 {
		t.Fatalf("legacy runtime starts = %#v, want queued turn to use agents runtime stream", runtime.started)
	}
	if _, ok := service.PeekQueuedTurn("sess_1", turn.ID); ok {
		t.Fatal("queued turn was not dequeued")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hasEventKind(emitted, "queue.dequeued") && hasEventKind(emitted, "message.assistant") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("emitted = %#v, want queue.dequeued and runtime message event", emitted)
}

func hasEventKind(events []protocol.AstralEvent, kind protocol.AstralEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func waitForServiceEvent(t *testing.T, events *[]protocol.AstralEvent, kind protocol.AstralEventKind) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hasEventKind(*events, kind) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("events = %#v, want %s", *events, kind)
}

func TestServiceClearSessionQueueEmitsCancelledFacts(t *testing.T) {
	store := newFakeStore()
	var emitted []protocol.AstralEvent
	service := newTestService(store, &fakeRuntime{}, &emitted, nil)
	first := service.EnqueueTurn(store.sessions["sess_1"], "one", sessiontypes.TurnOptions{})
	second := service.EnqueueTurn(store.sessions["sess_1"], "two", sessiontypes.TurnOptions{Internal: true})

	service.ClearSessionQueue("sess_1", "test")
	if snapshot := service.QueueSnapshot("sess_1"); len(snapshot) != 0 {
		t.Fatalf("queue = %#v, want empty", snapshot)
	}
	if len(emitted) != 4 || emitted[2].Kind != "queue.cancelled" || emitted[3].Kind != "queue.cancelled" {
		t.Fatalf("emitted = %#v, want two cancelled facts", emitted)
	}
	if emitted[2].Normalized == nil || first.ID == "" || second.ID == "" {
		t.Fatalf("cancelled events missing queue ids: %#v", emitted[2:])
	}
}

func TestServiceInterruptSessionUsesRuntimeBoundary(t *testing.T) {
	store := newFakeStore()
	runtime := &fakeRuntime{}
	service := newTestService(store, runtime, nil, nil)

	result, err := service.InterruptSession("sess_1")
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %#v, want ok", result)
	}
	if len(runtime.interrupted) != 1 || runtime.interrupted[0] != "sess_1" {
		t.Fatalf("interrupted = %#v, want sess_1", runtime.interrupted)
	}
}

func TestServiceRespondInteractionEmitsRespondedFact(t *testing.T) {
	store := newFakeStore()
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "approval.requested", map[string]any{
			"approval_id": "approval_1",
			"kind":        "command",
		}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	result, err := service.RespondInteraction("approval_1", map[string]any{"decision": "accept"})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %#v, want ok", result)
	}
	if runtime.respondedID != "approval_1" || runtime.responded["decision"] != "accept" {
		t.Fatalf("responded = %q/%#v, want approval_1 accept", runtime.respondedID, runtime.responded)
	}
	if len(emitted) != 1 || emitted[0].Kind != "approval.responded" {
		t.Fatalf("emitted = %#v, want approval.responded", emitted)
	}
}

func TestServiceRespondInteractionRejectsStaleInteraction(t *testing.T) {
	store := newFakeStore()
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "approval.requested", map[string]any{"approval_id": "approval_1"}),
		testEvent(2, "sess_1", protocol.AgentCodex, "approval.responded", map[string]any{"approval_id": "approval_1"}),
	}
	service := newTestService(store, &fakeRuntime{}, nil, nil)

	_, err := service.RespondInteraction("approval_1", map[string]any{"decision": "accept"})
	var actionErr *apperrors.ActionError
	if !errors.As(err, &actionErr) || actionErr.Code != "interaction_stale" {
		t.Fatalf("err = %#v, want interaction_stale", err)
	}
}

func TestServiceEditLastUserMessageEmitsReplacementFact(t *testing.T) {
	store := newFakeStore()
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "message.user", map[string]any{"text": "old"}),
		testEvent(2, "sess_1", protocol.AgentCodex, "turn.started", map[string]any{"turn_id": "turn_1"}),
		testEvent(3, "sess_1", protocol.AgentCodex, "message.assistant", map[string]any{"text": "answer"}),
		testEvent(4, "sess_1", protocol.AgentCodex, "turn.completed", map[string]any{"turn_id": "turn_1"}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	result, err := service.EditLastUserMessage("sess_1", protocol.EditLastUserMessageRequest{EventSeq: 1, Input: "new", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("result = %#v, want ok", result)
	}
	if len(runtime.edited) != 1 || runtime.edited[0].input != "new" || runtime.edited[0].options.Model != "gpt-test" {
		t.Fatalf("edited = %#v, want new/gpt-test", runtime.edited)
	}
	if len(emitted) != 1 || emitted[0].Kind != "turn.replaced" {
		t.Fatalf("emitted = %#v, want turn.replaced", emitted)
	}
}

func TestServiceForkSessionCreatesForkAndInvokesRuntime(t *testing.T) {
	store := newFakeStore()
	store.sessions["sess_1"] = protocol.Session{ID: "sess_1", WorkspaceID: "ws_1", Agent: protocol.AgentCodex, Status: "idle", NativeThreadID: "thread_1"}
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "message.user", map[string]any{"text": "question"}),
		testEvent(2, "sess_1", protocol.AgentCodex, "turn.started", map[string]any{"turn_id": "turn_1"}),
		testEvent(3, "sess_1", protocol.AgentCodex, "message.assistant", map[string]any{"text": "answer"}),
		testEvent(4, "sess_1", protocol.AgentCodex, "turn.completed", map[string]any{"turn_id": "turn_1"}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	result, err := service.ForkSession("sess_1", protocol.ForkSessionRequest{EventSeq: 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.ID != "fork_1" || result.Session.ForkedFromSessionID != "sess_1" {
		t.Fatalf("fork = %#v, want fork_1 from sess_1", result.Session)
	}
	if len(runtime.forked) != 1 || runtime.forked[0].sessionID != "sess_1" {
		t.Fatalf("forked = %#v, want source sess_1", runtime.forked)
	}
	if len(emitted) != 1 || emitted[0].Kind != "session.started" {
		t.Fatalf("emitted = %#v, want session.started", emitted)
	}
}

func TestServiceRespondInteractionStartsCodexPlanFollowup(t *testing.T) {
	store := newFakeStore()
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "approval.requested", map[string]any{
			"approval_id": "plan_1",
			"kind":        "plan",
		}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	_, err := service.RespondInteraction("plan_1", map[string]any{"action_id": "accept"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.started) != 1 || !strings.Contains(runtime.started[0].input, "Plan approved") || !runtime.started[0].options.Internal {
		t.Fatalf("started = %#v, want internal plan followup", runtime.started)
	}
	if len(emitted) != 1 || emitted[0].Kind != "approval.responded" {
		t.Fatalf("emitted = %#v, want approval.responded", emitted)
	}
}

func TestServiceRespondInteractionStartsClaudeFollowup(t *testing.T) {
	store := newFakeStore()
	store.sessions["sess_1"] = protocol.Session{ID: "sess_1", WorkspaceID: "ws_1", Agent: protocol.AgentClaude, Status: "idle"}
	store.workspaces["ws_1"] = protocol.Workspace{ID: "ws_1", Agent: protocol.AgentClaude, Target: "local"}
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentClaude, "ask.requested", map[string]any{
			"ask_id": "ask_1",
			"kind":   "AskUserQuestion",
		}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := NewService(store, map[protocol.AgentKind]agents.Runtime{protocol.AgentClaude: agents.AdaptLegacy(runtime)}, func(event protocol.AstralEvent) {
		emitted = append(emitted, event)
	}, func(protocol.Session, string) {})

	_, err := service.RespondInteraction("ask_1", map[string]any{"text": "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.started) != 1 || !strings.Contains(runtime.started[0].input, "Answer to the previous question") || !runtime.started[0].options.Internal {
		t.Fatalf("started = %#v, want internal ask followup", runtime.started)
	}
	if len(emitted) != 1 || emitted[0].Kind != "ask.resolved" {
		t.Fatalf("emitted = %#v, want ask.resolved", emitted)
	}
}

func TestServiceRespondInteractionCancelInterruptsRuntime(t *testing.T) {
	store := newFakeStore()
	store.events = []protocol.AstralEvent{
		testEvent(1, "sess_1", protocol.AgentCodex, "ask.requested", map[string]any{
			"ask_id": "ask_1",
			"kind":   "AskUserQuestion",
		}),
	}
	runtime := &fakeRuntime{}
	var emitted []protocol.AstralEvent
	service := newTestService(store, runtime, &emitted, nil)

	_, err := service.RespondInteraction("ask_1", map[string]any{"action_id": "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.interrupted) != 1 || runtime.interrupted[0] != "sess_1" {
		t.Fatalf("interrupted = %#v, want sess_1", runtime.interrupted)
	}
	if len(emitted) != 1 || emitted[0].Kind != "ask.resolved" {
		t.Fatalf("emitted = %#v, want ask.resolved", emitted)
	}
}

func TestInteractionClientActionMappingAndFollowupText(t *testing.T) {
	ask := testEvent(1, "sess_1", protocol.AgentCodex, "ask.requested", map[string]any{
		"ask_id": "ask_1",
		"params": map[string]any{
			"questions": []any{map[string]any{"id": "q", "question": "Question?"}},
		},
	})
	mapped := InteractionResponseForClientAction(ask, map[string]any{"action_id": "submit", "answers": map[string]any{"q": "answer"}})
	if answer := mapValue(mapValue(mapped["answers"])["q"])["answers"]; answer == nil {
		t.Fatalf("mapped = %#v, want answers payload", mapped)
	}

	approval := testEvent(2, "sess_1", protocol.AgentClaude, "approval.requested", map[string]any{
		"approval_id": "approval_1",
		"kind":        "permission",
		"tool_name":   "Bash",
		"command":     "go test ./...",
		"params":      map[string]any{"command": "go test ./..."},
	})
	response := InteractionResponseForClientAction(approval, map[string]any{"action_id": "acceptForSession"})
	if response["decision"] != "acceptForSession" {
		t.Fatalf("response = %#v, want acceptForSession", response)
	}
	text := ClaudeInteractionFollowupText(approval, response)
	if !strings.Contains(text, "approved") || !strings.Contains(text, "go test ./...") {
		t.Fatalf("followup = %q, want approval text with command", text)
	}
	tools := ClaudeAllowedToolsForInteraction(approval, response, protocol.Workspace{ID: "ws_1", Target: "local"})
	if len(tools) != 1 || !strings.Contains(tools[0], "go test ./...") {
		t.Fatalf("tools = %#v, want Bash rule", tools)
	}
	if display := ClaudeInteractionDisplayText(approval, response); display != "权限已允许" {
		t.Fatalf("display = %q, want 权限已允许", display)
	}
	if codex := CodexPlanFollowupText(map[string]any{"decision": "decline", "feedback": "smaller"}); !strings.Contains(codex, "smaller") {
		t.Fatalf("codex followup = %q, want feedback", codex)
	}
}

func TestSafeForkTranscriptEventsProjectsOnlyTranscriptFamilies(t *testing.T) {
	fork := protocol.Session{ID: "fork_1", WorkspaceID: "ws_1", Agent: protocol.AgentClaude}
	source := []protocol.AstralEvent{
		testEvent(1, "source_1", protocol.AgentClaude, "session.started", map[string]any{"session_id": "source_1"}),
		testEvent(2, "source_1", protocol.AgentClaude, "message.user", map[string]any{"text": "hi"}),
		testEvent(3, "source_1", protocol.AgentClaude, "turn.completed", map[string]any{"turn_id": "turn_1"}),
		testEvent(4, "source_1", protocol.AgentClaude, "control.context", map[string]any{"total_tokens": 1}),
	}
	projected := safeForkTranscriptEvents(source, 3, fork)
	if len(projected) != 2 {
		t.Fatalf("projected = %#v, want message + turn", projected)
	}
	for _, event := range projected {
		if event.SessionID != "fork_1" || !boolValue(mapValue(event.Normalized)["fork_projection"]) {
			t.Fatalf("event = %#v, want fork projection", event)
		}
	}
	if !boolValue(mapValue(projected[1].Normalized)["suppress_notification"]) {
		t.Fatalf("turn completion projection = %#v, want suppress_notification", projected[1])
	}
}

func newTestService(store *fakeStore, runtime sessiontypes.AgentRuntime, emitted *[]protocol.AstralEvent, stop func(protocol.Session, string)) *Service {
	emit := func(event protocol.AstralEvent) {
		if emitted != nil {
			*emitted = append(*emitted, event)
		}
	}
	if stop == nil {
		stop = func(protocol.Session, string) {}
	}
	return NewService(store, map[protocol.AgentKind]agents.Runtime{protocol.AgentCodex: agents.AdaptLegacy(runtime)}, emit, stop)
}

type fakeStore struct {
	workspaces map[string]protocol.Workspace
	sessions   map[string]protocol.Session
	events     []protocol.AstralEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		workspaces: map[string]protocol.Workspace{
			"ws_1": {ID: "ws_1", Agent: protocol.AgentCodex},
		},
		sessions: map[string]protocol.Session{
			"sess_1": {ID: "sess_1", WorkspaceID: "ws_1", Agent: protocol.AgentCodex, Status: "idle"},
		},
	}
}

func (s *fakeStore) GetSession(id string) (protocol.Session, bool) {
	session, ok := s.sessions[id]
	return session, ok
}

func (s *fakeStore) GetWorkspace(id string) (protocol.Workspace, bool) {
	workspace, ok := s.workspaces[id]
	return workspace, ok
}

func (s *fakeStore) CreateSession(workspace protocol.Workspace, agent protocol.AgentKind) protocol.Session {
	session := protocol.Session{ID: "created_1", WorkspaceID: workspace.ID, Agent: agent, Status: "idle"}
	s.sessions[session.ID] = session
	return session
}

func (s *fakeStore) CreateForkSession(workspace protocol.Workspace, source protocol.Session, anchor sessiontypes.ForkAnchor) protocol.Session {
	session := protocol.Session{ID: "fork_1", WorkspaceID: workspace.ID, Agent: source.Agent, Status: "idle", ForkedFromSessionID: source.ID, ForkedFromEventSeq: anchor.EventSeq}
	s.sessions[session.ID] = session
	return session
}

func (s *fakeStore) DeleteSession(id string) {
	delete(s.sessions, id)
}

func (s *fakeStore) UpdateSessionStatus(id, status string) {
	session := s.sessions[id]
	session.Status = status
	s.sessions[id] = session
}

func (s *fakeStore) QueryEvents(workspaceID, sessionID string, afterSeq int64) []protocol.AstralEvent {
	var out []protocol.AstralEvent
	for _, event := range s.events {
		if workspaceID != "" && event.WorkspaceID != workspaceID {
			continue
		}
		if sessionID != "" && event.SessionID != sessionID {
			continue
		}
		if event.Seq <= afterSeq {
			continue
		}
		out = append(out, event)
	}
	return out
}

func (s *fakeStore) SessionTitle(sessionID string) string {
	return "Test Session"
}

type runtimeCall struct {
	sessionID string
	input     string
	options   sessiontypes.TurnOptions
}

type fakeRuntime struct {
	startErr    error
	steerErr    error
	started     []runtimeCall
	steered     []runtimeCall
	interrupted []string
	respondedID string
	responded   map[string]any
	edited      []runtimeCall
	forked      []runtimeCall
}

func (r *fakeRuntime) StartTurn(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	r.started = append(r.started, runtimeCall{sessionID: session.ID, input: input, options: options})
	return r.startErr
}

func (r *fakeRuntime) Interrupt(sessionID string) error {
	r.interrupted = append(r.interrupted, sessionID)
	return nil
}

func (r *fakeRuntime) Steer(sessionID string, input string, options sessiontypes.TurnOptions) error {
	r.steered = append(r.steered, runtimeCall{sessionID: sessionID, input: input, options: options})
	return r.steerErr
}

func (r *fakeRuntime) RespondApproval(approvalID string, response map[string]any) error {
	r.respondedID = approvalID
	r.responded = response
	return nil
}

func (r *fakeRuntime) EditLastUserMessageAndResend(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	r.edited = append(r.edited, runtimeCall{sessionID: session.ID, input: input, options: options})
	return nil
}

func (r *fakeRuntime) ForkSession(source protocol.Session, fork protocol.Session, workspace protocol.Workspace, rollbackTurns int) error {
	r.forked = append(r.forked, runtimeCall{sessionID: source.ID, input: fork.ID})
	return nil
}

type fakeRuntimeNoSteer struct {
	startErr error
	started  []runtimeCall
}

func (r *fakeRuntimeNoSteer) StartTurn(session protocol.Session, workspace protocol.Workspace, input string, options sessiontypes.TurnOptions) error {
	r.started = append(r.started, runtimeCall{sessionID: session.ID, input: input, options: options})
	return r.startErr
}

func (r *fakeRuntimeNoSteer) Interrupt(sessionID string) error {
	return errors.New("not used")
}

type fakeEventRuntime struct {
	events []agents.RuntimeEvent
}

func (r fakeEventRuntime) StartTurn(context.Context, agents.TurnRequest) (<-chan agents.RuntimeEvent, error) {
	ch := make(chan agents.RuntimeEvent, len(r.events))
	for _, event := range r.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (r fakeEventRuntime) Interrupt(context.Context, string) error {
	return nil
}

func (r fakeEventRuntime) RespondInteraction(context.Context, agents.InteractionResponse) error {
	return agents.ErrInteractionUnsupported
}

func testEvent(seq int64, sessionID string, agent protocol.AgentKind, kind protocol.AstralEventKind, normalized map[string]any) protocol.AstralEvent {
	return protocol.AstralEvent{
		Seq:         seq,
		WorkspaceID: "ws_1",
		SessionID:   sessionID,
		Agent:       agent,
		Kind:        kind,
		Normalized:  protocol.EventNormalized(kind, normalized),
	}
}
