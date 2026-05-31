package controllercore

import (
	"context"
	"net/http"
	"strings"
)

const (
	PairingStatusPending  = "pending"
	PairingStatusApproved = "approved"
	PairingStatusDenied   = "denied"
)

type MeshState struct {
	Self                MeshSelfState      `json:"self"`
	Cloud               *MeshCloudState    `json:"cloud,omitempty"`
	Hosts               []RemoteHostRecord `json:"hosts"`
	PendingPairingCount int                `json:"pending_pairing_count"`
	UpdatedAt           string             `json:"updated_at"`
}

type MeshSelfState struct {
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name"`
	CanHost        bool   `json:"can_host"`
	CanControl     bool   `json:"can_control"`
	CloudActive    bool   `json:"cloud_active"`
	RelayConnected bool   `json:"relay_connected"`
}

type MeshCloudState struct {
	Enabled             bool   `json:"enabled"`
	AccountIDHash       string `json:"account_id_hash,omitempty"`
	RelayID             string `json:"relay_id,omitempty"`
	RelayURL            string `json:"relay_url,omitempty"`
	CredentialExpiresAt string `json:"credential_expires_at,omitempty"`
}

type RemoteHostRecord struct {
	DeviceID             string       `json:"device_id"`
	DeviceName           string       `json:"device_name,omitempty"`
	DeviceKind           string       `json:"device_kind,omitempty"`
	PublicKeyFingerprint string       `json:"public_key_fingerprint"`
	KnownIdentity        bool         `json:"known_identity,omitempty"`
	Status               string       `json:"status"`
	Connection           string       `json:"connection"`
	AuthorizationState   string       `json:"authorization_state,omitempty"`
	PairingRequestID     string       `json:"pairing_request_id,omitempty"`
	PairingStatus        string       `json:"pairing_status,omitempty"`
	LastBaseURL          string       `json:"last_base_url,omitempty"`
	LANBaseURL           string       `json:"lan_base_url,omitempty"`
	Capabilities         []string     `json:"capabilities,omitempty"`
	Control              ControlState `json:"control"`
}

type PairingSignal struct {
	RequestID                      string   `json:"request_id"`
	AccountIDHash                  string   `json:"account_id_hash,omitempty"`
	HostDeviceID                   string   `json:"host_device_id"`
	HostDeviceName                 string   `json:"host_device_name,omitempty"`
	HostDeviceKind                 string   `json:"host_device_kind,omitempty"`
	HostPublicKeyFingerprint       string   `json:"host_public_key_fingerprint,omitempty"`
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope"`
	Status                         string   `json:"status"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
	ResolverDeviceID               string   `json:"resolver_device_id,omitempty"`
	CreatedAt                      string   `json:"created_at,omitempty"`
	UpdatedAt                      string   `json:"updated_at,omitempty"`
	ResolvedAt                     string   `json:"resolved_at,omitempty"`
}

type MeshTransport interface {
	MeshState(ctx context.Context, discover bool) (MeshState, error)
	RequestPairing(ctx context.Context, hostDeviceID string) (PairingSignal, error)
}

func (c *Controller) MeshState(ctx context.Context, discover bool) (MeshState, error) {
	transport, err := c.meshTransport()
	if err != nil {
		return MeshState{}, err
	}
	return transport.MeshState(ctx, discover)
}

func (c *Controller) RequestPairing(ctx context.Context, hostDeviceID string) (PairingSignal, error) {
	hostDeviceID = strings.TrimSpace(hostDeviceID)
	if hostDeviceID == "" {
		return PairingSignal{}, NewActionError(http.StatusBadRequest, "remote_host_required", "remote Host device id is required")
	}
	transport, err := c.meshTransport()
	if err != nil {
		return PairingSignal{}, err
	}
	return transport.RequestPairing(ctx, hostDeviceID)
}

func (c *Controller) meshTransport() (MeshTransport, error) {
	if c == nil || c.transport == nil {
		return nil, NewActionError(http.StatusServiceUnavailable, "controller_transport_unavailable", "controller transport is not initialized")
	}
	transport, ok := c.transport.(MeshTransport)
	if !ok {
		return nil, NewActionError(http.StatusNotImplemented, "controller_mesh_unavailable", "controller mesh transport is not wired")
	}
	return transport, nil
}
