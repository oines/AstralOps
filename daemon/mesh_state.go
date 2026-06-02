package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

const meshStateRefreshTimeout = 6 * time.Second

type meshStateResponse struct {
	Self                meshSelfState      `json:"self"`
	Cloud               *meshCloudState    `json:"cloud,omitempty"`
	Hosts               []remoteHostRecord `json:"hosts"`
	PendingPairingCount int                `json:"pending_pairing_count"`
	UpdatedAt           string             `json:"updated_at"`
}

type meshSelfState struct {
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name"`
	CanHost        bool   `json:"can_host"`
	CanControl     bool   `json:"can_control"`
	CloudActive    bool   `json:"cloud_active"`
	RelayConnected bool   `json:"relay_connected"`
}

type meshCloudState struct {
	Enabled             bool   `json:"enabled"`
	AccountIDHash       string `json:"account_id_hash,omitempty"`
	RelayID             string `json:"relay_id,omitempty"`
	RelayURL            string `json:"relay_url,omitempty"`
	CredentialExpiresAt string `json:"credential_expires_at,omitempty"`
}

type meshStateManager struct {
	deps meshStateDeps

	mu          sync.Mutex
	state       meshStateResponse
	subscribers map[chan meshStateResponse]struct{}
}

type meshStateDeps struct {
	hostInfo               func() HostInfo
	currentSettings        func() AppSettings
	cloudMeshActiveFor     func(cloudMembershipRole) bool
	cloudMeshActive        func() bool
	currentRelayConnected  func() bool
	buildRemoteHostRecords func(context.Context, bool) []remoteHostRecord
	pairingRequests        func() []PairingRequest
	meshCloudState         func(context.Context, CloudSettings) *meshCloudState
}

func newMeshStateManager(deps meshStateDeps) *meshStateManager {
	return &meshStateManager{
		deps:        deps,
		subscribers: map[chan meshStateResponse]struct{}{},
	}
}

func meshStateDepsFromApp(a *app) meshStateDeps {
	if a == nil {
		return meshStateDeps{}
	}
	cloud := a.cloudmeshService()
	remote := a.remoteControlService()
	deps := meshStateDeps{
		currentSettings:        a.currentSettings,
		cloudMeshActiveFor:     cloud.cloudMeshActiveFor,
		cloudMeshActive:        cloud.cloudMeshActive,
		currentRelayConnected:  cloud.currentCloudRelayConnected,
		buildRemoteHostRecords: remote.buildRemoteHostRecords,
		meshCloudState:         cloud.meshCloudState,
	}
	if a.store != nil {
		deps.hostInfo = func() HostInfo { return a.store.hostInfo() }
		deps.pairingRequests = a.store.listPairingRequests
	}
	return deps
}

func (a *app) handleMeshState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.mesh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mesh state is not initialized"})
		return
	}
	if truthyQuery(r.URL.Query().Get("stream")) {
		a.mesh.stream(w, r)
		return
	}
	state, err := a.mesh.refresh(r.Context(), true)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (m *meshStateManager) refresh(ctx context.Context, discover bool) (meshStateResponse, error) {
	if m == nil || m.deps.hostInfo == nil {
		return meshStateResponse{}, nil
	}
	buildCtx, cancel := context.WithTimeout(ctx, meshStateRefreshTimeout)
	defer cancel()
	state := m.build(buildCtx, discover)
	m.mu.Lock()
	m.state = state
	subscribers := make([]chan meshStateResponse, 0, len(m.subscribers))
	for subscriber := range m.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	m.mu.Unlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- state:
		default:
		}
	}
	return state, nil
}

func (m *meshStateManager) build(ctx context.Context, discover bool) meshStateResponse {
	info := m.deps.hostInfo()
	settings := m.deps.currentSettings()
	self := meshSelfState{
		DeviceID:       info.Identity.DeviceID,
		DeviceName:     info.Identity.DeviceName,
		CanHost:        m.deps.cloudMeshActiveFor(cloudMembershipRole{CanHost: true}) && settings.RemoteControl.Enabled,
		CanControl:     m.deps.cloudMeshActiveFor(cloudMembershipRole{CanControl: true}),
		CloudActive:    m.deps.cloudMeshActive(),
		RelayConnected: m.deps.currentRelayConnected(),
	}
	state := meshStateResponse{
		Self:                self,
		Hosts:               m.deps.buildRemoteHostRecords(ctx, discover),
		PendingPairingCount: pendingPairingRequestCount(m.deps.pairingRequests()),
		UpdatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	state.Cloud = m.deps.meshCloudState(ctx, settings.Cloud)
	return state
}

func (m *meshStateManager) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan meshStateResponse, 4)
	m.mu.Lock()
	m.subscribers[ch] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.subscribers, ch)
		m.mu.Unlock()
		close(ch)
	}()

	if state, err := m.refresh(r.Context(), true); err == nil {
		writeSSE(w, flusher, "mesh-state", state)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case state := <-ch:
			writeSSE(w, flusher, "mesh-state", state)
		case <-ticker.C:
			state, err := m.refresh(r.Context(), true)
			if err != nil {
				writeSSE(w, flusher, "mesh-error", map[string]string{"error": err.Error()})
				continue
			}
			writeSSE(w, flusher, "mesh-state", state)
		}
	}
}

func (a *app) refreshMeshStateAsync(discover bool) {
	if a == nil || a.mesh == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), meshStateRefreshTimeout)
		defer cancel()
		_, _ = a.mesh.refresh(ctx, discover)
	}()
}

func (a *cloudmeshService) meshCloudState(ctx context.Context, settings CloudSettings) *meshCloudState {
	cloud := &meshCloudState{Enabled: settings.Enabled}
	if a == nil || a.store == nil {
		return cloud
	}
	if membership, err := a.store.currentCloudMembership(cloudMembershipRole{}); err == nil {
		cloud.AccountIDHash = membership.AccountIDHash
	}
	if !settings.Enabled || validateCloudSettings(settings) != nil {
		return cloud
	}
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	accountCtx, cancel := context.WithTimeout(ctx, remoteHostCloudTimeout)
	defer cancel()
	account, err := client.GetAccount(accountCtx)
	if err != nil {
		return cloud
	}
	if strings.TrimSpace(account.AccountIDHash) != "" {
		cloud.AccountIDHash = strings.TrimSpace(account.AccountIDHash)
	}
	if account.Relay != nil {
		cloud.RelayID = strings.TrimSpace(account.Relay.RelayID)
		if cloud.RelayID == "" {
			cloud.RelayID = "default"
		}
		cloud.RelayURL = strings.TrimSpace(account.Relay.RelayURL)
		cloud.CredentialExpiresAt = strings.TrimSpace(account.Relay.CredentialExpiresAt)
	}
	return cloud
}

func pendingPairingRequestCount(requests []PairingRequest) int {
	count := 0
	for _, request := range requests {
		if strings.TrimSpace(request.Status) == PairingStatusPending {
			count++
		}
	}
	return count
}

func (a *cloudmeshService) setCloudRelayConnected(connected bool) {
	if a == nil {
		return
	}
	a.cloudMu.Lock()
	changed := a.cloudRelayConnected != nil && *a.cloudRelayConnected != connected
	if a.cloudRelayConnected != nil {
		*a.cloudRelayConnected = connected
	}
	a.cloudMu.Unlock()
	if changed && a.refreshMeshStateAsync != nil {
		a.refreshMeshStateAsync(true)
	}
}

func (a *cloudmeshService) currentCloudRelayConnected() bool {
	if a == nil {
		return false
	}
	a.cloudMu.Lock()
	defer a.cloudMu.Unlock()
	return a.cloudRelayConnected != nil && *a.cloudRelayConnected
}
