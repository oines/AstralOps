package main

import (
	"errors"
	"net/http"
	"testing"
)

func newControlGatewayTestApp(t *testing.T, agent AgentKind, runtime AgentRuntime) (*app, Workspace, Session) {
	t.Helper()

	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:     "Gateway",
		Target:   "local",
		Agent:    agent,
		LocalCWD: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, agent)
	return &app{store: st, hub: newEventHub(), runtimes: map[AgentKind]AgentRuntime{agent: runtime}}, workspace, session
}

func trustControlDevice(t *testing.T, app *app, deviceID string, capabilities ...string) TrustGrant {
	t.Helper()

	grant, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID: deviceID,
		Capabilities:       capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func assertActionError(t *testing.T, err error, status int, code string) {
	t.Helper()

	var actionErr *actionError
	if !errors.As(err, &actionErr) {
		t.Fatalf("err = %#v, want actionError", err)
	}
	if actionErr.Status != status || actionErr.Code != code {
		t.Fatalf("action error = status %d code %q, want status %d code %q", actionErr.Status, actionErr.Code, status, code)
	}
}

func TestControlGatewayRequiresCapability(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_1",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"input":      "run tests",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
	if len(runtime.inputs) != 0 {
		t.Fatalf("runtime inputs = %#v, want none", runtime.inputs)
	}
}

func TestControlGatewayRejectsCapabilityMismatch(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_desktop", CapabilityCoreRead, CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_desktop",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"input":      "hello",
		},
	})
	assertActionError(t, err, http.StatusForbidden, "capability_mismatch")
}

func TestControlGatewayReadsSessionView(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_view",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionSessionView,
		Params:             map[string]any{"session_id": session.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != "req_view" {
		t.Fatalf("response = %#v, want ok with request id", response)
	}
	view, ok := response.Result.(sessionView)
	if !ok {
		t.Fatalf("result = %#v, want sessionView", response.Result)
	}
	if view.Session.ID != session.ID {
		t.Fatalf("session id = %q, want %q", view.Session.ID, session.ID)
	}
}

func TestControlGatewayReadsEventsWindow(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)
	first, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": "one"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: map[string]any{"turn_id": "turn-1"}})
	if err != nil {
		t.Fatal(err)
	}
	third, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: map[string]any{"text": "answer"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.store.appendEvent(AstralEvent{WorkspaceID: "other_workspace", SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": "filtered"}})
	if err != nil {
		t.Fatal(err)
	}

	response, err := app.executeControlRequest(ControlRequest{
		RequestID:          "req_events",
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             ControlActionEvents,
		Params: map[string]any{
			"workspace_id": workspace.ID,
			"session_id":   session.ID,
			"after_seq":    first.Seq,
			"before_seq":   third.Seq + 1,
			"limit":        2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != "req_events" {
		t.Fatalf("response = %#v, want ok with request id", response)
	}
	events, ok := response.Result.([]AstralEvent)
	if !ok {
		t.Fatalf("events result = %#v, want []AstralEvent", response.Result)
	}
	if len(events) != 2 || events[0].Seq != second.Seq || events[1].Seq != third.Seq {
		t.Fatalf("events = %#v, want second and third event in seq order", events)
	}
}

func TestControlGatewayStartsSessionInput(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id":       session.ID,
			"input":            "implement gateway",
			"model":            "gpt-test",
			"reasoning_effort": "low",
			"permission_mode":  "auto",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("response = %#v, want ok", response)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "start" || boolValue(result["queued"]) || boolValue(result["steered"]) {
		t.Fatalf("session input result = %#v, want start mode", result)
	}
	if len(runtime.inputs) != 1 || runtime.inputs[0] != "implement gateway" {
		t.Fatalf("runtime inputs = %#v, want gateway input", runtime.inputs)
	}
	if runtime.options[0].Model != "gpt-test" || runtime.options[0].ReasoningEffort != "low" || runtime.options[0].PermissionMode != "auto" {
		t.Fatalf("runtime options = %#v", runtime.options[0])
	}
}

func TestControlGatewaySessionInputQueuesWhenRuntimeIsRunning(t *testing.T) {
	runtime := &recordingRuntime{startErr: ErrSessionRunning}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"input":      "queue this",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "queue" || !boolValue(result["queued"]) || stringValue(result["queue_id"]) == "" {
		t.Fatalf("session input result = %#v, want queue mode", result)
	}
	if !containsEventKind(app.store.queryEvents("", session.ID, 0), "queue.queued") {
		t.Fatalf("events = %#v, want queue.queued", app.store.queryEvents("", session.ID, 0))
	}
}

func TestControlGatewaySessionInputSteersWhenRuntimeSupportsSteer(t *testing.T) {
	runtime := &recordingSteerRuntime{recordingRuntime: recordingRuntime{startErr: ErrSessionRunning}}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionInput,
		Params: map[string]any{
			"session_id": session.ID,
			"input":      "steer this",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if stringValue(result["mode"]) != "steer" || !boolValue(result["steered"]) || boolValue(result["queued"]) {
		t.Fatalf("session input result = %#v, want steer mode", result)
	}
	if len(runtime.steered) != 1 || runtime.steered[0] != "steer this" {
		t.Fatalf("steered = %#v, want steer this", runtime.steered)
	}
}

func TestControlGatewayQueueCancelCancelsQueuedInput(t *testing.T) {
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueCancel,
		Params: map[string]any{
			"session_id": session.ID,
			"queue_id":   turn.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !boolValue(result["ok"]) || stringValue(result["queue_id"]) != turn.ID {
		t.Fatalf("queue cancel result = %#v", result)
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after cancel")
	}
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.cancelled") {
		t.Fatalf("events = %#v, want queue.cancelled", events)
	}
}

func TestControlGatewayQueueSteerSteersQueuedInput(t *testing.T) {
	runtime := &recordingSteerRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "steer queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueSteer,
		Params: map[string]any{
			"session_id": session.ID,
			"queue_id":   turn.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := mapValue(response.Result)
	if !boolValue(result["ok"]) || stringValue(result["queue_id"]) != turn.ID {
		t.Fatalf("queue steer result = %#v", result)
	}
	if len(runtime.steered) != 1 || runtime.steered[0] != "steer queued prompt" {
		t.Fatalf("steered = %#v", runtime.steered)
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after steer")
	}
	events := app.store.queryEvents(workspace.ID, session.ID, 0)
	if !containsEventKind(events, "queue.steered") {
		t.Fatalf("events = %#v, want queue.steered", events)
	}
}

func TestControlGatewayQueueCancelRequiresExistingQueue(t *testing.T) {
	app, _, session := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionQueueCancel,
		Params: map[string]any{
			"session_id": session.ID,
			"queue_id":   "queue_missing",
		},
	})
	assertActionError(t, err, http.StatusNotFound, "queue_not_found")
}

func TestControlGatewayForksSession(t *testing.T) {
	runtime := &recordingForkRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	session.NativeThreadID = "source-thread"
	app.store.mu.Lock()
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)

	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": "one"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: map[string]any{"turn_id": "turn-1", "status": "running"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: map[string]any{"text": "answer", "item_id": "item-1"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: map[string]any{"turn_id": "turn-1", "status": "idle"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": "two"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.started", Normalized: map[string]any{"turn_id": "turn-2", "status": "running"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.assistant", Normalized: map[string]any{"text": "later", "item_id": "item-2"}})
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "turn.completed", Normalized: map[string]any{"turn_id": "turn-2", "status": "idle"}})
	targetSeq := int64(0)
	for _, event := range app.store.queryEvents("", session.ID, 0) {
		if event.Kind == "message.assistant" && stringValue(mapValue(event.Normalized)["text"]) == "answer" {
			targetSeq = event.Seq
			break
		}
	}
	if targetSeq == 0 {
		t.Fatal("missing fork target")
	}

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionFork,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  targetSeq,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(forkSessionResponse)
	if !ok {
		t.Fatalf("fork result = %#v, want forkSessionResponse", response.Result)
	}
	if result.Session.ForkedFromSessionID != session.ID || result.Session.ForkedFromEventSeq != targetSeq || result.Session.ForkedFromNativeAnchor != "turn-1" {
		t.Fatalf("fork metadata = %#v", result.Session)
	}
	if runtime.source.ID != session.ID || runtime.fork.ID != result.Session.ID || runtime.workspace.ID != workspace.ID {
		t.Fatalf("fork runtime call = source %#v fork %#v workspace %#v", runtime.source, runtime.fork, runtime.workspace)
	}
	if runtime.rollbackTurns != 1 {
		t.Fatalf("rollbackTurns = %d, want 1", runtime.rollbackTurns)
	}
	if !containsEventKind(app.store.queryEvents("", result.Session.ID, 0), "session.started") {
		t.Fatalf("fork events = %#v, want session.started", eventKinds(app.store.queryEvents("", result.Session.ID, 0)))
	}
}

func TestControlGatewayDeletesSession(t *testing.T) {
	runtime := &recordingRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityCoreControl)
	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreControl,
		Action:             ControlActionSessionDelete,
		Params: map[string]any{
			"session_id": session.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(sessionDeleteResult)
	if !ok {
		t.Fatalf("delete result = %#v, want sessionDeleteResult", response.Result)
	}
	if !result.OK || result.SessionID != session.ID {
		t.Fatalf("delete result = %#v", result)
	}
	if _, ok := app.store.getSession(session.ID); ok {
		t.Fatal("session still exists after delete")
	}
	if _, ok := app.peekQueuedTurn(session.ID, turn.ID); ok {
		t.Fatal("queued input still exists after session delete")
	}
	if len(runtime.interrupts) != 1 || runtime.interrupts[0] != session.ID {
		t.Fatalf("runtime interrupts = %#v, want deleted session interrupted", runtime.interrupts)
	}
	events := app.store.queryEvents("", session.ID, 0)
	if !containsEventKind(events, "session.deleted") {
		t.Fatalf("events = %#v, want session.deleted", eventKinds(events))
	}
}

func TestControlGatewayRejectsReplacedInteraction(t *testing.T) {
	runtime := &recordingRuntime{}
	app, workspace, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_mobile", CapabilityInteractionRespond)
	request, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "approval.requested",
		Normalized:  map[string]any{"approval_id": "approval_replaced"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.appendEvent(AstralEvent{
		WorkspaceID: workspace.ID,
		SessionID:   session.ID,
		Agent:       AgentCodex,
		Kind:        "turn.replaced",
		Normalized: map[string]any{
			"start_seq": request.Seq,
			"end_seq":   request.Seq,
			"hidden":    true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityInteractionRespond,
		Action:             ControlActionInteractionRespond,
		Params: map[string]any{
			"interaction_id": "approval_replaced",
			"response":       map[string]any{"decision": "accept"},
		},
	})
	assertActionError(t, err, http.StatusConflict, "interaction_stale")
	if len(runtime.approvalResponses) != 0 {
		t.Fatalf("runtime responses = %#v, want none", runtime.approvalResponses)
	}
}

func TestControlGatewayRejectsStaleSessionEdit(t *testing.T) {
	runtime := &recordingEditRuntime{}
	app, _, session := newControlGatewayTestApp(t, AgentCodex, runtime)
	trustControlDevice(t, app, "device_desktop", CapabilitySessionEdit)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_desktop",
		Capability:         CapabilitySessionEdit,
		Action:             ControlActionSessionEdit,
		Params: map[string]any{
			"session_id": session.ID,
			"event_seq":  int64(999),
			"input":      "replacement",
		},
	})
	assertActionError(t, err, http.StatusConflict, "editable_message_stale")
	if runtime.editCalls != 0 {
		t.Fatalf("edit calls = %d, want 0", runtime.editCalls)
	}
}

func TestControlGatewayRejectsUnknownAction(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityCoreRead,
		Action:             "core.read.unknown",
	})
	assertActionError(t, err, http.StatusNotFound, "control_action_unknown")
}

func TestControlGatewayHostTrustListRequiresHostManage(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_mobile", CapabilityCoreRead)

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustList,
	})
	assertActionError(t, err, http.StatusForbidden, "capability_denied")
}

func TestControlGatewayHostTrustListReturnsTrustGrants(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_admin", CapabilityHostManage)
	trustControlDevice(t, app, "device_reader", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_admin",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustList,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(hostTrustListResult)
	if !ok {
		t.Fatalf("trust list result = %#v, want hostTrustListResult", response.Result)
	}
	seen := map[string]bool{}
	for _, grant := range result.Grants {
		seen[grant.ControllerDeviceID] = true
	}
	if !seen["device_admin"] || !seen["device_reader"] {
		t.Fatalf("trust grants = %#v, want admin and reader", result.Grants)
	}
}

func TestControlGatewayHostTrustRevokeRevokesTrustedDevice(t *testing.T) {
	app, _, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	trustControlDevice(t, app, "device_admin", CapabilityHostManage)
	trustControlDevice(t, app, "device_reader", CapabilityCoreRead)

	response, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_admin",
		Capability:         CapabilityHostManage,
		Action:             ControlActionHostTrustRevoke,
		Params: map[string]any{
			"controller_device_id": "device_reader",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(hostTrustRevokeResult)
	if !ok {
		t.Fatalf("trust revoke result = %#v, want hostTrustRevokeResult", response.Result)
	}
	if result.ControllerDeviceID != "device_reader" || result.Grant.Status != TrustStatusRevoked || result.RevokedAt == "" {
		t.Fatalf("trust revoke result = %#v", result)
	}
	if _, ok := app.store.trustedControlGrant("device_reader"); ok {
		t.Fatal("revoked device still has trusted control grant")
	}
	if countKind(app.store.queryEvents("", "", 0), "control.trust.revoked") != 1 {
		t.Fatalf("events = %#v, want one trust revoke audit event", eventKinds(app.store.queryEvents("", "", 0)))
	}
}
