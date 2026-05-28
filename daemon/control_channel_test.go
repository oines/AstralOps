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
	"runtime"
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

func controlSessionForAck(t *testing.T, app *app, ack controlHelloAckFrame) *controlWSConn {
	t.Helper()
	app.controlMu.Lock()
	defer app.controlMu.Unlock()
	conn := app.controlSessions[ack.ConnectionID]
	if conn == nil {
		t.Fatalf("control session %q was not registered", ack.ConnectionID)
	}
	return conn
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

func TestControlWebSocketMediaStreamCancelIsEncrypted(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityMediaStream)
	server := startControlChannelTestServer(t, app)
	client, cipher, ack := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	streamID := "media_stream_secret_id"
	ctx, cancel := context.WithCancel(context.Background())
	controlSessionForAck(t, app, ack).registerMediaStream(streamID, cancel)

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_stream_cancel",
			Capability: CapabilityMediaStream,
			Action:     ControlActionMediaStreamCancel,
			Params:     map[string]any{"stream_id": streamID},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionMediaStreamCancel) || strings.Contains(string(sealedRequest), streamID) {
		t.Fatalf("sealed media stream cancel request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), streamID) {
		t.Fatalf("sealed media stream cancel response leaked payload: %s", string(sealedResponse))
	}
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("cancel response = %#v, want ok", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["stream_id"]) != streamID || !boolValue(result["cancelled"]) {
		t.Fatalf("cancel result = %#v, want cancelled stream", result)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("media stream cancel did not cancel registered context")
	}
}

func TestControlWebSocketMediaStreamResumesAcrossControlReconnect(t *testing.T) {
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityMediaStream)
	secret := []byte("0123456789abcdef")
	media := addControlMediaFixture(t, app, workspace, session, secret)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_stream_initial",
			Capability: CapabilityMediaStream,
			Action:     ControlActionMediaStream,
			Params: map[string]any{
				"session_id": session.ID,
				"event_seq":  media.eventSeq,
				"media_id":   media.mediaID,
				"chunk_size": 4,
			},
		},
	})

	plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealed), string(secret)) || strings.Contains(string(sealed), media.path) {
		t.Fatalf("sealed media stream response leaked payload: %s", string(sealed))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("initial stream response = %#v, want ok", plain)
	}
	result := mapValue(plain.Response.Result)
	streamID := stringValue(result["stream_id"])
	resumeToken := stringValue(result["resume_token"])
	if streamID == "" || resumeToken == "" {
		t.Fatalf("stream result = %#v, want stream_id and resume_token", result)
	}
	if strings.Contains(resumeToken, media.path) {
		t.Fatalf("resume token leaked Host path: %s", resumeToken)
	}

	firstChunk := readEncryptedControlFrame(t, client, cipher)
	if firstChunk.Type != mediaStreamFrameChunk || firstChunk.Media == nil || firstChunk.Media.StreamID != streamID {
		t.Fatalf("first stream frame = %#v, want chunk for stream %q", firstChunk, streamID)
	}
	if firstChunk.Media.ResumeToken != resumeToken {
		t.Fatalf("first chunk resume token = %q, want %q", firstChunk.Media.ResumeToken, resumeToken)
	}
	body, err := base64.StdEncoding.DecodeString(firstChunk.Media.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "0123" {
		t.Fatalf("first chunk body = %q, want 0123", string(body))
	}
	nextOffset := firstChunk.Media.Offset + int64(len(body))
	_ = client.Close()

	nextClient, nextCipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer nextClient.Close()
	writeEncryptedControlFrame(t, nextClient, nextCipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "media_stream_reconnect",
			Capability: CapabilityMediaStream,
			Action:     ControlActionMediaStream,
			Params: map[string]any{
				"resume_token": resumeToken,
				"offset":       nextOffset,
				"chunk_size":   5,
			},
		},
	})

	reconnectResponse := readEncryptedControlFrame(t, nextClient, nextCipher)
	if reconnectResponse.Type != "response" || reconnectResponse.Response == nil || !reconnectResponse.Response.OK {
		t.Fatalf("reconnect stream response = %#v, want ok", reconnectResponse)
	}
	reconnectResult := mapValue(reconnectResponse.Response.Result)
	reconnectStreamID := stringValue(reconnectResult["stream_id"])
	if reconnectStreamID == "" {
		t.Fatalf("reconnect stream id = %q, want non-empty stream id", reconnectStreamID)
	}
	if got := stringValue(reconnectResult["resume_token"]); got != resumeToken {
		t.Fatalf("reconnect resume token = %q, want %q", got, resumeToken)
	}

	var streamed []byte
	for {
		frame := readEncryptedControlFrame(t, nextClient, nextCipher)
		if frame.Media == nil || frame.Media.StreamID != reconnectStreamID {
			t.Fatalf("reconnect stream frame = %#v, want media frame for stream %q", frame, reconnectStreamID)
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
			if string(streamed) != "456789abcdef" {
				t.Fatalf("resumed stream after reconnect = %q, want 456789abcdef", string(streamed))
			}
			return
		default:
			t.Fatalf("reconnect stream frame type = %q", frame.Type)
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

func TestControlWebSocketSessionForkOverEncryptedChannel(t *testing.T) {
	runtime := &recordingForkRuntime{}
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreControl)
	app.runtimes[AgentCodex] = runtime
	session.NativeThreadID = "source-thread"
	app.store.mu.Lock()
	app.store.sessions[session.ID] = session
	app.store.mu.Unlock()

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

	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "session_fork",
			Capability: CapabilityCoreControl,
			Action:     ControlActionSessionFork,
			Params: map[string]any{
				"session_id": session.ID,
				"event_seq":  targetSeq,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionSessionFork) || strings.Contains(string(sealedRequest), session.ID) {
		t.Fatalf("sealed session fork request leaked payload: %s", string(sealedRequest))
	}

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK || plain.Response.RequestID != "session_fork" {
		t.Fatalf("fork response = %#v, want ok response", plain)
	}
	fork := mapValue(mapValue(plain.Response.Result)["session"])
	forkID := stringValue(fork["id"])
	if forkID == "" || stringValue(fork["forked_from_session_id"]) != session.ID || int64(numberValue(fork["forked_from_event_seq"])) != targetSeq || stringValue(fork["forked_from_native_anchor"]) != "turn-1" {
		t.Fatalf("fork session = %#v", fork)
	}
	if runtime.source.ID != session.ID || runtime.fork.ID != forkID || runtime.workspace.ID != workspace.ID {
		t.Fatalf("fork runtime call = source %#v fork %#v workspace %#v", runtime.source, runtime.fork, runtime.workspace)
	}
	if runtime.rollbackTurns != 1 {
		t.Fatalf("rollbackTurns = %d, want 1", runtime.rollbackTurns)
	}
	if !containsEventKind(app.store.queryEvents("", forkID, 0), "session.started") {
		t.Fatalf("fork events = %#v, want session.started", eventKinds(app.store.queryEvents("", forkID, 0)))
	}
}

func TestControlWebSocketSessionDeleteOverEncryptedChannel(t *testing.T) {
	app, _, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreRead, CapabilityCoreControl)
	turn := app.enqueueTurn(session, "queued prompt", TurnOptions{})
	runtime := app.runtimes[AgentCodex].(*recordingRuntime)

	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "session_delete",
			Capability: CapabilityCoreControl,
			Action:     ControlActionSessionDelete,
			Params:     map[string]any{"session_id": session.ID},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionSessionDelete) || strings.Contains(string(sealedRequest), session.ID) {
		t.Fatalf("sealed session delete request leaked payload: %s", string(sealedRequest))
	}

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK || plain.Response.RequestID != "session_delete" {
		t.Fatalf("delete response = %#v, want ok response", plain)
	}
	result := mapValue(plain.Response.Result)
	if !boolValue(result["ok"]) || stringValue(result["session_id"]) != session.ID {
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
	if !containsEventKind(app.store.queryEvents("", session.ID, 0), "session.deleted") {
		t.Fatalf("events = %#v, want session.deleted", eventKinds(app.store.queryEvents("", session.ID, 0)))
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "session_view_after_delete",
			Capability: CapabilityCoreRead,
			Action:     ControlActionSessionView,
			Params:     map[string]any{"session_id": session.ID},
		},
	})
	plain = readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || plain.Response.OK || plain.Response.Error == nil || plain.Response.Error.Code != "session_not_found" {
		t.Fatalf("view after delete response = %#v, want session_not_found", plain)
	}
}

func TestControlWebSocketEventSubscriptionStreamsEncryptedEvents(t *testing.T) {
	app, workspace, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityCoreRead)
	secret := "sealed-event-subscription-secret"
	saved, err := app.store.appendEvent(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "message.user", Normalized: map[string]any{"text": secret}})
	if err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "event_subscription",
			Capability: CapabilityCoreRead,
			Action:     ControlActionEventsSubscribe,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"session_id":   session.ID,
				"replay_limit": 1,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionEventsSubscribe) || strings.Contains(string(sealedRequest), session.ID) {
		t.Fatalf("sealed event subscription request leaked payload: %s", string(sealedRequest))
	}

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK || plain.Response.RequestID != "event_subscription" {
		t.Fatalf("subscribe response = %#v, want ok response", plain)
	}
	streamID := stringValue(mapValue(plain.Response.Result)["stream_id"])
	if streamID == "" {
		t.Fatalf("subscribe result = %#v, want stream id", plain.Response.Result)
	}

	plain, sealedEvent := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedEvent), secret) {
		t.Fatalf("sealed event frame leaked payload: %s", string(sealedEvent))
	}
	if plain.Type != eventStreamFrameEvent || plain.Event == nil || plain.Event.StreamID != streamID || plain.Event.Seq != saved.Seq {
		t.Fatalf("event frame = %#v, want replayed event frame", plain)
	}
	text := stringValue(mapValue(plain.Event.Event.Normalized)["text"])
	if plain.Event.Event.Kind != "message.user" || text != secret {
		t.Fatalf("event payload = %#v, want decrypted event", plain.Event.Event)
	}

	liveSecret := "sealed-event-subscription-live-secret"
	app.emit(AstralEvent{WorkspaceID: workspace.ID, SessionID: session.ID, Agent: session.Agent, Kind: "control.status", Normalized: map[string]any{"status": "running", "message": liveSecret}})
	plain, sealedEvent = readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedEvent), liveSecret) {
		t.Fatalf("sealed live event frame leaked payload: %s", string(sealedEvent))
	}
	if plain.Type != eventStreamFrameEvent || plain.Event == nil || plain.Event.StreamID != streamID || plain.Event.Event.Kind != "control.status" {
		t.Fatalf("live event frame = %#v, want control.status event frame", plain)
	}
	if stringValue(mapValue(plain.Event.Event.Normalized)["message"]) != liveSecret {
		t.Fatalf("live event payload = %#v, want decrypted live event", plain.Event.Event)
	}

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "event_unsubscribe",
			Capability: CapabilityCoreRead,
			Action:     ControlActionEventsUnsubscribe,
			Params:     map[string]any{"stream_id": streamID},
		},
	})
	plain = readEncryptedControlFrame(t, client, cipher)
	if plain.Response == nil || !plain.Response.OK || !boolValue(mapValue(plain.Response.Result)["cancelled"]) {
		t.Fatalf("unsubscribe response = %#v, want cancelled", plain)
	}
}

func TestControlWebSocketAttachmentIngestRequestResponseIsEncrypted(t *testing.T) {
	app, _, session, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityAttachmentIngest)
	secret := []byte("sealed-single-attachment-secret")
	encoded := base64.StdEncoding.EncodeToString(secret)
	name := "sealed-single-attachment.txt"
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "attachment_ingest",
			Capability: CapabilityAttachmentIngest,
			Action:     ControlActionAttachmentIngest,
			Params: map[string]any{
				"session_id":     session.ID,
				"name":           name,
				"mime_type":      "text/plain",
				"content_base64": encoded,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionAttachmentIngest) || strings.Contains(string(sealedRequest), session.ID) || strings.Contains(string(sealedRequest), name) || strings.Contains(string(sealedRequest), encoded) || strings.Contains(string(sealedRequest), string(secret)) {
		t.Fatalf("sealed attachment ingest request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), name) || strings.Contains(string(sealedResponse), encoded) || strings.Contains(string(sealedResponse), string(secret)) || strings.Contains(string(sealedResponse), app.store.dataDir) {
		t.Fatalf("sealed attachment ingest response leaked payload: %s", string(sealedResponse))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok attachment ingest response", plain)
	}
	result := mapValue(plain.Response.Result)
	attachment := mapValue(result["attachment"])
	attachmentID := stringValue(attachment["id"])
	if stringValue(result["session_id"]) != session.ID || attachmentID == "" || stringValue(attachment["media_id"]) != attachmentID {
		t.Fatalf("attachment ingest result = %#v", result)
	}
	if !boolValue(attachment["host_owned"]) || stringValue(attachment["path"]) != "" || stringValue(attachment["name"]) != name || stringValue(attachment["mime_type"]) != "text/plain" || int64(numberValue(attachment["size"])) != int64(len(secret)) {
		t.Fatalf("attachment handle = %#v", attachment)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), app.store.dataDir) || strings.Contains(string(wire), string(secret)) || strings.Contains(string(wire), encoded) {
		t.Fatalf("attachment ingest result leaked Host path or content: %s", string(wire))
	}
	stored, err := app.loadControlAttachment(session.ID, attachmentID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(stored.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(secret) {
		t.Fatalf("stored attachment body = %q, want secret", string(body))
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

func TestControlWebSocketWorkspaceFileWriteRequestResponseIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesWrite)
	secret := "sealed-workspace-write-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_write",
			Capability: CapabilityWorkspaceFilesWrite,
			Action:     ControlActionWorkspaceFilesWrite,
			Params: map[string]any{
				"workspace_id":   workspace.ID,
				"path":           "nested/out.txt",
				"content_base64": encoded,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionWorkspaceFilesWrite) || strings.Contains(string(sealedRequest), "nested/out.txt") || strings.Contains(string(sealedRequest), encoded) || strings.Contains(string(sealedRequest), workspace.ID) {
		t.Fatalf("sealed workspace file write request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), "nested/out.txt") || strings.Contains(string(sealedResponse), secret) || strings.Contains(string(sealedResponse), workspace.LocalCWD) {
		t.Fatalf("sealed workspace file write response leaked payload: %s", string(sealedResponse))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok workspace file write response", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["workspace_id"]) != workspace.ID || stringValue(result["path"]) != "nested/out.txt" || stringValue(result["kind"]) != "file" {
		t.Fatalf("workspace file write response metadata = %#v", result)
	}
	if int64(numberValue(result["size"])) != int64(len(secret)) {
		t.Fatalf("workspace file write size = %#v, want %d", result["size"], len(secret))
	}
	body, err := os.ReadFile(filepath.Join(workspace.LocalCWD, "nested", "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != secret {
		t.Fatalf("written body = %q, want encrypted write output", string(body))
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), workspace.LocalCWD) || strings.Contains(string(wire), secret) {
		t.Fatalf("workspace file write response leaked Host root or content: %s", string(wire))
	}
}

func TestControlWebSocketWorkspacePatchRequestResponseIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesWrite)
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "secret.txt"), []byte("old-wire-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_patch",
			Capability: CapabilityWorkspaceFilesWrite,
			Action:     ControlActionWorkspaceFilesApplyPatch,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "secret.txt",
				"edits": []map[string]any{
					{
						"old_string": "old-wire-secret",
						"new_string": "new-wire-secret",
					},
				},
			},
		},
	})
	if strings.Contains(string(sealedRequest), "old-wire-secret") || strings.Contains(string(sealedRequest), "new-wire-secret") {
		t.Fatalf("sealed workspace patch request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), "old-wire-secret") || strings.Contains(string(sealedResponse), "new-wire-secret") {
		t.Fatalf("sealed workspace patch response leaked payload: %s", string(sealedResponse))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok workspace patch response", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["path"]) != "secret.txt" || numberValue(result["applied_edits"]) != 1 {
		t.Fatalf("workspace patch response metadata = %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace.LocalCWD, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new-wire-secret\n" {
		t.Fatalf("patched body = %q, want encrypted patch output", string(body))
	}
}

func TestControlWebSocketWorkspaceFileMoveDeleteRequestResponseIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesWrite)
	secret := "sealed-workspace-move-secret"
	source := filepath.Join(workspace.LocalCWD, "from-secret.txt")
	target := filepath.Join(workspace.LocalCWD, "nested", "to-secret.txt")
	if err := os.WriteFile(source, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedMoveRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_move",
			Capability: CapabilityWorkspaceFilesWrite,
			Action:     ControlActionWorkspaceFilesMove,
			Params: map[string]any{
				"workspace_id":     workspace.ID,
				"path":             "from-secret.txt",
				"destination_path": "nested/to-secret.txt",
				"create_parents":   true,
			},
		},
	})
	if strings.Contains(string(sealedMoveRequest), ControlActionWorkspaceFilesMove) || strings.Contains(string(sealedMoveRequest), "from-secret.txt") || strings.Contains(string(sealedMoveRequest), "nested/to-secret.txt") || strings.Contains(string(sealedMoveRequest), workspace.ID) {
		t.Fatalf("sealed workspace file move request leaked payload: %s", string(sealedMoveRequest))
	}

	movePlain, sealedMoveResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedMoveResponse), "from-secret.txt") || strings.Contains(string(sealedMoveResponse), "nested/to-secret.txt") || strings.Contains(string(sealedMoveResponse), workspace.LocalCWD) {
		t.Fatalf("sealed workspace file move response leaked payload: %s", string(sealedMoveResponse))
	}
	if movePlain.Type != "response" || movePlain.Response == nil || !movePlain.Response.OK {
		t.Fatalf("move response = %#v, want ok workspace file move response", movePlain)
	}
	moveResult := mapValue(movePlain.Response.Result)
	if stringValue(moveResult["from_path"]) != "from-secret.txt" || stringValue(moveResult["to_path"]) != "nested/to-secret.txt" || stringValue(moveResult["kind"]) != "file" {
		t.Fatalf("workspace file move response metadata = %#v", moveResult)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("moved source stat err = %v, want not exist", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != secret {
		t.Fatalf("moved body = %q, want encrypted move output", string(body))
	}

	sealedDeleteRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_delete",
			Capability: CapabilityWorkspaceFilesWrite,
			Action:     ControlActionWorkspaceFilesDelete,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "nested/to-secret.txt",
			},
		},
	})
	if strings.Contains(string(sealedDeleteRequest), ControlActionWorkspaceFilesDelete) || strings.Contains(string(sealedDeleteRequest), "nested/to-secret.txt") || strings.Contains(string(sealedDeleteRequest), workspace.ID) {
		t.Fatalf("sealed workspace file delete request leaked payload: %s", string(sealedDeleteRequest))
	}

	deletePlain, sealedDeleteResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedDeleteResponse), "nested/to-secret.txt") || strings.Contains(string(sealedDeleteResponse), workspace.LocalCWD) {
		t.Fatalf("sealed workspace file delete response leaked payload: %s", string(sealedDeleteResponse))
	}
	if deletePlain.Type != "response" || deletePlain.Response == nil || !deletePlain.Response.OK {
		t.Fatalf("delete response = %#v, want ok workspace file delete response", deletePlain)
	}
	deleteResult := mapValue(deletePlain.Response.Result)
	if stringValue(deleteResult["path"]) != "nested/to-secret.txt" || stringValue(deleteResult["kind"]) != "file" || !boolValue(deleteResult["removed"]) {
		t.Fatalf("workspace file delete response metadata = %#v", deleteResult)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted target stat err = %v, want not exist", err)
	}
}

func TestControlWebSocketWorkspaceExecRequestResponseIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceExec)
	secret := "sealed-workspace-exec-secret"
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "exec-secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	command := "cat exec-secret.txt"
	if runtime.GOOS == "windows" {
		command = "type exec-secret.txt"
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_exec",
			Capability: CapabilityWorkspaceExec,
			Action:     ControlActionWorkspaceExec,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"command":      command,
				"timeout_ms":   5000,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionWorkspaceExec) || strings.Contains(string(sealedRequest), command) || strings.Contains(string(sealedRequest), workspace.ID) {
		t.Fatalf("sealed workspace exec request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), secret) || strings.Contains(string(sealedResponse), command) || strings.Contains(string(sealedResponse), workspace.LocalCWD) {
		t.Fatalf("sealed workspace exec response leaked payload: %s", string(sealedResponse))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok workspace exec response", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["workspace_id"]) != workspace.ID || stringValue(result["command"]) != command || stringValue(result["cwd"]) != "" {
		t.Fatalf("workspace exec response metadata = %#v", result)
	}
	if int(numberValue(result["exit_code"])) != 0 || stringValue(result["stdout"]) != secret || stringValue(result["approval_policy"]) != WorkspaceExecPolicyTrusted {
		t.Fatalf("workspace exec response result = %#v", result)
	}
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), workspace.LocalCWD) {
		t.Fatalf("workspace exec response leaked Host root: %s", string(wire))
	}
}

func TestControlWebSocketWorkspaceExecRequireApprovalIsEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceExec)
	if _, err := app.store.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "dev_controller",
		ControllerPublicKey: base64.StdEncoding.EncodeToString(controllerPublicKey),
		Capabilities:        []string{CapabilityWorkspaceExec},
		WorkspaceExecPolicy: WorkspaceExecPolicyRequireApproval,
	}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace.LocalCWD, "should-not-run.txt")
	command := "printf nope > should-not-run.txt"
	if runtime.GOOS == "windows" {
		command = "echo nope > should-not-run.txt"
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_exec_approval",
			Capability: CapabilityWorkspaceExec,
			Action:     ControlActionWorkspaceExec,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"command":      command,
				"timeout_ms":   5000,
			},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionWorkspaceExec) || strings.Contains(string(sealedRequest), command) || strings.Contains(string(sealedRequest), workspace.ID) {
		t.Fatalf("sealed workspace exec approval request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), command) || strings.Contains(string(sealedResponse), "workspace_exec_approval_required") {
		t.Fatalf("sealed workspace exec approval response leaked payload: %s", string(sealedResponse))
	}
	if plain.Type != "response" || plain.Response == nil || plain.Response.OK || plain.Response.Error == nil {
		t.Fatalf("plain response = %#v, want workspace exec approval error", plain)
	}
	if plain.Response.Error.Status != http.StatusConflict || plain.Response.Error.Code != "workspace_exec_approval_required" {
		t.Fatalf("workspace exec approval error = %#v", plain.Response.Error)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("policy-gated command created marker, stat err = %v", statErr)
	}
}

func TestControlWebSocketWorkspaceFileStreamChunksAreEncrypted(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesRead)
	secret := []byte("sealed-workspace-file-stream-secret")
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "large.txt"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_stream",
			Capability: CapabilityWorkspaceFilesRead,
			Action:     ControlActionWorkspaceFilesStream,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "large.txt",
				"chunk_size":   7,
			},
		},
	})

	plain, sealed := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealed), string(secret)) {
		t.Fatalf("sealed workspace file stream response leaked payload: %s", string(sealed))
	}
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("plain response = %#v, want ok workspace file stream response", plain)
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
			t.Fatalf("sealed workspace file stream frame leaked payload: %s", string(sealed))
		}
		if frame.WorkspaceFile == nil || frame.WorkspaceFile.StreamID != streamID {
			t.Fatalf("stream frame = %#v, want workspace file frame for stream %q", frame, streamID)
		}
		switch frame.Type {
		case workspaceFileStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.WorkspaceFile.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			streamed = append(streamed, body...)
		case workspaceFileStreamFrameComplete:
			if !frame.WorkspaceFile.Final {
				t.Fatalf("completion frame = %#v, want final", frame.WorkspaceFile)
			}
			if string(streamed) != string(secret) {
				t.Fatalf("streamed workspace file = %q, want %q", string(streamed), string(secret))
			}
			return
		default:
			t.Fatalf("stream frame type = %q", frame.Type)
		}
	}
}

func TestControlWebSocketWorkspaceFileStreamCancelIsEncrypted(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesRead)
	server := startControlChannelTestServer(t, app)
	client, cipher, ack := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	streamID := "workspace_file_secret_id"
	ctx, cancel := context.WithCancel(context.Background())
	controlSessionForAck(t, app, ack).registerWorkspaceFileStream(streamID, cancel)

	sealedRequest := writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_stream_cancel",
			Capability: CapabilityWorkspaceFilesRead,
			Action:     ControlActionWorkspaceFilesStreamCancel,
			Params:     map[string]any{"stream_id": streamID},
		},
	})
	if strings.Contains(string(sealedRequest), ControlActionWorkspaceFilesStreamCancel) || strings.Contains(string(sealedRequest), streamID) {
		t.Fatalf("sealed workspace file stream cancel request leaked payload: %s", string(sealedRequest))
	}

	plain, sealedResponse := readEncryptedControlFrameWithBody(t, client, cipher)
	if strings.Contains(string(sealedResponse), streamID) {
		t.Fatalf("sealed workspace file stream cancel response leaked payload: %s", string(sealedResponse))
	}
	if plain.Response == nil || !plain.Response.OK {
		t.Fatalf("cancel response = %#v, want ok", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["stream_id"]) != streamID || !boolValue(result["cancelled"]) {
		t.Fatalf("cancel result = %#v, want cancelled stream", result)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("workspace file stream cancel did not cancel registered context")
	}
}

func TestControlWebSocketWorkspaceFileStreamResumesFromOffset(t *testing.T) {
	app, workspace, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityWorkspaceFilesRead)
	if err := os.WriteFile(filepath.Join(workspace.LocalCWD, "resume.txt"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "workspace_file_stream_resume",
			Capability: CapabilityWorkspaceFilesRead,
			Action:     ControlActionWorkspaceFilesStream,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "resume.txt",
				"offset":       4,
				"chunk_size":   3,
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
		if frame.WorkspaceFile == nil || frame.WorkspaceFile.StreamID != streamID {
			t.Fatalf("stream frame = %#v, want workspace file frame for stream %q", frame, streamID)
		}
		switch frame.Type {
		case workspaceFileStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.WorkspaceFile.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			streamed = append(streamed, body...)
		case workspaceFileStreamFrameComplete:
			if string(streamed) != "456789" {
				t.Fatalf("resumed workspace file stream = %q, want 456789", string(streamed))
			}
			return
		default:
			t.Fatalf("stream frame type = %q", frame.Type)
		}
	}
}

func TestControlWebSocketRemoteWorkspaceFileStreamUsesProxyReadRange(t *testing.T) {
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.createWorkspace(createWorkspaceRequest{
		Name:   "Remote",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH:    &SSHConfig{Endpoint: "root@example.test", RemoteCWD: "/remote/project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteStore := t.TempDir()
	writeRemoteFixtureFile(t, remoteStore, "/remote/project/big.txt", "remote-stream-body")
	proxy, cleanup := newMutableClaudeRemoteProxy(t, workspace, remoteStore)
	defer cleanup()
	controllerPublicKey, controllerPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.trustDevice(trustDeviceRequest{
		ControllerDeviceID:  "dev_controller",
		ControllerPublicKey: base64.StdEncoding.EncodeToString(controllerPublicKey),
		Capabilities:        []string{CapabilityWorkspaceFilesRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{
		store:    st,
		hub:      newEventHub(),
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
	app.ssh = &sshManager{
		app: app,
		by: map[string]*sshTarget{
			workspace.ID: {workspace: workspace, proxy: proxy, state: initialSSHConnection(workspace, connectionConnected)},
		},
	}
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)
	defer client.Close()

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "remote_workspace_file_stream",
			Capability: CapabilityWorkspaceFilesRead,
			Action:     ControlActionWorkspaceFilesStream,
			Params: map[string]any{
				"workspace_id": workspace.ID,
				"path":         "big.txt",
				"offset":       7,
				"chunk_size":   4,
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
		if frame.WorkspaceFile == nil || frame.WorkspaceFile.StreamID != streamID {
			t.Fatalf("stream frame = %#v, want workspace file frame for stream %q", frame, streamID)
		}
		switch frame.Type {
		case workspaceFileStreamFrameChunk:
			body, err := base64.StdEncoding.DecodeString(frame.WorkspaceFile.DataBase64)
			if err != nil {
				t.Fatal(err)
			}
			streamed = append(streamed, body...)
		case workspaceFileStreamFrameComplete:
			if string(streamed) != "stream-body" {
				t.Fatalf("remote workspace stream = %q, want stream-body", string(streamed))
			}
			return
		default:
			t.Fatalf("stream frame type = %q", frame.Type)
		}
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

func TestControlWebSocketHostTrustSelfRevokeRespondsThenCloses(t *testing.T) {
	app, _, _, controllerPublicKey, controllerPrivateKey := newControlChannelTestApp(t, CapabilityHostManage)
	server := startControlChannelTestServer(t, app)
	client, cipher, _ := dialControlChannel(t, server.URL, app, controllerPublicKey, controllerPrivateKey)

	writeEncryptedControlFrame(t, client, cipher, controlPlainFrame{
		Type: "request",
		Request: &ControlRequest{
			RequestID:  "host_trust_revoke_self",
			Capability: CapabilityHostManage,
			Action:     ControlActionHostTrustRevoke,
			Params: map[string]any{
				"controller_device_id": "dev_controller",
			},
		},
	})

	plain := readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "response" || plain.Response == nil || !plain.Response.OK {
		t.Fatalf("self revoke response = %#v, want ok response before close", plain)
	}
	result := mapValue(plain.Response.Result)
	if stringValue(result["controller_device_id"]) != "dev_controller" {
		t.Fatalf("self revoke result = %#v", result)
	}

	plain = readEncryptedControlFrame(t, client, cipher)
	if plain.Type != "close" || plain.Code != "trust_revoked" {
		t.Fatalf("close frame = %#v, want trust_revoked", plain)
	}
	_ = client.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := app.activeControlSessionCountForDevice("dev_controller"); got == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("active sessions = %d, want 0 after self revoke", app.activeControlSessionCountForDevice("dev_controller"))
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
