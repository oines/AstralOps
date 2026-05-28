package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

func runControlDevClient(args []string) bool {
	if len(args) == 0 || args[0] != "control-client" {
		return false
	}
	if err := runControlDevClientCommand(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}

func runControlDevClientCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: control-client <identity|pair|workspaces|request>")
	}
	st, err := loadStore(defaultDataDir())
	if err != nil {
		return err
	}
	switch args[0] {
	case "identity":
		return writePrettyJSON(os.Stdout, st.hostInfo().Identity)
	case "pair":
		fs := flag.NewFlagSet("control-client pair", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL, for example http://10.0.0.10:43900")
		capabilityList := fs.String("capabilities", strings.Join(defaultHostCapabilities(), ","), "comma-separated capabilities")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *host == "" {
			return fmt.Errorf("--host is required")
		}
		grant, err := controlClientPair(*host, st.deviceIdentity, parseCapabilityList(*capabilityList))
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, grant)
	case "workspaces":
		fs := flag.NewFlagSet("control-client workspaces", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *host == "" {
			return fmt.Errorf("--host is required")
		}
		response, err := controlClientRequest(*host, st, ControlRequest{
			RequestID:  "dev_workspaces",
			Capability: CapabilityCoreRead,
			Action:     ControlActionWorkspaces,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	case "request":
		fs := flag.NewFlagSet("control-client request", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		action := fs.String("action", "", "control action")
		capability := fs.String("capability", "", "control capability")
		params := fs.String("params", "{}", "JSON params object")
		requestID := fs.String("request-id", "dev_request", "request id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *host == "" || *action == "" || *capability == "" {
			return fmt.Errorf("--host, --action, and --capability are required")
		}
		var decodedParams map[string]any
		if err := json.Unmarshal([]byte(*params), &decodedParams); err != nil {
			return err
		}
		response, err := controlClientRequest(*host, st, ControlRequest{
			RequestID:  *requestID,
			Capability: *capability,
			Action:     *action,
			Params:     decodedParams,
		})
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, response)
	default:
		return fmt.Errorf("unknown control-client command %q", args[0])
	}
}

func controlClientPair(host string, identity DeviceIdentity, capabilities []string) (TrustGrant, error) {
	req := trustDeviceRequest{
		ControllerDeviceID:             identity.DeviceID,
		ControllerDeviceName:           identity.DeviceName,
		ControllerPublicKey:            identity.PublicKey,
		ControllerPublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:                   capabilities,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return TrustGrant{}, err
	}
	httpResp, err := http.Post(controlHTTPURL(host, "/v1/trust/devices"), "application/json", bytes.NewReader(body))
	if err != nil {
		return TrustGrant{}, err
	}
	defer httpResp.Body.Close()
	responseBody, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return TrustGrant{}, fmt.Errorf("pairing failed: status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var grant TrustGrant
	if err := json.Unmarshal(responseBody, &grant); err != nil {
		return TrustGrant{}, err
	}
	return grant, nil
}

func controlClientRequest(host string, st *store, req ControlRequest) (ControlResponse, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return ControlResponse{}, err
	}
	socket, cipher, err := controlClientDial(host, st, hostInfo)
	if err != nil {
		return ControlResponse{}, err
	}
	defer socket.Close()

	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	if err := controlClientWrite(socket, cipher, controlPlainFrame{Type: "request", Request: &req}); err != nil {
		return ControlResponse{}, err
	}
	plain, err := controlClientRead(socket, cipher)
	if err != nil {
		return ControlResponse{}, err
	}
	if plain.Response == nil {
		return ControlResponse{}, fmt.Errorf("remote did not return a response frame")
	}
	return *plain.Response, nil
}

func controlClientHostInfo(host string) (HostInfo, error) {
	resp, err := http.Get(controlHTTPURL(host, "/v1/host"))
	if err != nil {
		return HostInfo{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return HostInfo{}, fmt.Errorf("host info failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info HostInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return HostInfo{}, err
	}
	if info.Identity.DeviceID == "" || info.Identity.PublicKey == "" {
		return HostInfo{}, fmt.Errorf("remote Host did not return a usable identity")
	}
	return info, nil
}

func controlClientDial(host string, st *store, hostInfo HostInfo) (*websocket.Conn, *controlCipher, error) {
	socket, _, err := websocket.DefaultDialer.Dial(controlWSURL(host, "/v1/control/ws"), nil)
	if err != nil {
		return nil, nil, err
	}
	curve := ecdh.X25519()
	controllerEphemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	clientNonce, err := randomBase64(32)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	hello := controlHelloFrame{
		Type:                   "hello",
		Version:                controlProtocolVersion,
		ControllerDeviceID:     st.deviceIdentity.DeviceID,
		ControllerPublicKey:    st.deviceIdentity.PublicKey,
		ControllerEphemeralKey: base64.StdEncoding.EncodeToString(controllerEphemeral.PublicKey().Bytes()),
		ClientNonce:            clientNonce,
	}
	hello.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(st.devicePrivateKey), controlClientSignaturePayload(hostInfo.Identity.DeviceID, hello)))
	if err := socket.WriteJSON(hello); err != nil {
		socket.Close()
		return nil, nil, err
	}
	var ack controlHelloAckFrame
	if err := socket.ReadJSON(&ack); err != nil {
		socket.Close()
		return nil, nil, err
	}
	if ack.Type != "hello_ack" || ack.Version != controlProtocolVersion {
		socket.Close()
		return nil, nil, fmt.Errorf("invalid control hello_ack")
	}
	if ack.HostDeviceID != hostInfo.Identity.DeviceID || ack.HostPublicKey != hostInfo.Identity.PublicKey {
		socket.Close()
		return nil, nil, fmt.Errorf("remote Host identity changed during handshake")
	}
	hostPublicKey, err := decodeDevicePublicKey(ack.HostPublicKey)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(ack.Signature)
	if err != nil || !ed25519.Verify(hostPublicKey, controlHostSignaturePayload(hello, ack), signature) {
		socket.Close()
		return nil, nil, fmt.Errorf("invalid Host hello_ack signature")
	}
	hostEphemeralBytes, err := base64.StdEncoding.DecodeString(ack.HostEphemeralKey)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	hostEphemeral, err := curve.NewPublicKey(hostEphemeralBytes)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	sharedSecret, err := controllerEphemeral.ECDH(hostEphemeral)
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	cipher, err := newControlCipher(deriveControlSessionKey(sharedSecret, hello, ack.HostDeviceID, ack.HostPublicKey, ack.HostEphemeralKey, ack.ServerNonce, ack.ConnectionID))
	if err != nil {
		socket.Close()
		return nil, nil, err
	}
	return socket, cipher, nil
}

func controlClientWrite(socket *websocket.Conn, cipher *controlCipher, frame controlPlainFrame) error {
	sealed, err := cipher.seal(frame)
	if err != nil {
		return err
	}
	return socket.WriteJSON(sealed)
}

func controlClientRead(socket *websocket.Conn, cipher *controlCipher) (controlPlainFrame, error) {
	var sealed controlSealedFrame
	if err := socket.ReadJSON(&sealed); err != nil {
		return controlPlainFrame{}, err
	}
	return cipher.open(sealed)
}

func controlHTTPURL(host, path string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host + path
}

func controlWSURL(host, path string) string {
	url := controlHTTPURL(host, path)
	url = strings.TrimPrefix(url, "http://")
	if strings.HasPrefix(url, "https://") {
		return "wss://" + strings.TrimPrefix(url, "https://")
	}
	return "ws://" + url
}

func parseCapabilityList(value string) []string {
	items := strings.Split(value, ",")
	out := []string{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func writePrettyJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
