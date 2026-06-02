package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/oines/astralops/pkg/cloudmesh"
)

type CloudClient = cloudmesh.Client
type cloudDeviceRegistration = cloudmesh.DeviceRegistration
type cloudDeviceHeartbeat = cloudmesh.DeviceHeartbeat
type cloudDeviceListResponse = cloudmesh.DeviceListResponse
type cloudPairingSignalInput = cloudmesh.PairingSignalInput
type CloudPairingSignal = cloudmesh.PairingSignal
type cloudPairingSignalResponse = cloudmesh.PairingSignalResponse
type cloudPairingResolveRequest = cloudmesh.PairingResolveRequest
type cloudPairingSignalListResponse = cloudmesh.PairingSignalListResponse
type cloudLoginCodeExchangeRequest = cloudmesh.LoginCodeExchangeRequest
type cloudLoginCodeExchangeResponse = cloudmesh.LoginCodeExchangeResponse

func ExchangeCloudLoginCode(ctx context.Context, baseURL, loginCode string, identity DeviceIdentity, canHost, canControl bool, httpClient *http.Client) (cloudLoginCodeExchangeResponse, error) {
	return cloudmesh.ExchangeLoginCode(ctx, baseURL, loginCode, identity, canHost, canControl, httpClient)
}

func cloudDeviceRegistrationFromIdentity(identity DeviceIdentity, canHost, canControl bool, relayURL string) cloudDeviceRegistration {
	return cloudmesh.DeviceRegistrationFromIdentity(identity, canHost, canControl, relayURL)
}

func relayClientFromCloudAccount(account CloudAccount, httpClient *http.Client) (RelayClient, CloudRelayConfig, bool) {
	if account.Relay == nil {
		return RelayClient{}, CloudRelayConfig{}, false
	}
	relay := CloudRelayConfig{
		RelayID:             strings.TrimSpace(account.Relay.RelayID),
		RelayURL:            strings.TrimSpace(account.Relay.RelayURL),
		Region:              strings.TrimSpace(account.Relay.Region),
		Name:                strings.TrimSpace(account.Relay.Name),
		Credential:          strings.TrimSpace(account.Relay.Credential),
		CredentialExpiresAt: strings.TrimSpace(account.Relay.CredentialExpiresAt),
	}
	if relay.RelayURL == "" || relay.Credential == "" {
		return RelayClient{}, CloudRelayConfig{}, false
	}
	if relay.RelayID == "" {
		relay.RelayID = "default"
	}
	return RelayClient{BaseURL: relay.RelayURL, Token: relay.Credential, HTTPClient: httpClient}, relay, true
}

func sanitizeCloudRelayList(input CloudRelayListResponse) CloudRelayListResponse {
	return cloudmesh.SanitizeRelayList(input)
}

func authedJSONRequest(ctx context.Context, serviceName, baseURLValue, token string, httpClient *http.Client, method, path string, body any, out any) error {
	return cloudmesh.AuthedJSONRequest(ctx, serviceName, baseURLValue, token, httpClient, method, path, body, out)
}

func jsonRequest(ctx context.Context, serviceName, baseURLValue string, httpClient *http.Client, method, path string, body any, out any, headers map[string]string) error {
	return cloudmesh.JSONRequest(ctx, serviceName, baseURLValue, httpClient, method, path, body, out, headers)
}

func pathEscape(value string) string {
	return cloudmesh.PathEscape(value)
}

func queryEscape(value string) string {
	return cloudmesh.QueryEscape(value)
}
