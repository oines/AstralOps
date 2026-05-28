package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	plain, _ := readEncryptedControlFrameWithBody(t, client, cipher)
	return plain
}

func readEncryptedControlFrameWithBody(t *testing.T, client *websocket.Conn, cipher *controlCipher) (controlPlainFrame, []byte) {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer client.SetReadDeadline(time.Time{})
	_, body, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var sealed controlSealedFrame
	if err := json.Unmarshal(body, &sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return plain, body
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

func TestControlWebSocketMediaReadResponseIsEncrypted(t *testing.T) {
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityMediaRead)
	media := addControlMediaFixture(t, app, workspace, session, []byte("sealed-media-secret"))
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	body := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_read",
			Capability: CapabilityMediaRead,
			Action:     ControlActionMediaRead,
			Params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq,
				"media_id":   media.mediaID,
			},
		},
	})
	if strings.Contains(string(body), media.path) || strings.Contains(string(body), "sealed-media-secret") {
		t.Fatalf("sealed media request leaked payload: %s", string(body))
	}

	plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealed), media.path) || strings.Contains(string(sealed), "sealed-media-secret") {
		t.Fatalf("sealed media response leaked payload: %s", string(sealed))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok media response", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["name"]) != "clip.png" || stringValue(result["mime_type"]) != "image/png" {
		t.Fatalf("media response metadata = %#v", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(stringValue(result["content_base64"]))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "sealed-media-secret" {
		t.Fatalf("media response body = %q, want fixture body", string(decoded))
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), media.path) {
		t.Fatalf("decrypted media response leaked Host path: %s", string(wire))
	}
}

func TestControlWebSocketMediaStreamChunksAreEncrypted(t *testing.T) {
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityMediaStream)
	secret := []byte("sealed-media-stream-secret")
	media := addControlMediaFixture(t, app, workspace, session, secret)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_stream",
			Capability: CapabilityMediaStream,
			Action:     ControlActionMediaStream,
			Params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq,
				"media_id":   media.mediaID,
				"chunk_size": 7,
			},
		},
	})

	plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealed), string(secret)) {
		t.Fatalf("sealed media stream response leaked payload: %s", string(sealed))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok media stream response", plain)
	}
	result := mapValue(plain.Response.Result)
	streamID := stringValue(result["stream_id"])
	if streamID == "" || stringValue(result["content_base64"]) != "" {
		t.Fatalf("stream result = %#v, want stream metadata without content", result)
	}

	var streamed []byte
	for {
		frame, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
		if strings.Contains(string(sealed), string(secret)) {
			t.Fatalf("sealed media stream frame leaked payload: %s", string(sealed))
		}
		if frame.Media == nil || frame.Media.StreamID != streamID {
			t.Fatalf("stream frame = %#v, want media frame for stream %q", frame, streamID)
		}
		switch frame.Type {
		case mediaStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.Media.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			streamed = append(streamed, body...)
		case mediaStreamFrameComplete:
			if !frame.Media.Final {
				t.Fatalf("completion frame = %#v, want final", frame.Media)
			}
			if string(streamed) != string(secret) {
				t.Fatalf("streamed media = %q, want %q", string(streamed), string(secret))
			}
			return
		default:
			t.Fatalf("stream frame type = %q", frame.Type)
		}
	}
}

func TestControlWebSocketMediaStreamResumesFromOffset(t *testing.T) {
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityMediaStream)
	secret := []byte("0123456789")
	media := addControlMediaFixture(t, app, workspace, session, secret)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_stream_resume",
			Capability: CapabilityMediaStream,
			Action:     ControlActionMediaStream,
			Params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq,
				"media_id":   media.mediaID,
				"offset":     4,
				"chunk_size": 3,
			},
		},
	})

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("stream response = %#v, want ok", plain)
	}
	streamID := stringValue(mapValue(plain.Response.Result)["stream_id"])
	var streamed []byte
	for {
		frame := readEncryptedControlFrame(t, client, cipher)
		if frame.Media == nil || frame.Media.StreamID != streamID {
			t.Fatalf("stream frame = %#v, want media frame for stream %q", frame, streamID)
		}
		switch frame.Type {
		case mediaStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.Media.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			streamed = append(streamed, body...)
		case mediaStreamFrameComplete:
			if string(streamed) != "456789" {
				t.Fatalf("resumed stream = %q, want 456789", string(streamed))
			}
			return
		default:
			t.Fatalf("stream frame type = %q", frame.Type)
		}
	}
}

func TestControlWebSocketChunkedAttachmentIngestIsEncrypted(t *testing.T) {
	app, _, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityAttachmentIngest)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "attachment_start",
			Capability: CapabilityAttachmentIngest,
			Action:     ControlActionAttachmentIngestStart,
			Params: map[string]any{
				"session_id": session.ID,
				"name":       "upload.txt",
			},
		},
	})
	startPlain := readEncryptedControlFrame(t, client, cipher)
	if startPlain.Response == nil || !startPlain.Response.OK {
		t.Fatalf("start response = %#v, want ok", startPlain)
	}
	uploadID := stringValue(mapValue(startPlain.Response.Result)["upload_id"])
	if uploadID == "" {
		t.Fatalf("start result = %#v, want upload id", startPlain.Response.Result)
	}

	chunk := []byte("chunk-upload-secret")
	encoded := base64.StdEncoding.EncodeToString(chunk)
	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "attachment_chunk",
			Capability: CapabilityAttachmentIngest,
			Action:     ControlActionAttachmentIngestChunk,
			Params: map[string]any{
				"session_id":  session.ID,
				"upload_id":   uploadID,
				"seq":         1,
				"offset":      0,
				"data_base64": encoded,
			},
		},
	})
	if strings.Contains(string(sealedRequest), string(chunk)) || strings.Contains(string(sealedRequest), encoded) {
		t.Fatalf("sealed attachment chunk request leaked payload: %s", string(sealedRequest))
	}
	chunkPlain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), string(chunk)) || strings.Contains(string(sealedResponse), encoded) {
		t.Fatalf("sealed attachment chunk response leaked payload: %s", string(sealedResponse))
	}
	if chunkPlain.Response == nil || !chunkPlain.Response.OK {
		t.Fatalf("chunk response = %#v, want ok", chunkPlain)
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "attachment_finish",
			Capability: CapabilityAttachmentIngest,
			Action:     ControlActionAttachmentIngestFinish,
			Params: map[string]any{
				"session_id": session.ID,
				"upload_id":  uploadID,
			},
		},
	})
	finishPlain := readEncryptedControlFrame(t, client, cipher)
	if finishPlain.Response == nil || !finishPlain.Response.OK {
		t.Fatalf("finish response = %#v, want ok", finishPlain)
	}
	attachment := mapValue(mapValue(finishPlain.Response.Result)["attachment"])
	if stringValue(attachment["id"]) == "" || !boolValue(attachment["host_owned"]) || stringValue(attachment["path"]) != "" {
		t.Fatalf("finish attachment = %#v", attachment)
	}
}

func TestControlWebSocketWorkspaceFileReadResponseIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesRead)
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "secret.txt"), []byte("sealed-workspace-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_read",
			Capability: CapabilityWorkspaceFilesRead,
			Action:     ControlActionWorkspaceFilesRead,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "secret.txt",
			},
		},
	})

	plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealed), "sealed-workspace-secret") {
		t.Fatalf("sealed workspace file response leaked payload: %s", string(sealed))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok workspace file response", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["path"]) != "secret.txt" || stringValue(result["kind"]) != "file" {
		t.Fatalf("workspace file response metadata = %#v", result)
	}
	decoded, err := base64.StdEncoding.DecodeString(stringValue(result["content_base64"]))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "sealed-workspace-secret" {
		t.Fatalf("workspace file response body = %q, want fixture body", string(decoded))
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

func TestControlWebSocketTerminalAttachStreamsOutputOverEncryptedChannel(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityTerminalOpen, CapabilityTerminalInput)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_open",
			Capability: CapabilityTerminalOpen,
			Action:     ControlActionTerminalOpen,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"cols":         80,
				"rows":         24,
			},
		},
	})
	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("open response = %#v, want ok", plain)
	}
	terminalID := stringValue(mapValue(plain.Response.Result)["terminal_id"])
	if terminalID == "" {
		t.Fatalf("open result = %#v, want terminal id", plain.Response.Result)
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_attach",
			Capability: CapabilityTerminalOpen,
			Action:     ControlActionTerminalAttach,
			Params:     map[string]any{"terminal_id": terminalID},
		},
	})
	plain = readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("attach response = %#v, want ok", plain)
	}

	secret := "stream-secret-" + randomID(8)
	sealedInput := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_input",
			Capability: CapabilityTerminalInput,
			Action:     ControlActionTerminalInput,
			Params: map[string]any{
				"terminal_id": terminalID,
				"data":        "printf '%s\\n' " + shellSingleQuote(secret) + "\n",
			},
		},
	})
	if strings.Contains(string(sealedInput), secret) {
		t.Fatalf("sealed terminal input leaked payload: %s", string(sealedInput))
	}

	sawInputResponse := false
	sawOutput := false
	for i := 0; i < 20 && (!sawInputResponse || !sawOutput); i++ {
		plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
		if strings.Contains(string(sealed), secret) {
			t.Fatalf("sealed terminal stream leaked payload: %s", string(sealed))
		}
		switch plain.Type {
		case "response":
			if plain.Response != nil && plain.Response.RequestID == "terminal_input" && plain.Response.OK {
				sawInputResponse = true
			}
		case terminalFrameOutput:
			if plain.Terminal == nil || plain.Terminal.TerminalID != terminalID {
				t.Fatalf("terminal output frame = %#v, want terminal %s", plain, terminalID)
			}
			if strings.Contains(plain.Terminal.Data, secret) {
				sawOutput = true
			}
		}
	}
	if !sawInputResponse || !sawOutput {
		t.Fatalf("saw input response=%v output=%v, want both", sawInputResponse, sawOutput)
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_close",
			Capability: CapabilityTerminalInput,
			Action:     ControlActionTerminalClose,
			Params:     map[string]any{"terminal_id": terminalID},
		},
	})
	sawCloseResponse := false
	sawClosedFrame := false
	for i := 0; i < 20 && (!sawCloseResponse || !sawClosedFrame); i++ {
		plain := readEncryptedControlFrame(t, client, cipher)
		switch plain.Type {
		case "response":
			if plain.Response != nil && plain.Response.RequestID == "terminal_close" && plain.Response.OK {
				sawCloseResponse = true
			}
		case terminalFrameClosed:
			if plain.Terminal != nil && plain.Terminal.TerminalID == terminalID && plain.Terminal.Status == terminalStatusClosed {
				sawClosedFrame = true
			}
		}
	}
	if !sawCloseResponse || !sawClosedFrame {
		t.Fatalf("saw close response=%v closed frame=%v, want both", sawCloseResponse, sawClosedFrame)
	}

	events := app.store.queryEvents(workspace.ID, "", 0)
	body, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("terminal output leaked into JSONL events: %s", string(body))
	}
	if countKind(events, "control.terminal.opened") != 1 || countKind(events, "control.terminal.attached") != 1 || countKind(events, "control.terminal.closed") != 1 {
		t.Fatalf("terminal lifecycle events = %#v", eventKinds(events))
	}
}

func TestControlWebSocketTerminalReconnectAttachWithinRetention(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityTerminalOpen, CapabilityTerminalInput)
	app.terminalManager().retentionTimeout = 500 * time.Millisecond
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_open",
			Capability: CapabilityTerminalOpen,
			Action:     ControlActionTerminalOpen,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"cols":         80,
				"rows":         24,
			},
		},
	})
	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("open response = %#v, want ok", plain)
	}
	terminalID := stringValue(mapValue(plain.Response.Result)["terminal_id"])
	if terminalID == "" {
		t.Fatalf("open result = %#v, want terminal id", plain.Response.Result)
	}
	t.Cleanup(func() {
		_, _ = app.terminalManager().close(context.Background(), "dev_controller", terminalCloseParams{TerminalID: terminalID})
	})

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_attach",
			Capability: CapabilityTerminalOpen,
			Action:     ControlActionTerminalAttach,
			Params:     map[string]any{"terminal_id": terminalID},
		},
	})
	plain = readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("attach response = %#v, want ok", plain)
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{Type: "close"})
	_ = client.Close()
	waitForEventKindCount(t, app, workspace.ID, "control.terminal.detached", 1)

	reconnected, reconnectedCipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer reconnected.Close()
	writeEncryptedControlFrame(t, reconnected, reconnectedCipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_reattach",
			Capability: CapabilityTerminalOpen,
			Action:     ControlActionTerminalAttach,
			Params:     map[string]any{"terminal_id": terminalID},
		},
	})
	plain = readEncryptedControlFrame(t, reconnected, reconnectedCipher)
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("reattach response = %#v, want ok", plain)
	}

	secret := "reattach-secret-" + randomID(8)
	writeEncryptedControlFrame(t, reconnected, reconnectedCipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "terminal_input_after_reattach",
			Capability: CapabilityTerminalInput,
			Action:     ControlActionTerminalInput,
			Params: map[string]any{
				"terminal_id": terminalID,
				"data":        "printf '%s\\n' " + shellSingleQuote(secret) + "\n",
			},
		},
	})
	sawOutput := false
	for i := 0; i < 20 && !sawOutput; i++ {
		plain := readEncryptedControlFrame(t, reconnected, reconnectedCipher)
		if plain.Type == terminalFrameOutput && plain.Terminal != nil && strings.Contains(plain.Terminal.Data, secret) {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Fatal("reattached terminal did not stream output")
	}
}

func TestTerminalRetentionTimeoutClosesUnattachedSession(t *testing.T) {
	t.Setenv("SHELL", terminalManagerTestShell(t))

	app, workspace, _ := newControlGatewayTestApp(t, AgentCodex, &recordingRuntime{})
	manager := app.terminalManager()
	manager.retentionTimeout = 50 * time.Millisecond
	trustControlDevice(t, app, "device_mobile", CapabilityTerminalOpen, CapabilityTerminalInput)

	open := openTerminalForTest(t, app, "device_mobile", workspace.ID)
	waitForTerminalClosedReason(t, app, workspace.ID, open.TerminalID, "retention_timeout")

	_, err := app.executeControlRequest(ControlRequest{
		ControllerDeviceID: "device_mobile",
		Capability:         CapabilityTerminalInput,
		Action:             ControlActionTerminalInput,
		Params: map[string]any{
			"terminal_id": open.TerminalID,
			"data":        "echo too-late\n",
		},
	})
	assertActionError(t, err, http.StatusGone, "terminal_closed")
}

func waitForEventKindCount(t *testing.T, app *app, workspaceID, kind string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := countKind(app.store.queryEvents(workspaceID, "", 0), kind); got >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s count did not reach %d; events = %#v", kind, want, eventKinds(app.store.queryEvents(workspaceID, "", 0)))
}

func waitForTerminalClosedReason(t *testing.T, app *app, workspaceID, terminalID, reason string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range app.store.queryEvents(workspaceID, "", 0) {
			if event.Kind != "control.terminal.closed" {
				continue
			}
			normalized := mapValue(event.Normalized)
			if stringValue(normalized["terminal_id"]) == terminalID && stringValue(normalized["reason"]) == reason {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("terminal %s did not close with reason %s; events = %#v", terminalID, reason, app.store.queryEvents(workspaceID, "", 0))
}
