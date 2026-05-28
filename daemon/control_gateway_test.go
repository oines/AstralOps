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
	if len(runtime.inputs) != 1 || runtime.inputs[0] != "implement gateway" {
		t.Fatalf("runtime inputs = %#v, want gateway input", runtime.inputs)
	}
	if runtime.options[0].Model != "gpt-test" || runtime.options[0].ReasoningEffort != "low" || runtime.options[0].PermissionMode != "auto" {
		t.Fatalf("runtime options = %#v", runtime.options[0])
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
