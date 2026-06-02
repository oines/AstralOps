package controllercore

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oines/astralops/pkg/cloudmesh"
	"github.com/oines/astralops/pkg/controlwire"
	"github.com/oines/astralops/pkg/relaymesh"
)

const (
	DefaultDiscoveryPort    = 43900
	defaultDiscoveryTimeout = 3 * time.Second
	defaultLANTimeout       = 2 * time.Second
	defaultRelayTimeout     = relaymesh.RoundTripTimeout

	discoveryRequestType  = "astralops.discovery.request"
	discoveryResponseType = "astralops.discovery.response"
)

type HostInfo struct {
	Identity     cloudmesh.DeviceIdentity `json:"identity"`
	Capabilities []string                 `json:"capabilities,omitempty"`
}

type KnownHost struct {
	DeviceID             string `json:"device_id"`
	DeviceName           string `json:"device_name,omitempty"`
	PublicKey            string `json:"public_key"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	Status               string `json:"status,omitempty"`
	LastBaseURL          string `json:"last_base_url,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	RevokedAt            string `json:"revoked_at,omitempty"`
}

type LanHostCandidate struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	AccountIDHash        string   `json:"account_id_hash,omitempty"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Host                 string   `json:"host"`
	Port                 int      `json:"port"`
	BaseURL              string   `json:"base_url"`
	Addresses            []string `json:"addresses"`
}

type ClientTarget struct {
	HostInfo           HostInfo
	BaseURL            string
	Timeout            time.Duration
	RelayClient        relaymesh.Client
	UseRelay           bool
	ControllerDeviceID string
}

type ClientCredentials struct {
	Identity   cloudmesh.DeviceIdentity
	PrivateKey ed25519.PrivateKey
	Membership controlwire.MembershipState
}

type discoveryPacket struct {
	Type      string            `json:"type"`
	Version   string            `json:"version"`
	Candidate *LanHostCandidate `json:"candidate,omitempty"`
}

type wsFrameConn struct {
	writeMu sync.Mutex
	socket  *websocket.Conn
	cipher  *controlwire.Cipher
}

type relayFrameConn struct {
	writeMu      sync.Mutex
	target       ClientTarget
	relay        *relaymesh.WebSocketConn
	cipher       *controlwire.Cipher
	connectionID string
	openedAt     time.Time
	ctx          context.Context
	cancel       context.CancelFunc
}

func FetchHostInfo(ctx context.Context, baseURL string, timeout time.Duration) (HostInfo, error) {
	if timeout <= 0 {
		timeout = defaultLANTimeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, controlHTTPURL(baseURL, "/v1/host"), nil)
	if err != nil {
		return HostInfo{}, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
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

func DiscoverLANHosts(ctx context.Context, port int) ([]LanHostCandidate, error) {
	if port <= 0 {
		port = DefaultDiscoveryPort
	}
	return discoverLANHostsAt(ctx, broadcastTargets(port))
}

func DiscoverLANHost(ctx context.Context, port int, accept func(LanHostCandidate) bool) (LanHostCandidate, bool, error) {
	if port <= 0 {
		port = DefaultDiscoveryPort
	}
	candidates, err := discoverLANHostsAt(ctx, broadcastTargets(port))
	if err != nil {
		return LanHostCandidate{}, false, err
	}
	for _, candidate := range candidates {
		if accept == nil || accept(candidate) {
			return candidate, true, nil
		}
	}
	return LanHostCandidate{}, false, nil
}

func DialDirectFrameConn(ctx context.Context, target ClientTarget, credentials ClientCredentials) (FrameConn, ClientTarget, error) {
	if target.UseRelay {
		return nil, target, fmt.Errorf("direct control dial requires a non-relay target")
	}
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = defaultLANTimeout
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = timeout
	socket, _, err := dialer.DialContext(ctx, controlWSURL(target.BaseURL, "/v1/control/ws"), nil)
	if err != nil {
		return nil, target, err
	}
	cipher, err := performControllerHandshake(socket, target.HostInfo.Identity, credentials)
	if err != nil {
		_ = socket.Close()
		return nil, target, err
	}
	target.Timeout = timeout
	return &wsFrameConn{socket: socket, cipher: cipher}, target, nil
}

func OpenRelayFrameConn(parent context.Context, target ClientTarget, credentials ClientCredentials) (FrameConn, ClientTarget, error) {
	if strings.TrimSpace(target.ControllerDeviceID) == "" {
		target.ControllerDeviceID = credentials.Identity.DeviceID
	}
	if strings.TrimSpace(target.RelayClient.BaseURL) == "" || strings.TrimSpace(target.RelayClient.Token) == "" {
		return nil, target, fmt.Errorf("cloud relay is not configured")
	}
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = defaultRelayTimeout
	}
	target.Timeout = timeout
	openedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	relay, err := target.RelayClient.ConnectRelayWebSocket(ctx, credentials.Identity.DeviceID)
	if err != nil {
		return nil, target, err
	}
	closeRelay := true
	defer func() {
		if closeRelay {
			_ = relay.Close()
		}
	}()
	hello, controllerEphemeral, err := controlwire.NewControllerHello(target.HostInfo.Identity.DeviceID, credentials.Identity, credentials.PrivateKey, credentials.Membership.Lease)
	if err != nil {
		return nil, target, err
	}
	helloBody, err := json.Marshal(hello)
	if err != nil {
		return nil, target, err
	}
	if _, err := relay.EnqueueRelayEnvelope(ctx, relaymesh.Envelope{
		Version:       relaymesh.EnvelopeVersion,
		FromDeviceID:  credentials.Identity.DeviceID,
		ToDeviceID:    target.HostInfo.Identity.DeviceID,
		PayloadKind:   relaymesh.PayloadKindControlHello,
		PayloadBase64: base64.StdEncoding.EncodeToString(helloBody),
	}); err != nil {
		return nil, target, err
	}
	ack, err := waitRelayHelloAck(ctx, relay, target, credentials, hello, openedAt)
	if err != nil {
		return nil, target, err
	}
	cipher, err := controlwire.NewControllerCipherFromAck(hello, ack, controllerEphemeral)
	if err != nil {
		return nil, target, err
	}
	connCtx, connCancel := context.WithCancel(context.Background())
	conn := &relayFrameConn{
		target:       target,
		relay:        relay,
		cipher:       cipher,
		connectionID: ack.ConnectionID,
		openedAt:     openedAt,
		ctx:          connCtx,
		cancel:       connCancel,
	}
	closeRelay = false
	registerRelayActive(target, ack.ConnectionID)
	target.UseRelay = true
	return conn, target, nil
}

func ValidateKnownHost(expected KnownHost, info HostInfo) error {
	expected = NormalizeKnownHost(expected)
	if expected.DeviceID == "" || expected.PublicKey == "" || expected.PublicKeyFingerprint == "" {
		return fmt.Errorf("known Host identity is incomplete")
	}
	if info.Identity.DeviceID != expected.DeviceID {
		return fmt.Errorf("remote Host device mismatch")
	}
	if info.Identity.PublicKey != expected.PublicKey {
		return fmt.Errorf("remote Host public key mismatch")
	}
	if info.Identity.PublicKeyFingerprint != "" && info.Identity.PublicKeyFingerprint != expected.PublicKeyFingerprint {
		return fmt.Errorf("remote Host public key fingerprint mismatch")
	}
	return nil
}

func NormalizeKnownHost(host KnownHost) KnownHost {
	host.DeviceID = strings.TrimSpace(host.DeviceID)
	host.DeviceName = strings.TrimSpace(host.DeviceName)
	host.PublicKey = strings.TrimSpace(host.PublicKey)
	host.PublicKeyFingerprint = strings.TrimSpace(host.PublicKeyFingerprint)
	host.Status = strings.TrimSpace(host.Status)
	host.LastBaseURL = strings.TrimRight(strings.TrimSpace(host.LastBaseURL), "/")
	host.RevokedAt = strings.TrimSpace(host.RevokedAt)
	return host
}

func KnownHostRevoked(host KnownHost) bool {
	return strings.TrimSpace(host.Status) == "revoked" || strings.TrimSpace(host.RevokedAt) != ""
}

func (c *wsFrameConn) Close() error {
	if c == nil || c.socket == nil {
		return nil
	}
	return c.socket.Close()
}

func (c *wsFrameConn) WritePlain(frame controlwire.PlainFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	sealed, err := c.cipher.Seal(frame)
	if err != nil {
		return err
	}
	return c.socket.WriteJSON(sealed)
}

func (c *wsFrameConn) ReadPlain(timeout time.Duration) (controlwire.PlainFrame, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	_ = c.socket.SetReadDeadline(time.Now().Add(timeout))
	defer c.socket.SetReadDeadline(time.Time{})
	var sealed controlwire.SealedFrame
	if err := c.socket.ReadJSON(&sealed); err != nil {
		return controlwire.PlainFrame{}, err
	}
	return c.cipher.Open(sealed)
}

func (c *relayFrameConn) Close() error {
	if c == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	unregisterRelayActive(c.target, c.connectionID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.relay != nil {
		defer c.relay.Close()
	}
	return relayWrite(ctx, c.relay, c.target, c.cipher, c.connectionID, controlwire.PlainFrame{Type: "close"})
}

func (c *relayFrameConn) WritePlain(frame controlwire.PlainFrame) error {
	timeout := c.target.Timeout
	if timeout <= 0 {
		timeout = defaultRelayTimeout
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return relayWrite(ctx, c.relay, c.target, c.cipher, c.connectionID, frame)
}

func (c *relayFrameConn) ReadPlain(timeout time.Duration) (controlwire.PlainFrame, error) {
	ctx := c.ctx
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(c.ctx, timeout)
	}
	defer cancel()
	return relayRead(ctx, c.relay, c.target, c.cipher, c.connectionID, c.openedAt)
}

func performControllerHandshake(socket *websocket.Conn, host cloudmesh.DeviceIdentity, credentials ClientCredentials) (*controlwire.Cipher, error) {
	hello, controllerEphemeral, err := controlwire.NewControllerHello(host.DeviceID, credentials.Identity, credentials.PrivateKey, credentials.Membership.Lease)
	if err != nil {
		return nil, err
	}
	if err := socket.WriteJSON(hello); err != nil {
		return nil, err
	}
	_, ackBody, err := socket.ReadMessage()
	if err != nil {
		return nil, err
	}
	if closeErr, ok := controlwire.ParseCloseFrame(ackBody); ok {
		if closeErr.Code == "capability_denied" {
			return nil, NewActionError(http.StatusForbidden, AuthorizationRequiredCode, "target Host must approve this device before remote control")
		}
		return nil, closeErr
	}
	var ack controlwire.HelloAckFrame
	if err := json.Unmarshal(ackBody, &ack); err != nil {
		return nil, err
	}
	if err := controlwire.ValidateControllerHelloAck(host, credentials.Membership, hello, ack); err != nil {
		return nil, err
	}
	return controlwire.NewControllerCipherFromAck(hello, ack, controllerEphemeral)
}

func waitRelayHelloAck(ctx context.Context, relay *relaymesh.WebSocketConn, target ClientTarget, credentials ClientCredentials, hello controlwire.HelloFrame, openedAt time.Time) (controlwire.HelloAckFrame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return controlwire.HelloAckFrame{}, err
		}
		envelope, err := relay.ReadRelayEnvelope(ctx)
		if err != nil {
			return controlwire.HelloAckFrame{}, err
		}
		if envelope.PayloadKind == relaymesh.PayloadKindControlSealedFrame && envelope.FromDeviceID == target.HostInfo.Identity.DeviceID && relayEnvelopeIsStale(target, envelope, openedAt) {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID)
			continue
		}
		if envelope.PayloadKind != relaymesh.PayloadKindControlHelloAck || envelope.FromDeviceID != target.HostInfo.Identity.DeviceID {
			continue
		}
		payload, err := relayEnvelopePayload(envelope, 16*1024)
		if err != nil {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID)
			continue
		}
		if closeErr, ok := controlwire.ParseCloseFrame(payload); ok {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID)
			if closeErr.Code == "capability_denied" {
				return controlwire.HelloAckFrame{}, NewActionError(http.StatusForbidden, AuthorizationRequiredCode, "target Host must approve this device before remote control")
			}
			return controlwire.HelloAckFrame{}, closeErr
		}
		var ack controlwire.HelloAckFrame
		if err := json.Unmarshal(payload, &ack); err != nil {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID)
			continue
		}
		if ack.ClientNonce != "" && ack.ClientNonce != hello.ClientNonce {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID)
			continue
		}
		if err := controlwire.ValidateControllerHelloAck(target.HostInfo.Identity, credentials.Membership, hello, ack); err != nil {
			continue
		}
		if err := relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, credentials.Identity.DeviceID); err != nil {
			return controlwire.HelloAckFrame{}, err
		}
		return ack, nil
	}
}

func relayWrite(ctx context.Context, relay *relaymesh.WebSocketConn, target ClientTarget, cipher *controlwire.Cipher, connectionID string, frame controlwire.PlainFrame) error {
	if relay == nil {
		return fmt.Errorf("cloud relay is not connected")
	}
	sealed, err := cipher.Seal(frame)
	if err != nil {
		return err
	}
	body, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	_, err = relay.EnqueueRelayEnvelope(ctx, relaymesh.Envelope{
		Version:       relaymesh.EnvelopeVersion,
		ConnectionID:  connectionID,
		FromDeviceID:  target.ControllerDeviceID,
		ToDeviceID:    target.HostInfo.Identity.DeviceID,
		PayloadKind:   relaymesh.PayloadKindControlSealedFrame,
		PayloadBase64: base64.StdEncoding.EncodeToString(body),
	})
	return err
}

func relayRead(ctx context.Context, relay *relaymesh.WebSocketConn, target ClientTarget, cipher *controlwire.Cipher, connectionID string, openedAt time.Time) (controlwire.PlainFrame, error) {
	if relay == nil {
		return controlwire.PlainFrame{}, fmt.Errorf("cloud relay is not connected")
	}
	for {
		if err := ctx.Err(); err != nil {
			return controlwire.PlainFrame{}, err
		}
		envelope, err := relay.ReadRelayEnvelope(ctx)
		if err != nil {
			return controlwire.PlainFrame{}, err
		}
		if envelope.PayloadKind != relaymesh.PayloadKindControlSealedFrame || envelope.FromDeviceID != target.HostInfo.Identity.DeviceID {
			continue
		}
		if envelope.ConnectionID != connectionID {
			if relayEnvelopeIsStale(target, envelope, openedAt) {
				_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
			}
			continue
		}
		payload, err := relayEnvelopePayload(envelope, 64*1024*1024)
		if err != nil {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
			continue
		}
		var sealed controlwire.SealedFrame
		if err := json.Unmarshal(payload, &sealed); err != nil {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
			continue
		}
		plain, err := cipher.Open(sealed)
		if err != nil {
			_ = relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID)
			return controlwire.PlainFrame{}, err
		}
		if err := relay.AckRelayEnvelope(ctx, envelope.EnvelopeID, target.ControllerDeviceID); err != nil {
			return controlwire.PlainFrame{}, err
		}
		if plain.Type == "close" {
			return controlwire.PlainFrame{}, errors.New(firstString(plain.Reason, plain.Code, "relay control session closed"))
		}
		return plain, nil
	}
}

func discoverLANHostsAt(ctx context.Context, targets []net.UDPAddr) ([]LanHostCandidate, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	request, err := json.Marshal(discoveryPacket{
		Type:    discoveryRequestType,
		Version: controlwire.ProtocolVersion,
	})
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		_, _ = conn.WriteToUDP(request, &target)
	}

	candidates := []LanHostCandidate{}
	seen := map[string]bool{}
	buf := make([]byte, 4096)
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if err := conn.SetReadDeadline(deadline); err != nil {
				return nil, err
			}
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return candidates, nil
			}
			select {
			case <-ctx.Done():
				return candidates, nil
			default:
				return nil, err
			}
		}
		candidate, ok := candidateFromDiscoveryPacket(buf[:n])
		if !ok {
			continue
		}
		key := candidate.DeviceID + "|" + candidate.Host + "|" + strconv.Itoa(candidate.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, candidate)
	}
}

func candidateFromDiscoveryPacket(body []byte) (LanHostCandidate, bool) {
	var packet discoveryPacket
	if err := json.Unmarshal(body, &packet); err != nil {
		return LanHostCandidate{}, false
	}
	if packet.Type != discoveryResponseType || packet.Version != controlwire.ProtocolVersion || packet.Candidate == nil {
		return LanHostCandidate{}, false
	}
	candidate := *packet.Candidate
	if candidate.DeviceID == "" || candidate.PublicKeyFingerprint == "" || candidate.Host == "" || candidate.Port <= 0 {
		return LanHostCandidate{}, false
	}
	hostIP := net.ParseIP(candidate.Host).To4()
	if hostIP == nil {
		return LanHostCandidate{}, false
	}
	candidate.Host = hostIP.String()
	if len(candidate.Addresses) == 0 {
		candidate.Addresses = []string{candidate.Host}
	}
	candidate.BaseURL = "http://" + net.JoinHostPort(candidate.Host, strconv.Itoa(candidate.Port))
	return candidate, true
}

func broadcastTargets(port int) []net.UDPAddr {
	targets := []net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	seen := map[string]bool{net.IPv4bcast.String(): true}
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, mask := ipv4AndMask(addr)
			if ip == nil || mask == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			broadcast := ipv4Broadcast(ip, mask)
			if broadcast == nil || seen[broadcast.String()] {
				continue
			}
			seen[broadcast.String()] = true
			targets = append(targets, net.UDPAddr{IP: broadcast, Port: port})
		}
	}
	return targets
}

func ipv4AndMask(addr net.Addr) (net.IP, net.IPMask) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, nil
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return nil, nil
	}
	return ip, ipNet.Mask
}

func ipv4Broadcast(ip net.IP, mask net.IPMask) net.IP {
	ip = ip.To4()
	if ip == nil || len(mask) < net.IPv4len {
		return nil
	}
	if len(mask) != net.IPv4len {
		mask = mask[len(mask)-net.IPv4len:]
	}
	out := make(net.IP, net.IPv4len)
	for i := range out {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func relayEnvelopePayload(envelope relaymesh.Envelope, limit int64) ([]byte, error) {
	payload, err := base64.StdEncoding.DecodeString(envelope.PayloadBase64)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(payload)) > limit {
		return nil, fmt.Errorf("relay envelope payload too large")
	}
	return payload, nil
}

var relayActiveConnections sync.Map

func registerRelayActive(target ClientTarget, connectionID string) {
	key := relayConnectionKey(target, connectionID)
	if key != "" {
		relayActiveConnections.Store(key, struct{}{})
	}
}

func unregisterRelayActive(target ClientTarget, connectionID string) {
	key := relayConnectionKey(target, connectionID)
	if key != "" {
		relayActiveConnections.Delete(key)
	}
}

func relayConnectionActive(target ClientTarget, connectionID string) bool {
	key := relayConnectionKey(target, connectionID)
	if key == "" {
		return false
	}
	_, ok := relayActiveConnections.Load(key)
	return ok
}

func relayConnectionKey(target ClientTarget, connectionID string) string {
	controllerID := strings.TrimSpace(target.ControllerDeviceID)
	hostID := strings.TrimSpace(target.HostInfo.Identity.DeviceID)
	connectionID = strings.TrimSpace(connectionID)
	if controllerID == "" || hostID == "" || connectionID == "" {
		return ""
	}
	return controllerID + "|" + hostID + "|" + connectionID
}

func relayEnvelopeIsStale(target ClientTarget, envelope relaymesh.Envelope, openedAt time.Time) bool {
	if strings.TrimSpace(envelope.ConnectionID) == "" || relayConnectionActive(target, envelope.ConnectionID) {
		return false
	}
	if openedAt.IsZero() {
		return false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.CreatedAt))
	if err != nil {
		return false
	}
	return createdAt.Before(openedAt)
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

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
