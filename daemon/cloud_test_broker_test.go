package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oines/astralops/internal/relayauth"
	"github.com/oines/astralops/pkg/cloudmesh"
)

const testRelayCredentialKid = "test-1"

var testRelayCredentialSecret = []byte("test-relay-credential-secret-000000")

const testMembershipKeyID = "test-membership-1"

var (
	testMembershipKeyOnce    sync.Once
	testMembershipPublicKey  ed25519.PublicKey
	testMembershipPrivateKey ed25519.PrivateKey
	testMembershipKeyErr     error
)

func testCloudRelayCredential(t *testing.T, accountIDHash string) string {
	t.Helper()
	now := time.Now().UTC()
	credential, err := relayauth.SignCredential(relayauth.CredentialPayload{
		KeyID:         testRelayCredentialKid,
		RelayID:       "test",
		AccountIDHash: strings.TrimSpace(accountIDHash),
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(10 * time.Minute).Unix(),
	}, testRelayCredentialSecret)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

type testCloudBroker struct {
	mu              sync.Mutex
	token           string
	accountIDHash   string
	membershipKeyID string
	membershipPub   ed25519.PublicKey
	membershipPriv  ed25519.PrivateKey
	relay           *CloudRelayConfig
	relays          []CloudRelayConfig
	currentRelayID  string
	devices         map[string]CloudDeviceRecord
	pairing         []CloudPairingSignal
	nextPairID      int
}

func newTestCloudBrokerServer(t *testing.T, token string) (*testCloudBroker, *httptest.Server) {
	t.Helper()
	membershipPub, membershipPriv := testCloudMembershipSigningKey(t)
	broker := &testCloudBroker{
		token:           strings.TrimSpace(token),
		accountIDHash:   "acct_test",
		membershipKeyID: testMembershipKeyID,
		membershipPub:   membershipPub,
		membershipPriv:  membershipPriv,
		devices:         map[string]CloudDeviceRecord{},
	}
	server := httptest.NewServer(broker.Handler())
	t.Cleanup(server.Close)
	return broker, server
}

func testCloudMembershipSigningKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	testMembershipKeyOnce.Do(func() {
		testMembershipPublicKey, testMembershipPrivateKey, testMembershipKeyErr = ed25519.GenerateKey(rand.Reader)
	})
	if testMembershipKeyErr != nil {
		t.Fatal(testMembershipKeyErr)
	}
	return testMembershipPublicKey, testMembershipPrivateKey
}

func testCloudMembershipSigningPublicKey(t *testing.T) string {
	t.Helper()
	publicKey, _ := testCloudMembershipSigningKey(t)
	return base64.StdEncoding.EncodeToString(publicKey)
}

func testCloudMembershipLeaseForDevice(t *testing.T, accountIDHash, deviceID, publicKeyFingerprint string, canHost, canControl bool) *CloudMembershipLease {
	t.Helper()
	_, privateKey := testCloudMembershipSigningKey(t)
	lease, err := signTestCloudMembershipLease(accountIDHash, deviceID, publicKeyFingerprint, canHost, canControl, testMembershipKeyID, privateKey, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func testCloudMembershipLeaseForIdentity(t *testing.T, accountIDHash string, identity DeviceIdentity, canHost, canControl bool) *CloudMembershipLease {
	t.Helper()
	return testCloudMembershipLeaseForDevice(t, accountIDHash, identity.DeviceID, identity.PublicKeyFingerprint, canHost, canControl)
}

func setTestCloudMembership(t *testing.T, st *store, canHost, canControl bool) cloudMembershipState {
	t.Helper()
	state := cloudMembershipState{
		AccountIDHash:    "acct_test",
		SigningKeyID:     testMembershipKeyID,
		SigningPublicKey: testCloudMembershipSigningPublicKey(t),
		Lease:            testCloudMembershipLeaseForIdentity(t, "acct_test", st.deviceIdentity, canHost, canControl),
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONFile(cloudMembershipPath(st.dataDir), state, defaultHostFileMode); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.cloudMembership = state
	st.mu.Unlock()
	return state
}

func signTestCloudMembershipLease(accountIDHash, deviceID, publicKeyFingerprint string, canHost, canControl bool, keyID string, privateKey ed25519.PrivateKey, now time.Time) (*CloudMembershipLease, error) {
	payload := struct {
		AccountIDHash        string `json:"account_id_hash"`
		DeviceID             string `json:"device_id"`
		PublicKeyFingerprint string `json:"public_key_fingerprint"`
		CanHost              bool   `json:"can_host"`
		CanControl           bool   `json:"can_control"`
		MeshEpoch            int64  `json:"mesh_epoch"`
		IssuedAt             int64  `json:"iat"`
		ExpiresAt            int64  `json:"exp"`
	}{
		AccountIDHash:        strings.TrimSpace(accountIDHash),
		DeviceID:             strings.TrimSpace(deviceID),
		PublicKeyFingerprint: strings.TrimSpace(publicKeyFingerprint),
		CanHost:              canHost,
		CanControl:           canControl,
		MeshEpoch:            1,
		IssuedAt:             now.Unix(),
		ExpiresAt:            now.Add(24 * time.Hour).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(privateKey, []byte(payloadPart))
	return &CloudMembershipLease{
		Version:       cloudmesh.MembershipLeaseVersion,
		Algorithm:     cloudmesh.MembershipLeaseAlgorithm,
		KeyID:         keyID,
		PayloadBase64: payloadPart,
		Signature:     base64.RawURLEncoding.EncodeToString(signature),
	}, nil
}

func (b *testCloudBroker) SetDefaultRelay(config CloudRelayConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setDefaultRelayLocked(config)
}

func (b *testCloudBroker) setDefaultRelayLocked(config CloudRelayConfig) {
	config.RelayID = strings.TrimSpace(config.RelayID)
	config.RelayURL = strings.TrimSpace(config.RelayURL)
	if config.RelayURL == "" {
		b.relay = nil
		b.relays = nil
		b.currentRelayID = ""
		return
	}
	if config.RelayID == "" {
		config.RelayID = "test"
	}
	b.relay = &config
	b.relays = []CloudRelayConfig{config}
	b.currentRelayID = config.RelayID
}

func (b *testCloudBroker) SetRelays(currentRelayID string, relays ...CloudRelayConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.relays = nil
	b.relay = nil
	b.currentRelayID = strings.TrimSpace(currentRelayID)
	for _, relay := range relays {
		relay.RelayID = strings.TrimSpace(relay.RelayID)
		relay.RelayURL = strings.TrimSpace(relay.RelayURL)
		if relay.RelayID == "" || relay.RelayURL == "" {
			continue
		}
		b.relays = append(b.relays, relay)
		if b.currentRelayID == "" {
			b.currentRelayID = relay.RelayID
		}
	}
	b.relay = b.relayByIDLocked(b.currentRelayID)
}

func (b *testCloudBroker) relayByIDLocked(relayID string) *CloudRelayConfig {
	for _, relay := range b.relays {
		if relay.RelayID == relayID {
			next := relay
			return &next
		}
	}
	return nil
}

func (b *testCloudBroker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/account", b.withAccount(b.handleAccount))
	mux.HandleFunc("/v1/account/relay", b.withAccount(b.handleAccountRelay))
	mux.HandleFunc("/v1/relays", b.withAccount(b.handleRelays))
	mux.HandleFunc("/v1/devices", b.withAccount(b.handleDevices))
	mux.HandleFunc("/v1/devices/", b.withAccount(b.handleDeviceAction))
	mux.HandleFunc("/v1/pairing/requests", b.withAccount(b.handlePairingRequests))
	mux.HandleFunc("/v1/pairing/requests/", b.withAccount(b.handlePairingRequestAction))
	return mux
}

func (b *testCloudBroker) withAccount(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if header != "Bearer "+b.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "error": "missing bearer token"})
			return
		}
		next(w, r)
	}
}

func (b *testCloudBroker) handleRelays(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b.mu.Lock()
	relays := make([]CloudRelayConfig, 0, len(b.relays))
	for _, relay := range b.relays {
		relay.Credential = ""
		relay.CredentialExpiresAt = ""
		relays = append(relays, relay)
	}
	currentRelayID := b.currentRelayID
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, CloudRelayListResponse{Relays: relays, CurrentRelayID: currentRelayID})
}

func (b *testCloudBroker) handleAccountRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input CloudRelayUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
		return
	}
	b.mu.Lock()
	relay := b.relayByIDLocked(strings.TrimSpace(input.RelayID))
	if relay == nil {
		b.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "relay_not_found", "error": "relay not found"})
		return
	}
	b.currentRelayID = relay.RelayID
	b.relay = relay
	b.mu.Unlock()
	b.writeAccount(w)
}

func (b *testCloudBroker) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b.writeAccount(w)
}

func (b *testCloudBroker) writeAccount(w http.ResponseWriter) {
	b.mu.Lock()
	account := CloudAccount{
		AccountIDHash:              b.accountIDHash,
		MembershipKeyID:            b.membershipKeyID,
		MembershipSigningPublicKey: base64.StdEncoding.EncodeToString(b.membershipPub),
	}
	if b.relay != nil {
		relay := *b.relay
		now := time.Now().UTC()
		credential, err := relayauth.SignCredential(relayauth.CredentialPayload{
			KeyID:         testRelayCredentialKid,
			RelayID:       relay.RelayID,
			AccountIDHash: b.accountIDHash,
			IssuedAt:      now.Unix(),
			ExpiresAt:     now.Add(10 * time.Minute).Unix(),
		}, testRelayCredentialSecret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "relay_credential_failed", "error": err.Error()})
			b.mu.Unlock()
			return
		}
		relay.Credential = credential
		relay.CredentialExpiresAt = now.Add(10 * time.Minute).Format(time.RFC3339)
		account.Relay = &relay
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, account)
}

func (b *testCloudBroker) handleDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b.mu.Lock()
		devices := make([]CloudDeviceRecord, 0, len(b.devices))
		for _, device := range b.devices {
			devices = append(devices, device)
		}
		b.mu.Unlock()
		sort.Slice(devices, func(i, j int) bool {
			if devices[i].Status != devices[j].Status {
				return devices[i].Status == cloudDeviceStatusOnline
			}
			return devices[i].DeviceID < devices[j].DeviceID
		})
		writeJSON(w, http.StatusOK, cloudDeviceListResponse{Devices: devices})
	case http.MethodPost:
		var input cloudDeviceRegistration
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
			return
		}
		record, err := b.registerDevice(input)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "device_invalid", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (b *testCloudBroker) handleDeviceAction(w http.ResponseWriter, r *http.Request) {
	parts := testPathParts(strings.TrimPrefix(r.URL.Path, "/v1/devices/"))
	if len(parts) != 2 || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch parts[1] {
	case "heartbeat":
		var input cloudDeviceHeartbeat
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
			return
		}
		record, status, err := b.updateDeviceStatus(parts[0], cloudDeviceStatusOnline, input.RelayURL)
		if err != nil {
			writeJSON(w, status, map[string]string{"code": "device_update_failed", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	case "offline":
		record, status, err := b.updateDeviceStatus(parts[0], cloudDeviceStatusOffline, "")
		if err != nil {
			writeJSON(w, status, map[string]string{"code": "device_update_failed", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	case "remove":
		record, status, err := b.removeDevice(parts[0])
		if err != nil {
			writeJSON(w, status, map[string]string{"code": "device_remove_failed", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (b *testCloudBroker) handlePairingRequests(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		b.mu.Lock()
		requests := make([]CloudPairingSignal, 0, len(b.pairing))
		for _, request := range b.pairing {
			if deviceID == "" || request.HostDeviceID == deviceID || request.ControllerDeviceID == deviceID {
				requests = append(requests, request)
			}
		}
		b.mu.Unlock()
		writeJSON(w, http.StatusOK, cloudPairingSignalListResponse{Requests: requests})
	case http.MethodPost:
		var input cloudPairingSignalInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
			return
		}
		request, status, err := b.createPairingRequest(input)
		if err != nil {
			writeJSON(w, status, map[string]string{"code": "pairing_failed", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, cloudPairingSignalResponse{Request: request})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (b *testCloudBroker) handlePairingRequestAction(w http.ResponseWriter, r *http.Request) {
	parts := testPathParts(strings.TrimPrefix(r.URL.Path, "/v1/pairing/requests/"))
	if len(parts) != 2 || parts[1] != "resolve" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var input cloudPairingResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json", "error": err.Error()})
		return
	}
	request, status, err := b.resolvePairingRequest(parts[0], input)
	if err != nil {
		writeJSON(w, status, map[string]string{"code": "pairing_resolve_failed", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cloudPairingSignalResponse{Request: request})
}

func (b *testCloudBroker) registerDevice(input cloudDeviceRegistration) (CloudDeviceRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := CloudDeviceRecord{
		AccountIDHash:        b.accountIDHash,
		DeviceID:             strings.TrimSpace(input.DeviceID),
		DeviceName:           strings.TrimSpace(input.DeviceName),
		DeviceKind:           strings.TrimSpace(input.DeviceKind),
		PublicKey:            strings.TrimSpace(input.PublicKey),
		PublicKeyFingerprint: strings.TrimSpace(input.PublicKeyFingerprint),
		Capabilities:         normalizeCapabilities(input.Capabilities),
		CanHost:              input.CanHost,
		CanControl:           input.CanControl,
		Status:               cloudDeviceStatusOnline,
		RelayURL:             strings.TrimSpace(input.RelayURL),
		LastSeen:             now,
		UpdatedAt:            now,
	}
	if err := validateCloudDeviceRecord(record); err != nil {
		return CloudDeviceRecord{}, err
	}
	if !record.CanHost && !record.CanControl {
		return CloudDeviceRecord{}, errTestCloudBroker("device must be able to host or control")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.devices[record.DeviceID]; ok && existing.Status == cloudDeviceStatusRevoked {
		return CloudDeviceRecord{}, errTestCloudBroker("device has been removed from mesh")
	}
	b.devices[record.DeviceID] = record
	return b.attachMembershipLease(record)
}

func (b *testCloudBroker) updateDeviceStatus(deviceID, status, relayURL string) (CloudDeviceRecord, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.devices[strings.TrimSpace(deviceID)]
	if !ok {
		return CloudDeviceRecord{}, http.StatusNotFound, errTestCloudBroker("device not found")
	}
	if record.Status == cloudDeviceStatusRevoked && status != cloudDeviceStatusRevoked {
		return CloudDeviceRecord{}, http.StatusForbidden, errTestCloudBroker("device has been removed from cloud mesh")
	}
	record.Status = status
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if status == cloudDeviceStatusOnline {
		record.LastSeen = record.UpdatedAt
		if strings.TrimSpace(relayURL) != "" {
			record.RelayURL = strings.TrimSpace(relayURL)
		}
	}
	b.devices[record.DeviceID] = record
	if status == cloudDeviceStatusOnline {
		withLease, err := b.attachMembershipLease(record)
		if err != nil {
			return CloudDeviceRecord{}, http.StatusInternalServerError, err
		}
		return withLease, http.StatusOK, nil
	}
	return record, http.StatusOK, nil
}

func (b *testCloudBroker) attachMembershipLease(record CloudDeviceRecord) (CloudDeviceRecord, error) {
	lease, err := signTestCloudMembershipLease(record.AccountIDHash, record.DeviceID, record.PublicKeyFingerprint, record.CanHost, record.CanControl, b.membershipKeyID, b.membershipPriv, time.Now().UTC())
	if err != nil {
		return CloudDeviceRecord{}, err
	}
	record.MembershipLease = lease
	return record, nil
}

func (b *testCloudBroker) removeDevice(deviceID string) (CloudDeviceRecord, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.devices[strings.TrimSpace(deviceID)]
	if !ok {
		return CloudDeviceRecord{}, http.StatusNotFound, errTestCloudBroker("device not found")
	}
	record.Status = cloudDeviceStatusRevoked
	record.RelayURL = ""
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	b.devices[record.DeviceID] = record
	for i, request := range b.pairing {
		if request.Status != PairingStatusPending {
			continue
		}
		if request.HostDeviceID == record.DeviceID || request.ControllerDeviceID == record.DeviceID {
			request.Status = PairingStatusDenied
			request.ResolverDeviceID = record.DeviceID
			request.ResolvedAt = record.UpdatedAt
			request.UpdatedAt = record.UpdatedAt
			b.pairing[i] = request
		}
	}
	return record, http.StatusOK, nil
}

func (b *testCloudBroker) createPairingRequest(input cloudPairingSignalInput) (CloudPairingSignal, int, error) {
	hostID := strings.TrimSpace(input.HostDeviceID)
	controllerID := strings.TrimSpace(input.ControllerDeviceID)
	if hostID == "" || controllerID == "" || hostID == controllerID {
		return CloudPairingSignal{}, http.StatusBadRequest, errTestCloudBroker("invalid pairing devices")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	host, hostOK := b.devices[hostID]
	controller, controllerOK := b.devices[controllerID]
	if !hostOK || !controllerOK {
		return CloudPairingSignal{}, http.StatusNotFound, errTestCloudBroker("host or controller device not found")
	}
	if host.Status == cloudDeviceStatusRevoked || controller.Status == cloudDeviceStatusRevoked {
		return CloudPairingSignal{}, http.StatusForbidden, errTestCloudBroker("host or controller device has been removed from mesh")
	}
	if !host.CanHost || !controller.CanControl {
		return CloudPairingSignal{}, http.StatusBadRequest, errTestCloudBroker("device roles do not allow pairing")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, existing := range b.pairing {
		if existing.HostDeviceID == hostID && existing.ControllerDeviceID == controllerID && existing.Status == PairingStatusPending {
			existing.Scope = strings.TrimSpace(input.Scope)
			if existing.Scope == "" {
				existing.Scope = TrustScopeFull
			}
			existing.Capabilities = normalizeCapabilities(input.Capabilities)
			existing.WorkspaceExecPolicy = strings.TrimSpace(input.WorkspaceExecPolicy)
			existing.UpdatedAt = now
			b.pairing[i] = existing
			return existing, http.StatusAccepted, nil
		}
	}
	b.nextPairID++
	request := CloudPairingSignal{
		RequestID:                      "pair_" + strconv.Itoa(b.nextPairID),
		AccountIDHash:                  b.accountIDHash,
		HostDeviceID:                   host.DeviceID,
		HostDeviceName:                 host.DeviceName,
		HostDeviceKind:                 host.DeviceKind,
		HostPublicKeyFingerprint:       host.PublicKeyFingerprint,
		ControllerDeviceID:             controller.DeviceID,
		ControllerDeviceName:           controller.DeviceName,
		ControllerDeviceKind:           controller.DeviceKind,
		ControllerPublicKeyFingerprint: controller.PublicKeyFingerprint,
		Scope:                          strings.TrimSpace(input.Scope),
		Status:                         PairingStatusPending,
		Capabilities:                   normalizeCapabilities(input.Capabilities),
		WorkspaceExecPolicy:            strings.TrimSpace(input.WorkspaceExecPolicy),
		CreatedAt:                      now,
		UpdatedAt:                      now,
	}
	if request.Scope == "" {
		request.Scope = TrustScopeFull
	}
	b.pairing = append(b.pairing, request)
	return request, http.StatusAccepted, nil
}

func (b *testCloudBroker) resolvePairingRequest(requestID string, input cloudPairingResolveRequest) (CloudPairingSignal, int, error) {
	status := strings.TrimSpace(input.Status)
	if status != PairingStatusApproved && status != PairingStatusDenied {
		return CloudPairingSignal{}, http.StatusBadRequest, errTestCloudBroker("invalid pairing status")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, request := range b.pairing {
		if request.RequestID != strings.TrimSpace(requestID) {
			continue
		}
		if request.Status != PairingStatusPending {
			return CloudPairingSignal{}, http.StatusConflict, errTestCloudBroker("pairing request is already resolved")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		request.Status = status
		request.ResolverDeviceID = strings.TrimSpace(input.ResolverDeviceID)
		request.ResolvedAt = now
		request.UpdatedAt = now
		b.pairing[i] = request
		return request, http.StatusOK, nil
	}
	return CloudPairingSignal{}, http.StatusNotFound, errTestCloudBroker("pairing request not found")
}

type errTestCloudBroker string

func (e errTestCloudBroker) Error() string {
	return string(e)
}

func testPathParts(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
