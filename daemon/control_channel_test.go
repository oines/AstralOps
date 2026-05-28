package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func newControlChannelTestApp(t *testing.T, capabilities ...string) (*app, Workspace, Session, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{Name: "Control", Target: "local", Agent: AgentCodex, LocalCWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	session := st.createSession(workspace, AgentCodex)
	controllerPublicKey, controllerPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "dev_controller",
		ControllerPublicKey: base64.StdEncoding.EncodeToString(controllerPublicKey),
		Capabilities:        capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{
		store:    st,
		hub:      newEventHub(),
		runtimes: map[AgentKind]AgentRuntime{AgentCodex: &recordingRuntime{}},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	return app, workspace, session, controllerPublicKey, controllerPrivateKey
}

func startControlChannelTestServer(t *testing.T, app *app) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(app.handleControlWS))
	t.Cleanup(server.Close)
	return server
}

func dialControlChannel(t *testing.T, serverURL string, app *app, controllerPublicKey ed25519.PublicKey, controllerPrivateKey ed25519.PrivateKey) (*websocket.Conn, *controlCipher, controlHelloAckFrame) {
	t.Helper()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(serverURL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}

	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		t.Fatal(err)
	}
	hello := controlHelloFrame{
		Type:                   "hello",
		Version:                controlProtocolVersion,
		ControllerDeviceID:     "dev_controller",
		ControllerPublicKey:    base64.StdEncoding.EncodeToString(controllerPublicKey),
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(controllerPrivateKey, controlClientSignaturePayload(app.store.deviceIdentity.DeviceID, hello)))
	if err := client.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}

	var ack controlHelloAckFrame
	if err := client.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != "hello_ack" || ack.Version != controlProtocolVersion || ack.ConnectionID == "" {
		t.Fatalf("ack = %#v, want hello_ack", ack)
	}
	hostPublicKey, err := decodeDevicePublicKey(ack.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(ack.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(hostPublicKey, controlHostSignaturePayload(hello, ack), signature) {
		t.Fatal("host hello_ack signature did not verify")
	}
	hostEphemeralBytes, err := base64.StdEncoding.DecodeString(ack.HostEphemeralKey)
	if err != nil {
		t.Fatal(err)
	}
	hostEphemeral, err := curve.NewPublicKey(hostEphemeralBytes)
	if err != nil {
		t.Fatal(err)
	}
	sharedSecret, err := controllerEphemeral.ECDH(hostEphemeral)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := newControlCipher(deriveControlSessionKey(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID))
	if err != nil {
		t.Fatal(err)
	}
	return client, cipher, ack
}

func writeEncryptedControlFrame(t *testing.T, client *websocket.Conn, cipher *controlCipher, plain controlPlainFrame) []byte {
	t.Helper()
	sealed, err := cipher.seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatal(err)
	}
	return body
}

func readEncryptedControlFrame(t *testing.T, client *websocket.Conn, cipher *controlCipher) controlPlainFrame {
	t.Helper()
	var sealed controlSealedFrame
	if err := client.ReadJSON(&sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestControlWebSocketEncryptedRequestResponse(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreRead)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	body := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "req_workspaces",
			Capability: CapabilityCoreRead,
			Action:     ControlActionWorkspaces,
		},
	})
	if strings.Contains(string(body), ControlActionWorkspaces) || strings.Contains(string(body), CapabilityCoreRead) {
		t.Fatalf("sealed frame leaked request payload: %s", string(body))
	}

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK || plain.Response.RequestID != "req_workspaces" {
		t.Fatalf("plain response = %#v, want ok response", plain)
	}
}

func TestControlWebSocketRejectsControllerDeviceMismatch(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreRead)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:          "req_spoof",
			ControllerDeviceID: "dev_other",
			Capability:         CapabilityCoreRead,
			Action:             ControlActionWorkspaces,
		},
	})
	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || plain.Response.OK || plain.Response.Error == nil || plain.Response.Error.Code != "controller_device_mismatch" {
		t.Fatalf("plain response = %#v, want controller_device_mismatch", plain)
	}
}

func TestControlWebSocketRejectsUntrustedController(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{
		store:    st,
		hub:      newEventHub(),
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	server := startControlChannelTestServer(t, app)
	controllerPublicKey, controllerPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		t.Fatal(err)
	}
	hello := controlHelloFrame{
		Type:                   "hello",
		Version:                controlProtocolVersion,
		ControllerDeviceID:     "dev_untrusted",
		ControllerPublicKey:    base64.StdEncoding.EncodeToString(controllerPublicKey),
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(controllerPrivateKey, controlClientSignaturePayload(app.store.deviceIdentity.DeviceID, hello)))
	if err := client.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var closeFrame controlPlainFrame
	if err := client.ReadJSON(&closeFrame); err != nil {
		t.Fatal(err)
	}
	if closeFrame.Type != "close" || closeFrame.Code != "capability_denied" {
		t.Fatalf("close frame = %#v, want capability_denied", closeFrame)
	}
}

func TestControlWebSocketRevokeClosesActiveSession(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreRead)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	if got := app.activeControlSessionCountForDevice("dev_controller"); got != 1 {
		t.Fatalf("active sessions = %d, want 1", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/trust/devices/dev_controller/revoke", nil)
	rr := httptest.NewRecorder()
	app.handleTrustDeviceAction(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", rr.Code, rr.Body.String())
	}
	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "close" || plain.Code != "trust_revoked" {
		t.Fatalf("close frame = %#v, want trust_revoked", plain)
	}
	if got := app.activeControlSessionCountForDevice("dev_controller"); got != 0 {
		t.Fatalf("active sessions = %d, want 0 after revoke", got)
	}
}
