package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type RelayClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type relayEnvelopeListResponse struct {
	Envelopes []RelayEnvelope `json:"envelopes"`
}

type relayEnvelopeAckInput struct {
	DeviceID string `json:"device_id"`
}

type relayWebSocketFrame struct {
	Type     string         `json:"type"`
	Envelope *RelayEnvelope `json:"envelope,omitempty"`
	Code     string         `json:"code,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type RelayWebSocketConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c RelayClient) EnqueueRelayEnvelope(ctx context.Context, envelope RelayEnvelope) (RelayEnvelope, error) {
	var out RelayEnvelope
	if err := c.do(ctx, http.MethodPost, "/v1/relay/envelopes", envelope, &out); err != nil {
		return RelayEnvelope{}, err
	}
	return out, nil
}

func (c RelayClient) ListRelayEnvelopes(ctx context.Context, deviceID string, limit int) ([]RelayEnvelope, error) {
	return c.ListRelayEnvelopesWait(ctx, deviceID, limit, 0)
}

func (c RelayClient) ListRelayEnvelopesWait(ctx context.Context, deviceID string, limit int, wait time.Duration) ([]RelayEnvelope, error) {
	path := "/v1/relay/envelopes?device_id=" + queryEscape(deviceID)
	if limit > 0 {
		path += "&limit=" + queryEscape(fmt.Sprintf("%d", limit))
	}
	if wait > 0 {
		path += "&wait=" + queryEscape(wait.String())
	}
	var out relayEnvelopeListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Envelopes, nil
}

func (c RelayClient) AckRelayEnvelope(ctx context.Context, envelopeID, deviceID string) error {
	err := c.do(ctx, http.MethodPost, "/v1/relay/envelopes/"+pathEscape(envelopeID)+"/ack", relayEnvelopeAckInput{
		DeviceID: strings.TrimSpace(deviceID),
	}, nil)
	if relayEnvelopeAckAlreadyConsumed(err) {
		return nil
	}
	return err
}

func (c RelayClient) ConnectRelayWebSocket(ctx context.Context, deviceID string) (*RelayWebSocketConn, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("relay websocket device_id required")
	}
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Token) == "" {
		return nil, fmt.Errorf("cloud relay is not configured")
	}
	u, err := relayWebSocketURL(c.BaseURL, deviceID)
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
	return &RelayWebSocketConn{conn: conn}, nil
}

func (c *RelayWebSocketConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *RelayWebSocketConn) EnqueueRelayEnvelope(ctx context.Context, envelope RelayEnvelope) (RelayEnvelope, error) {
	if c == nil || c.conn == nil {
		return RelayEnvelope{}, fmt.Errorf("relay websocket is not connected")
	}
	if err := validateRelayEnvelope(envelope); err != nil {
		return RelayEnvelope{}, err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(controlRelayRoundTripTimeout))
	}
	if err := c.conn.WriteJSON(relayWebSocketFrame{Type: "send", Envelope: &envelope}); err != nil {
		return RelayEnvelope{}, err
	}
	return envelope, nil
}

func (c *RelayWebSocketConn) AckRelayEnvelope(context.Context, string, string) error {
	return nil
}

func (c *RelayWebSocketConn) ReadRelayEnvelope(ctx context.Context) (RelayEnvelope, error) {
	if c == nil || c.conn == nil {
		return RelayEnvelope{}, fmt.Errorf("relay websocket is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetReadDeadline(deadline)
	} else {
		_ = c.conn.SetReadDeadline(time.Time{})
	}
	for {
		var frame relayWebSocketFrame
		if err := c.conn.ReadJSON(&frame); err != nil {
			return RelayEnvelope{}, err
		}
		switch strings.TrimSpace(frame.Type) {
		case "envelope":
			if frame.Envelope == nil {
				continue
			}
			return *frame.Envelope, nil
		case "error":
			return RelayEnvelope{}, fmt.Errorf("relay websocket error: %s", firstString(frame.Error, frame.Code, "relay websocket error"))
		default:
			continue
		}
	}
}

func (c RelayClient) do(ctx context.Context, method, path string, body any, out any) error {
	return authedJSONRequest(ctx, "relay", c.BaseURL, c.Token, c.HTTPClient, method, path, body, out)
}

func relayEnvelopeAckAlreadyConsumed(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "404 Not Found") && strings.Contains(message, `"code":"relay_envelope_not_found"`)
}

func relayWebSocketURL(baseURL, deviceID string) (string, error) {
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
