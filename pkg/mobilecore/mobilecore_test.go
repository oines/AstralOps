package mobilecore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/deviceidentity"
)

func TestMobileCoreFacadeDelegatesRemoteActionsToGoController(t *testing.T) {
	transport := &fakeMobileCoreTransport{terminal: newFakeMobileCoreTerminal("term_1")}
	core := New()
	core.controller = controllercore.New(transport)

	if _, err := core.Snapshot("dev_host", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SendInput("dev_host", "session_1", mustJSON(t, map[string]any{"input": "hello"})); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RespondInteraction("dev_host", "approval_1", mustJSON(t, map[string]any{"decision": "accept"})); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ControlRequest("dev_host", "workspace.files.read", "workspace.files.read", mustJSON(t, map[string]any{"workspace_id": "workspace_1", "path": "."})); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ListTerminals("dev_host"); err != nil {
		t.Fatal(err)
	}
	body, err := core.OpenTerminal("dev_host", "workspace_1")
	if err != nil {
		t.Fatal(err)
	}
	var opened map[string]any
	if err := json.Unmarshal([]byte(body), &opened); err != nil {
		t.Fatal(err)
	}
	if opened["terminal_id"] != "term_1" {
		t.Fatalf("opened terminal = %#v, want term_1", opened)
	}
	if _, err := core.TerminalInput("dev_host", "term_1", "pwd\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.TerminalResize("dev_host", "term_1", 100, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := core.TerminalHeartbeatAck("dev_host", "term_1", 3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := core.DetachTerminal("dev_host", "term_1"); err != nil {
		t.Fatal(err)
	}
	if transport.requestCount(controllercore.ActionHostSnapshot) != 1 {
		t.Fatalf("snapshot requests = %d, want 1", transport.requestCount(controllercore.ActionHostSnapshot))
	}
	if transport.requestCount(controllercore.ActionSessionInput) != 1 {
		t.Fatalf("session input requests = %d, want 1", transport.requestCount(controllercore.ActionSessionInput))
	}
	if transport.requestCount(controllercore.ActionInteractionRespond) != 1 {
		t.Fatalf("interaction respond requests = %d, want 1", transport.requestCount(controllercore.ActionInteractionRespond))
	}
	if response := transport.requestForAction(controllercore.ActionInteractionRespond); response.Params["interaction_id"] != "approval_1" {
		t.Fatalf("interaction params = %#v, want approval_1", response.Params)
	}
	if response := transport.requestForAction("workspace.files.read"); response.Params["workspace_id"] != "workspace_1" {
		t.Fatalf("workspace files params = %#v, want workspace_1", response.Params)
	}
	if transport.requestCount("terminal.list") != 1 {
		t.Fatalf("terminal list requests = %d, want 1", transport.requestCount("terminal.list"))
	}
	if transport.terminal.input != "pwd\n" {
		t.Fatalf("terminal input = %q, want pwd", transport.terminal.input)
	}
	if transport.terminal.cols != 100 || transport.terminal.rows != 40 {
		t.Fatalf("terminal resize = %dx%d, want 100x40", transport.terminal.cols, transport.terminal.rows)
	}
	if transport.terminal.heartbeatSeq != 3 || transport.terminal.renderedSeq != 2 {
		t.Fatalf("terminal heartbeat ack = %d/%d, want 3/2", transport.terminal.heartbeatSeq, transport.terminal.renderedSeq)
	}
	if !transport.terminal.detached {
		t.Fatal("terminal was not detached")
	}
}

func TestMobileCoreReplacingTerminalViewerDetachesPreviousStream(t *testing.T) {
	first := newFakeMobileCoreTerminal("term_1")
	second := newFakeMobileCoreTerminal("term_1")
	transport := &fakeMobileCoreTransport{terminals: []*fakeMobileCoreTerminal{first, second}}
	core := New()
	core.controller = controllercore.New(transport)

	if _, err := core.OpenTerminal("dev_host", "workspace_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.AttachTerminal("dev_host", "term_1", 0); err != nil {
		t.Fatal(err)
	}
	if !first.detached {
		t.Fatal("previous terminal viewer was not detached")
	}
	if second.detached {
		t.Fatal("new terminal viewer was detached")
	}
	if _, err := core.TerminalInput("dev_host", "term_1", "x"); err != nil {
		t.Fatal(err)
	}
	if first.input != "" {
		t.Fatalf("old terminal input = %q, want empty", first.input)
	}
	if second.input != "x" {
		t.Fatalf("new terminal input = %q, want x", second.input)
	}
}

func TestMobileCoreControlRequestRejectsUnsupportedAction(t *testing.T) {
	core := New()
	core.controller = controllercore.New(&fakeMobileCoreTransport{terminal: newFakeMobileCoreTerminal("term_1")})

	if _, err := core.ControlRequest("dev_host", "core.read", "workspace.files.read", "{}"); controllercore.ErrorCode(err) != "capability_mismatch" {
		t.Fatalf("capability mismatch error = %v, want capability_mismatch", err)
	}
	if _, err := core.ControlRequest("dev_host", "core.read", "core.control.unknown", "{}"); controllercore.ErrorCode(err) != "control_action_unknown" {
		t.Fatalf("unknown action error = %v, want control_action_unknown", err)
	}
}

func TestStartReturnsPersistentStoredIdentity(t *testing.T) {
	core := New()
	body, err := core.Start(mustJSON(t, map[string]any{"device_name": "iPhone"}))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Started        bool                          `json:"started"`
		Identity       cloudmesh.DeviceIdentity      `json:"identity"`
		StoredIdentity deviceidentity.StoredIdentity `json:"stored_identity"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Started || result.Identity.DeviceID == "" {
		t.Fatalf("start result = %#v, want started identity", result)
	}
	if result.StoredIdentity.DeviceID != result.Identity.DeviceID || strings.TrimSpace(result.StoredIdentity.PrivateKey) == "" {
		t.Fatalf("stored_identity = %#v, want persistent identity for %q", result.StoredIdentity, result.Identity.DeviceID)
	}
	if _, _, err := deviceidentity.ValidateStored(result.StoredIdentity, deviceidentity.MobileControllerCapabilities()); err != nil {
		t.Fatalf("stored_identity invalid: %v", err)
	}

	body, err = core.Start(mustJSON(t, map[string]any{"stored_identity": result.StoredIdentity}))
	if err != nil {
		t.Fatal(err)
	}
	var restarted struct {
		Identity       cloudmesh.DeviceIdentity      `json:"identity"`
		StoredIdentity deviceidentity.StoredIdentity `json:"stored_identity"`
	}
	if err := json.Unmarshal([]byte(body), &restarted); err != nil {
		t.Fatal(err)
	}
	if restarted.Identity.DeviceID != result.Identity.DeviceID || restarted.StoredIdentity.PrivateKey != result.StoredIdentity.PrivateKey {
		t.Fatalf("restarted = %#v, want same persistent identity", restarted)
	}
}

func TestSetCloudSessionRefreshesMeshThroughGoCore(t *testing.T) {
	stored := testMobileStoredIdentity(t)
	identity := stored.DeviceIdentity
	host := cloudmesh.DeviceRecord{
		AccountIDHash:        "acct_test",
		DeviceID:             "dev_host",
		DeviceName:           "Desktop",
		DeviceKind:           deviceidentity.DeviceKindDesktop,
		PublicKey:            "host-public-key",
		PublicKeyFingerprint: "sha256:HOST",
		CanHost:              true,
		CanControl:           true,
		Status:               cloudmesh.DeviceStatusOnline,
		RelayURL:             "http://relay.test",
		UpdatedAt:            "2026-01-01T00:00:00Z",
	}
	server := newMobileCoreCloudServer(t, identity, []cloudmesh.DeviceRecord{host}, []cloudmesh.PairingSignal{{
		RequestID:            "pair_1",
		AccountIDHash:        "acct_test",
		HostDeviceID:         host.DeviceID,
		ControllerDeviceID:   identity.DeviceID,
		Scope:                "full",
		Status:               controllercore.PairingStatusApproved,
		WorkspaceExecPolicy:  "trusted",
		CreatedAt:            "2026-01-01T00:00:00Z",
		UpdatedAt:            "2026-01-01T00:00:00Z",
		HostDeviceName:       host.DeviceName,
		HostDeviceKind:       host.DeviceKind,
		ControllerDeviceName: identity.DeviceName,
	}})
	defer server.Close()

	core := New()
	payload := mustJSON(t, map[string]any{
		"stored_identity": stored,
		"session": map[string]any{
			"base_url":      server.URL,
			"account_token": "token_test",
		},
	})
	body, err := core.SetCloudSession(payload)
	if err != nil {
		t.Fatal(err)
	}
	var state controllercore.MeshState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Self.CloudActive || !state.Self.CanControl || state.Self.CanHost {
		t.Fatalf("self = %#v, want mobile controller cloud active", state.Self)
	}
	if state.Cloud == nil || state.Cloud.RelayURL != "http://relay.test" {
		t.Fatalf("cloud = %#v, want relay config", state.Cloud)
	}
	if len(state.Hosts) != 1 || state.Hosts[0].DeviceID != host.DeviceID {
		t.Fatalf("hosts = %#v, want one host", state.Hosts)
	}
	if state.Hosts[0].AuthorizationState != controllercore.PairingStatusApproved || state.Hosts[0].Connection != controllercore.TransportRelay {
		t.Fatalf("host = %#v, want approved relay host", state.Hosts[0])
	}
}

type fakeMobileCoreTransport struct {
	mu        sync.Mutex
	requests  []controllercore.ControlRequest
	terminal  *fakeMobileCoreTerminal
	terminals []*fakeMobileCoreTerminal
}

func (f *fakeMobileCoreTransport) ControlState(string) controllercore.ControlState {
	return controllercore.ControlState{State: controllercore.StateLive, Transport: controllercore.TransportRelay}
}

func (f *fakeMobileCoreTransport) Request(_ context.Context, _ string, capability, action string, params map[string]any) (controllercore.ControlResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, controllercore.ControlRequest{Capability: capability, Action: action, Params: params})
	f.mu.Unlock()
	return controllercore.ControlResponse{OK: true, Result: map[string]any{"ok": true}}, nil
}

func (f *fakeMobileCoreTransport) SubscribeEvents(context.Context, string, controllercore.EventSubscriptionParams) (controllercore.EventStream, error) {
	ch := make(chan controllercore.EventEnvelope)
	close(ch)
	return controllercore.EventStream{Events: ch, Close: func() {}}, nil
}

func (f *fakeMobileCoreTransport) OpenTerminal(context.Context, string, string, int64) (controllercore.TerminalStream, error) {
	return f.nextTerminal(), nil
}

func (f *fakeMobileCoreTransport) AttachTerminal(context.Context, string, string, int64) (controllercore.TerminalStream, error) {
	return f.nextTerminal(), nil
}

func (f *fakeMobileCoreTransport) Invalidate(string, string) {}

func (f *fakeMobileCoreTransport) nextTerminal() *fakeMobileCoreTerminal {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.terminals) > 0 {
		terminal := f.terminals[0]
		f.terminals = f.terminals[1:]
		return terminal
	}
	return f.terminal
}

func (f *fakeMobileCoreTransport) requestCount(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request.Action == action {
			count++
		}
	}
	return count
}

func (f *fakeMobileCoreTransport) requestForAction(action string) controllercore.ControlRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, request := range f.requests {
		if request.Action == action {
			return request
		}
	}
	return controllercore.ControlRequest{}
}

type fakeMobileCoreTerminal struct {
	terminalID   string
	viewerID     string
	inputLeaseID string
	frames       chan controllercore.TerminalFrame
	input        string
	cols         int
	rows         int
	heartbeatSeq int64
	renderedSeq  int64
	detached     bool
}

var fakeMobileCoreTerminalSeq int64

func newFakeMobileCoreTerminal(terminalID string) *fakeMobileCoreTerminal {
	suffix := strconv.FormatInt(atomic.AddInt64(&fakeMobileCoreTerminalSeq, 1), 10)
	return &fakeMobileCoreTerminal{
		terminalID:   terminalID,
		viewerID:     "viewer_" + terminalID + "_" + suffix,
		inputLeaseID: "lease_" + terminalID + "_" + suffix,
		frames:       make(chan controllercore.TerminalFrame),
	}
}

func (f *fakeMobileCoreTerminal) TerminalID() string { return f.terminalID }
func (f *fakeMobileCoreTerminal) ViewerID() string   { return f.viewerID }
func (f *fakeMobileCoreTerminal) InputLeaseID() string {
	return f.inputLeaseID
}
func (f *fakeMobileCoreTerminal) Shell() string    { return "zsh" }
func (f *fakeMobileCoreTerminal) CWD() string      { return "/" }
func (f *fakeMobileCoreTerminal) OutputSeq() int64 { return 0 }
func (f *fakeMobileCoreTerminal) Frames() <-chan controllercore.TerminalFrame {
	return f.frames
}
func (f *fakeMobileCoreTerminal) Input(data string) error {
	f.input += data
	return nil
}
func (f *fakeMobileCoreTerminal) Resize(cols, rows int) error {
	f.cols = cols
	f.rows = rows
	return nil
}
func (f *fakeMobileCoreTerminal) AckHeartbeat(seq, renderedSeq int64) error {
	f.heartbeatSeq = seq
	f.renderedSeq = renderedSeq
	return nil
}
func (f *fakeMobileCoreTerminal) Close() error { return nil }
func (f *fakeMobileCoreTerminal) Detach() error {
	f.detached = true
	return nil
}

func TestRequestPairingUsesCloudMeshClient(t *testing.T) {
	stored := testMobileStoredIdentity(t)
	identity := stored.DeviceIdentity
	var captured cloudmesh.PairingSignalInput
	server := newMobileCoreCloudServer(t, identity, nil, nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: []cloudmesh.DeviceRecord{{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/pairing/requests":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			writeTestJSON(t, w, cloudmesh.PairingSignalResponse{Request: cloudmesh.PairingSignal{
				RequestID:           "pair_new",
				AccountIDHash:       "acct_test",
				HostDeviceID:        captured.HostDeviceID,
				ControllerDeviceID:  captured.ControllerDeviceID,
				Scope:               captured.Scope,
				Status:              controllercore.PairingStatusPending,
				Capabilities:        captured.Capabilities,
				WorkspaceExecPolicy: captured.WorkspaceExecPolicy,
				CreatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
				UpdatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
			}})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.Path)
		}
	})
	defer server.Close()

	core := New()
	if _, err := core.SetCloudSession(mustJSON(t, map[string]any{
		"stored_identity": stored,
		"session":         map[string]any{"base_url": server.URL, "account_token": "token_test"},
	})); err != nil {
		t.Fatal(err)
	}
	body, err := core.RequestPairing("dev_host")
	if err != nil {
		t.Fatal(err)
	}
	var signal controllercore.PairingSignal
	if err := json.Unmarshal([]byte(body), &signal); err != nil {
		t.Fatal(err)
	}
	if signal.RequestID != "pair_new" || signal.Status != controllercore.PairingStatusPending {
		t.Fatalf("signal = %#v, want pending pair_new", signal)
	}
	if captured.ControllerDeviceID != identity.DeviceID || captured.HostDeviceID != "dev_host" {
		t.Fatalf("captured = %#v, want mobile controller pairing request", captured)
	}
	if !containsCapability(captured.Capabilities, "terminal.input") || !containsCapability(captured.Capabilities, "workspace.exec") {
		t.Fatalf("capabilities = %#v, want mobile controller capabilities", captured.Capabilities)
	}
}

func TestSetCloudSessionCanExchangeLoginCode(t *testing.T) {
	stored := testMobileStoredIdentity(t)
	identity := stored.DeviceIdentity
	var captured cloudmesh.LoginCodeExchangeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login-code/exchange" {
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			writeTestJSON(t, w, cloudmesh.LoginCodeExchangeResponse{
				AccountToken: "token_test",
				Account: cloudmesh.Account{
					AccountIDHash:              "acct_test",
					MembershipSigningPublicKey: "membership_pub",
					Relay:                      &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test", Credential: "relay_secret"},
				},
				Device: &cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline},
			})
			return
		}
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: []cloudmesh.DeviceRecord{{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	core := New()
	body, err := core.SetCloudSession(mustJSON(t, map[string]any{
		"stored_identity": stored,
		"base_url":        server.URL,
		"login_code":      "login_123",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if captured.LoginCode != "login_123" || captured.Device.DeviceID != identity.DeviceID || captured.Device.CanHost || !captured.Device.CanControl {
		t.Fatalf("captured = %#v, want mobile controller login-code exchange", captured)
	}
	var state controllercore.MeshState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Cloud == nil || state.Cloud.AccountIDHash != "acct_test" {
		t.Fatalf("state = %#v, want exchanged cloud state", state)
	}
	sessionBody, err := core.CloudSession()
	if err != nil {
		t.Fatal(err)
	}
	var sessionResult struct {
		OK      bool `json:"ok"`
		Session struct {
			BaseURL      string `json:"base_url"`
			AccountToken string `json:"account_token"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(sessionBody), &sessionResult); err != nil {
		t.Fatal(err)
	}
	if !sessionResult.OK || sessionResult.Session.BaseURL != server.URL || sessionResult.Session.AccountToken != "token_test" {
		t.Fatalf("cloud session = %#v, want exchanged token for keychain persistence", sessionResult)
	}
}

func TestLogoutRemovesCloudDeviceAndResetsMeshIdentity(t *testing.T) {
	stored := testMobileStoredIdentity(t)
	identity := stored.DeviceIdentity
	removeCalled := false
	server := newMobileCoreCloudServer(t, identity, nil, nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: []cloudmesh.DeviceRecord{{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/remove":
			removeCalled = true
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, Status: cloudmesh.DeviceStatusRevoked})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.String())
		}
	})
	defer server.Close()

	core := New()
	if _, err := core.SetCloudSession(mustJSON(t, map[string]any{
		"stored_identity": stored,
		"session":         map[string]any{"base_url": server.URL, "account_token": "token_test"},
	})); err != nil {
		t.Fatal(err)
	}
	body, err := core.Logout()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		CloudRemoved   bool                          `json:"cloud_removed"`
		MeshReset      bool                          `json:"mesh_reset"`
		Identity       cloudmesh.DeviceIdentity      `json:"identity"`
		StoredIdentity deviceidentity.StoredIdentity `json:"stored_identity"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if !removeCalled || !result.CloudRemoved || !result.MeshReset {
		t.Fatalf("logout result = %#v removeCalled=%v, want cloud removed mesh reset", result, removeCalled)
	}
	if result.Identity.DeviceID == "" || result.Identity.DeviceID == identity.DeviceID {
		t.Fatalf("identity = %#v, want fresh mobile identity", result.Identity)
	}
	if result.StoredIdentity.DeviceID != result.Identity.DeviceID || strings.TrimSpace(result.StoredIdentity.PrivateKey) == "" {
		t.Fatalf("stored_identity = %#v, want fresh persistent identity", result.StoredIdentity)
	}
	if _, _, err := deviceidentity.ValidateStored(result.StoredIdentity, deviceidentity.MobileControllerCapabilities()); err != nil {
		t.Fatalf("stored_identity invalid: %v", err)
	}
	if _, err := core.RefreshMesh(); controllercore.ErrorCode(err) != "cloud_session_missing" {
		t.Fatalf("RefreshMesh error = %v, want cloud_session_missing", err)
	}
}

func newMobileCoreCloudServer(t *testing.T, identity cloudmesh.DeviceIdentity, hosts []cloudmesh.DeviceRecord, signals []cloudmesh.PairingSignal) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeTestJSON(t, w, cloudmesh.Account{AccountIDHash: "acct_test", Relay: &cloudmesh.RelayConfig{RelayID: "relay", RelayURL: "http://relay.test", CredentialExpiresAt: "2026-01-01T01:00:00Z"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/"+cloudmesh.PathEscape(identity.DeviceID)+"/heartbeat":
			writeTestJSON(t, w, cloudmesh.DeviceRecord{AccountIDHash: "acct_test", DeviceID: identity.DeviceID, DeviceName: identity.DeviceName, DeviceKind: identity.DeviceKind, PublicKey: identity.PublicKey, PublicKeyFingerprint: identity.PublicKeyFingerprint, CanControl: true, Status: cloudmesh.DeviceStatusOnline})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			devices := append([]cloudmesh.DeviceRecord{{
				AccountIDHash:        "acct_test",
				DeviceID:             identity.DeviceID,
				DeviceName:           identity.DeviceName,
				DeviceKind:           identity.DeviceKind,
				PublicKey:            identity.PublicKey,
				PublicKeyFingerprint: identity.PublicKeyFingerprint,
				CanControl:           true,
				Status:               cloudmesh.DeviceStatusOnline,
			}}, hosts...)
			writeTestJSON(t, w, cloudmesh.DeviceListResponse{Devices: devices})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/pairing/requests":
			if got := r.URL.Query().Get("device_id"); got != identity.DeviceID {
				t.Fatalf("device_id query = %q, want %q", got, identity.DeviceID)
			}
			writeTestJSON(t, w, cloudmesh.PairingSignalListResponse{Requests: signals})
		default:
			t.Fatalf("unexpected cloud request %s %s", r.Method, r.URL.String())
		}
	}))
}

func testMobileIdentity(t *testing.T) cloudmesh.DeviceIdentity {
	t.Helper()
	return testMobileStoredIdentity(t).DeviceIdentity
}

func testMobileStoredIdentity(t *testing.T) deviceidentity.StoredIdentity {
	t.Helper()
	stored, _, err := deviceidentity.NewStored(deviceidentity.Options{
		DeviceKind:   deviceidentity.DeviceKindMobile,
		DeviceName:   "Phone",
		Capabilities: deviceidentity.MobileControllerCapabilities(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func containsCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == want {
			return true
		}
	}
	return false
}
