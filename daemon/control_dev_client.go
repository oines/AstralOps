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
	"time"

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
		return fmt.Errorf("usage: control-client <identity|known-hosts|discover|pair|workspaces|request>")
	}
	st, err := loadStore(defaultDataDir())
	if err != nil {
		return err
	}
	switch args[0] {
	case "identity":
		return writePrettyJSON(os.Stdout, st.hostInfo().Identity)
	case "known-hosts":
		return writePrettyJSON(os.Stdout, st.listKnownHosts())
	case "discover":
		fs := flag.NewFlagSet("control-client discover", flag.ContinueOnError)
		timeout := fs.Duration("timeout", 3*time.Second, "LAN discovery timeout")
		port := fs.Int("port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		candidates, err := discoverRemoteControlHostsWithTimeout(*timeout, *port)
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, candidates)
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
		grant, err := controlClientPair(*host, st, parseCapabilityList(*capabilityList))
		if err != nil {
			return err
		}
		return writePrettyJSON(os.Stdout, grant)
	case "workspaces":
		fs := flag.NewFlagSet("control-client workspaces", flag.ContinueOnError)
		host := fs.String("host", "", "remote Host base URL")
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
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
		discover := fs.Bool("discover", false, "discover a known Host on LAN before connecting")
		hostDeviceID := fs.String("host-device-id", "", "known Host device id for LAN discovery")
		discoveryPort := fs.Int("discovery-port", defaultRemoteControlDiscoveryPort, "LAN discovery UDP port")
		discoveryTimeout := fs.Duration("discovery-timeout", 3*time.Second, "LAN discovery timeout")
		lanTimeout := fs.Duration("lan-timeout", 2*time.Second, "LAN host validation and handshake timeout")
		action := fs.String("action", "", "control action")
		capability := fs.String("capability", "", "control capability")
		params := fs.String("params", "{}", "JSON params object")
		requestID := fs.String("request-id", "dev_request", "request id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *action == "" || *capability == "" {
			return fmt.Errorf("--action and --capability are required")
		}
		var decodedParams map[string]any
		if err := json.Unmarshal([]byte(*params), &decodedParams); err != nil {
			return err
		}
		target, err := controlClientResolveTarget(st, controlClientTargetOptions{
			Host:             *host,
			Discover:         *discover,
			HostDeviceID:     *hostDeviceID,
			DiscoveryPort:    *discoveryPort,
			DiscoveryTimeout: *discoveryTimeout,
			LANTimeout:       *lanTimeout,
		})
		if err != nil {
			return err
		}
		response, err := controlClientRequestToTarget(target, st, ControlRequest{
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

type controlClientTargetOptions struct {
	Host             string
	Discover         bool
	HostDeviceID     string
	DiscoveryPort    int
	DiscoveryTimeout time.Duration
	LANTimeout       time.Duration
}

type controlClientTarget struct {
	BaseURL  string
	HostInfo HostInfo
	Timeout  time.Duration
}

func controlClientResolveTarget(st *store, opts controlClientTargetOptions) (controlClientTarget, error) {
	if opts.Host != "" && opts.Discover {
		return controlClientTarget{}, fmt.Errorf("--host and --discover cannot be used together")
	}
	if opts.Host != "" {
		hostInfo, err := controlClientHostInfo(opts.Host)
		if err != nil {
			return controlClientTarget{}, err
		}
		return controlClientTarget{BaseURL: opts.Host, HostInfo: hostInfo}, nil
	}
	if !opts.Discover {
		return controlClientTarget{}, fmt.Errorf("--host is required unless --discover is set")
	}
	if opts.DiscoveryTimeout <= 0 {
		opts.DiscoveryTimeout = 3 * time.Second
	}
	if opts.LANTimeout <= 0 {
		opts.LANTimeout = 2 * time.Second
	}
	candidates, err := discoverRemoteControlHostsWithTimeout(opts.DiscoveryTimeout, opts.DiscoveryPort)
	if err != nil {
		return controlClientTarget{}, err
	}
	candidate, knownHost, err := selectKnownLanCandidate(st, candidates, opts.HostDeviceID)
	if err != nil {
		return controlClientTarget{}, err
	}
	client := &http.Client{Timeout: opts.LANTimeout}
	hostInfo, err := controlClientHostInfoWithClient(candidate.BaseURL, client)
	if err != nil {
		return controlClientTarget{}, err
	}
	if err := validateKnownLanHost(candidate, knownHost, hostInfo); err != nil {
		return controlClientTarget{}, err
	}
	return controlClientTarget{BaseURL: candidate.BaseURL, HostInfo: hostInfo, Timeout: opts.LANTimeout}, nil
}

func selectKnownLanCandidate(st *store, candidates []LanHostCandidate, hostDeviceID string) (LanHostCandidate, KnownHost, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	matches := []struct {
		candidate LanHostCandidate
		knownHost KnownHost
	}{}
	for _, candidate := range candidates {
		if hostDeviceID != "" && candidate.DeviceID != hostDeviceID {
			continue
		}
		knownHost, ok := st.knownHost(candidate.DeviceID)
		if !ok {
			continue
		}
		if knownHost.PublicKeyFingerprint != candidate.PublicKeyFingerprint {
			continue
		}
		matches = append(matches, struct {
			candidate LanHostCandidate
			knownHost KnownHost
		}{candidate: candidate, knownHost: knownHost})
	}
	if len(matches) == 0 {
		if hostDeviceID == "" {
			return LanHostCandidate{}, KnownHost{}, fmt.Errorf("no known LAN Host candidates found; pair the Host first or pass --host")
		}
		return LanHostCandidate{}, KnownHost{}, fmt.Errorf("known Host %q was not found on LAN", hostDeviceID)
	}
	if len(matches) > 1 {
		return LanHostCandidate{}, KnownHost{}, fmt.Errorf("multiple known LAN Host candidates found; pass --host-device-id")
	}
	return matches[0].candidate, matches[0].knownHost, nil
}

func validateKnownLanHost(candidate LanHostCandidate, knownHost KnownHost, hostInfo HostInfo) error {
	if hostInfo.Identity.DeviceID != candidate.DeviceID {
		return fmt.Errorf("LAN candidate device_id mismatch: discovered %q but Host returned %q", candidate.DeviceID, hostInfo.Identity.DeviceID)
	}
	if hostInfo.Identity.DeviceID != knownHost.DeviceID {
		return fmt.Errorf("known Host device_id mismatch: known %q but Host returned %q", knownHost.DeviceID, hostInfo.Identity.DeviceID)
	}
	if hostInfo.Identity.PublicKey != knownHost.PublicKey {
		return fmt.Errorf("known Host public key mismatch for %s", knownHost.DeviceID)
	}
	if hostInfo.Identity.PublicKeyFingerprint != knownHost.PublicKeyFingerprint || candidate.PublicKeyFingerprint != knownHost.PublicKeyFingerprint {
		return fmt.Errorf("known Host public key fingerprint mismatch for %s", knownHost.DeviceID)
	}
	return nil
}

func controlClientPair(host string, st *store, capabilities []string) (TrustGrant, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return TrustGrant{}, err
	}
	identity := st.deviceIdentity
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
	if _, err := st.rememberKnownHost(hostInfo, host); err != nil {
		return TrustGrant{}, err
	}
	return grant, nil
}

func controlClientRequest(host string, st *store, req ControlRequest) (ControlResponse, error) {
	hostInfo, err := controlClientHostInfo(host)
	if err != nil {
		return ControlResponse{}, err
	}
	return controlClientRequestToTarget(controlClientTarget{BaseURL: host, HostInfo: hostInfo}, st, req)
}

func controlClientRequestToTarget(target controlClientTarget, st *store, req ControlRequest) (ControlResponse, error) {
	socket, cipher, err := controlClientDialWithTimeout(target.BaseURL, st, target.HostInfo, target.Timeout)
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
	return controlClientHostInfoWithClient(host, http.DefaultClient)
}

func controlClientHostInfoWithClient(host string, client *http.Client) (HostInfo, error) {
	resp, err := client.Get(controlHTTPURL(host, "/v1/host"))
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
	return controlClientDialWithTimeout(host, st, hostInfo, 0)
}

func controlClientDialWithTimeout(host string, st *store, hostInfo HostInfo, timeout time.Duration) (*websocket.Conn, *controlCipher, error) {
	dialer := *websocket.DefaultDialer
	if timeout > 0 {
		dialer.HandshakeTimeout = timeout
	}
	socket, _, err := dialer.Dial(controlWSURL(host, "/v1/control/ws"), nil)
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
