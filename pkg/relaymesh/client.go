package relaymesh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	EnvelopeVersion               = "astralops-relay-envelope-v1"
	PayloadKindControlHello       = "control.hello"
	PayloadKindControlHelloAck    = "control.hello_ack"
	PayloadKindControlSealedFrame = "control.sealed_frame"

	WebSocketPingInterval = 20 * time.Second
	WebSocketPongWait     = 60 * time.Second
	RoundTripTimeout      = 15 * time.Second
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type Envelope struct {
	Version       string `json:"version"`
	EnvelopeID    string `json:"envelope_id,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
	FromDeviceID  string `json:"from_device_id"`
	ToDeviceID    string `json:"to_device_id"`
	PayloadKind   string `json:"payload_kind"`
	PayloadBase64 string `json:"payload_base64"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type EnvelopeListResponse struct {
	Envelopes []Envelope `json:"envelopes"`
}

type EnvelopeAckInput struct {
	DeviceID string `json:"device_id"`
}

type webSocketFrame struct {
	Type     string    `json:"type"`
	Envelope *Envelope `json:"envelope,omitempty"`
	Code     string    `json:"code,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type WebSocketConn struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

func (c Client) EnqueueRelayEnvelope(ctx context.Context, envelope Envelope) (Envelope, error) {
	var out Envelope
	if err := c.do(ctx, http.MethodPost, "/v1/relay/envelopes", envelope, &out); err != nil {
		return Envelope{}, err
	}
	return out, nil
}

func (c Client) ListRelayEnvelopes(ctx context.Context, deviceID string, limit int) ([]Envelope, error) {
	return c.ListRelayEnvelopesWait(ctx, deviceID, limit, 0)
}

func (c Client) ListRelayEnvelopesWait(ctx context.Context, deviceID string, limit int, wait time.Duration) ([]Envelope, error) {
	path := "/v1/relay/envelopes?device_id=" + queryEscape(deviceID)
	if limit > 0 {
		path += "&limit=" + queryEscape(fmt.Sprintf("%d", limit))
	}
	if wait > 0 {
		path += "&wait=" + queryEscape(wait.String())
	}
	var out EnvelopeListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Envelopes, nil
}

func (c Client) AckRelayEnvelope(ctx context.Context, envelopeID, deviceID string) error {
	err := c.do(ctx, http.MethodPost, "/v1/relay/envelopes/"+pathEscape(envelopeID)+"/ack", EnvelopeAckInput{
		DeviceID: strings.TrimSpace(deviceID),
	}, nil)
	if EnvelopeAckAlreadyConsumed(err) {
		return nil
	}
	return err
}

func (c Client) ConnectRelayWebSocket(ctx context.Context, deviceID string) (*WebSocketConn, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("relay websocket device_id required")
	}
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Token) == "" {
		return nil, fmt.Errorf("cloud relay is not configured")
	}
	u, err := WebSocketURL(c.BaseURL, deviceID)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	dialer := *websocket.DefaultDialer
	if deadline, ok := ctx.Deadline(); ok {
		if timeout := time.Until(deadline); timeout > 0 {
			dialer.HandshakeTimeout = timeout
		}
	}
	conn, resp, err := dialer.DialContext(ctx, u, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if len(body) > 0 {
				return nil, fmt.Errorf("relay websocket connect failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
			}
			return nil, fmt.Errorf("relay websocket connect failed: %s", resp.Status)
		}
		return nil, err
	}
	relay := &WebSocketConn{conn: conn, done: make(chan struct{})}
	relay.configureHeartbeat()
	return relay, nil
}

func (c *WebSocketConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		if c.done != nil {
			close(c.done)
		}
		err = c.conn.Close()
	})
	return err
}

func (c *WebSocketConn) configureHeartbeat() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(WebSocketPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(WebSocketPongWait))
	})
	go c.pingLoop()
}

func (c *WebSocketConn) pingLoop() {
	ticker := time.NewTicker(WebSocketPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.writeMu.Lock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(RoundTripTimeout))
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.writeMu.Unlock()
			if err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *WebSocketConn) EnqueueRelayEnvelope(ctx context.Context, envelope Envelope) (Envelope, error) {
	if c == nil || c.conn == nil {
		return Envelope{}, fmt.Errorf("relay websocket is not connected")
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(RoundTripTimeout))
	}
	if err := c.conn.WriteJSON(webSocketFrame{Type: "send", Envelope: &envelope}); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func (c *WebSocketConn) AckRelayEnvelope(context.Context, string, string) error {
	return nil
}

func (c *WebSocketConn) ReadRelayEnvelope(ctx context.Context) (Envelope, error) {
	if c == nil || c.conn == nil {
		return Envelope{}, fmt.Errorf("relay websocket is not connected")
	}
	c.setReadDeadline(ctx)
	for {
		var frame webSocketFrame
		if err := c.conn.ReadJSON(&frame); err != nil {
			return Envelope{}, err
		}
		switch strings.TrimSpace(frame.Type) {
		case "envelope":
			if frame.Envelope == nil {
				continue
			}
			return *frame.Envelope, nil
		case "error":
			return Envelope{}, fmt.Errorf("relay websocket error: %s", firstString(frame.Error, frame.Code, "relay websocket error"))
		default:
			continue
		}
	}
}

func (c *WebSocketConn) setReadDeadline(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(WebSocketPongWait)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = c.conn.SetReadDeadline(deadline)
}

func ValidateEnvelope(envelope Envelope) error {
	if strings.TrimSpace(envelope.Version) != EnvelopeVersion {
		return fmt.Errorf("relay envelope version invalid")
	}
	if strings.TrimSpace(envelope.FromDeviceID) == "" {
		return fmt.Errorf("from_device_id required")
	}
	if strings.TrimSpace(envelope.ToDeviceID) == "" {
		return fmt.Errorf("to_device_id required")
	}
	payloadKind := strings.TrimSpace(envelope.PayloadKind)
	if !IsAllowedPayloadKind(payloadKind) {
		return fmt.Errorf("relay payload kind invalid")
	}
	if payloadKind != PayloadKindControlHello && strings.TrimSpace(envelope.ConnectionID) == "" {
		return fmt.Errorf("connection_id required")
	}
	payload := strings.TrimSpace(envelope.PayloadBase64)
	if payload == "" {
		return fmt.Errorf("payload_base64 required")
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return fmt.Errorf("payload_base64 invalid")
	}
	return nil
}

func IsAllowedPayloadKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case PayloadKindControlHello, PayloadKindControlHelloAck, PayloadKindControlSealedFrame:
		return true
	default:
		return false
	}
}

func EnvelopeAckAlreadyConsumed(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "404 Not Found") && strings.Contains(message, `"code":"relay_envelope_not_found"`)
}

func WebSocketURL(baseURL, deviceID string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("relay websocket url scheme %q is not supported", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/relay/connect"
	query := u.Query()
	query.Set("device_id", strings.TrimSpace(deviceID))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (c Client) do(ctx context.Context, method, path string, body any, out any) error {
	return authedJSONRequest(ctx, "relay", c.BaseURL, c.Token, c.HTTPClient, method, path, body, out)
}

func authedJSONRequest(ctx context.Context, serviceName, baseURLValue, token string, httpClient *http.Client, method, path string, body any, out any) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%s token required", serviceName)
	}
	return jsonRequest(ctx, serviceName, baseURLValue, httpClient, method, path, body, out, map[string]string{"Authorization": "Bearer " + strings.TrimSpace(token)})
}

func jsonRequest(ctx context.Context, serviceName, baseURLValue string, httpClient *http.Client, method, path string, body any, out any, headers map[string]string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(baseURLValue), "/")
	if baseURL == "" {
		return fmt.Errorf("%s base url required", serviceName)
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s request failed: %s: %s", serviceName, res.Status, strings.TrimSpace(string(payload)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func pathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func queryEscape(value string) string {
	return url.QueryEscape(strings.TrimSpace(value))
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
