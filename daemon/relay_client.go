package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
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
