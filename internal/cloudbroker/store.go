package cloudbroker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileStore struct {
	mu    sync.Mutex
	path  string
	state cloudState
}

type cloudState struct {
	Devices         []DeviceRecord   `json:"devices"`
	PairingRequests []PairingRequest `json:"pairing_requests"`
	RelayEnvelopes  []RelayEnvelope  `json:"relay_envelopes,omitempty"`
}

func LoadFileStore(path string) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("cloud store path required")
	}
	store := &FileStore{path: path}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		store.state = cloudState{}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		store.state = cloudState{}
		return store, nil
	}
	if err := json.Unmarshal(body, &store.state); err != nil {
		return nil, err
	}
	store.normalizeLocked()
	return store, nil
}

func (s *FileStore) RegisterDevice(account Account, input DeviceRegistration, now time.Time) (DeviceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing *DeviceRecord
	for i := range s.state.Devices {
		if s.state.Devices[i].AccountIDHash == account.AccountIDHash && s.state.Devices[i].DeviceID == strings.TrimSpace(input.DeviceID) {
			existing = &s.state.Devices[i]
			break
		}
	}
	if existing != nil && existing.Status == DeviceStatusRevoked {
		return DeviceRecord{}, apiErr(403, "device_revoked", "device has been removed from mesh")
	}
	record, err := validateDeviceRegistration(account.AccountIDHash, input, existing, now)
	if err != nil {
		return DeviceRecord{}, err
	}
	if existing != nil {
		*existing = record
	} else {
		s.state.Devices = append(s.state.Devices, record)
	}
	s.normalizeLocked()
	if err := s.writeLocked(); err != nil {
		return DeviceRecord{}, err
	}
	return record, nil
}

func (s *FileStore) ListDevices(account Account) []DeviceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeviceRecord, 0)
	for _, device := range s.state.Devices {
		if device.AccountIDHash == account.AccountIDHash {
			out = append(out, device)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == DeviceStatusOnline
		}
		return strings.ToLower(out[i].DeviceName) < strings.ToLower(out[j].DeviceName)
	})
	return out
}

func (s *FileStore) HeartbeatDevice(account Account, deviceID string, input DeviceHeartbeat, now time.Time) (DeviceRecord, error) {
	return s.updateDeviceStatus(account, deviceID, DeviceStatusOnline, strings.TrimSpace(input.RelayURL), now)
}

func (s *FileStore) MarkDeviceOffline(account Account, deviceID string, now time.Time) (DeviceRecord, error) {
	return s.updateDeviceStatus(account, deviceID, DeviceStatusOffline, "", now)
}

func (s *FileStore) RemoveDevice(account Account, deviceID string, now time.Time) (DeviceRecord, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return DeviceRecord{}, apiErr(400, "device_id_required", "device_id required")
	}
	nowText := now.Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, device := range s.state.Devices {
		if device.AccountIDHash != account.AccountIDHash || device.DeviceID != deviceID {
			continue
		}
		device.Status = DeviceStatusRevoked
		device.RelayURL = ""
		device.UpdatedAt = nowText
		if err := validateDeviceRecord(device); err != nil {
			return DeviceRecord{}, err
		}
		s.state.Devices[i] = device
		for j, request := range s.state.PairingRequests {
			if request.AccountIDHash != account.AccountIDHash || request.Status != PairingStatusPending {
				continue
			}
			if request.HostDeviceID != deviceID && request.ControllerDeviceID != deviceID {
				continue
			}
			request.Status = PairingStatusDenied
			request.ResolverDeviceID = deviceID
			request.ResolvedAt = nowText
			request.UpdatedAt = nowText
			s.state.PairingRequests[j] = request
		}
		s.state.RelayEnvelopes = filterRelayEnvelopes(s.state.RelayEnvelopes, func(envelope RelayEnvelope) bool {
			if envelope.AccountIDHash != account.AccountIDHash {
				return true
			}
			return envelope.FromDeviceID != deviceID && envelope.ToDeviceID != deviceID
		})
		if err := s.writeLocked(); err != nil {
			return DeviceRecord{}, err
		}
		return device, nil
	}
	return DeviceRecord{}, apiErr(404, "device_not_found", "device not found")
}

func (s *FileStore) updateDeviceStatus(account Account, deviceID, status, relayURL string, now time.Time) (DeviceRecord, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, device := range s.state.Devices {
		if device.AccountIDHash != account.AccountIDHash || device.DeviceID != strings.TrimSpace(deviceID) {
			continue
		}
		if device.Status == DeviceStatusRevoked && status != DeviceStatusRevoked {
			return DeviceRecord{}, apiErr(403, "device_revoked", "device has been removed from mesh")
		}
		device.Status = status
		if status == DeviceStatusOnline {
			device.LastSeen = now.Format(time.RFC3339Nano)
			if relayURL != "" {
				device.RelayURL = relayURL
			}
		}
		device.UpdatedAt = now.Format(time.RFC3339Nano)
		if err := validateDeviceRecord(device); err != nil {
			return DeviceRecord{}, err
		}
		s.state.Devices[i] = device
		if err := s.writeLocked(); err != nil {
			return DeviceRecord{}, err
		}
		return device, nil
	}
	return DeviceRecord{}, apiErr(404, "device_not_found", "device not found")
}

func (s *FileStore) CreatePairingRequest(account Account, input PairingRequestInput, now time.Time) (PairingRequest, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hostID := strings.TrimSpace(input.HostDeviceID)
	controllerID := strings.TrimSpace(input.ControllerDeviceID)
	if hostID == "" || controllerID == "" {
		return PairingRequest{}, apiErr(400, "pairing_devices_required", "host_device_id and controller_device_id are required")
	}
	if hostID == controllerID {
		return PairingRequest{}, apiErr(400, "self_pairing_not_allowed", "host and controller must be different devices")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	host, hostOK := s.deviceLocked(account.AccountIDHash, hostID)
	controller, controllerOK := s.deviceLocked(account.AccountIDHash, controllerID)
	if !hostOK || !controllerOK {
		return PairingRequest{}, apiErr(404, "pairing_device_not_found", "host or controller device not found")
	}
	if host.Status == DeviceStatusRevoked || controller.Status == DeviceStatusRevoked {
		return PairingRequest{}, apiErr(403, "pairing_device_revoked", "host or controller device has been removed from mesh")
	}
	if !host.CanHost {
		return PairingRequest{}, apiErr(400, "host_role_required", "host device cannot accept remote control")
	}
	if !controller.CanControl {
		return PairingRequest{}, apiErr(400, "controller_role_required", "controller device cannot control hosts")
	}
	nowText := now.Format(time.RFC3339Nano)
	for i, existing := range s.state.PairingRequests {
		if existing.AccountIDHash == account.AccountIDHash && existing.HostDeviceID == hostID && existing.ControllerDeviceID == controllerID && existing.Status == PairingStatusPending {
			existing.Scope = strings.TrimSpace(input.Scope)
			if existing.Scope == "" {
				existing.Scope = "full"
			}
			existing.Capabilities = normalizeStringList(input.Capabilities)
			existing.WorkspaceExecPolicy = strings.TrimSpace(input.WorkspaceExecPolicy)
			existing.UpdatedAt = nowText
			s.state.PairingRequests[i] = existing
			if err := s.writeLocked(); err != nil {
				return PairingRequest{}, err
			}
			return existing, nil
		}
	}
	request := PairingRequest{
		RequestID:                      "pair_" + randomHex(16),
		AccountIDHash:                  account.AccountIDHash,
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
		Capabilities:                   normalizeStringList(input.Capabilities),
		WorkspaceExecPolicy:            strings.TrimSpace(input.WorkspaceExecPolicy),
		CreatedAt:                      nowText,
		UpdatedAt:                      nowText,
	}
	if request.Scope == "" {
		request.Scope = "full"
	}
	s.state.PairingRequests = append(s.state.PairingRequests, request)
	s.normalizeLocked()
	if err := s.writeLocked(); err != nil {
		return PairingRequest{}, err
	}
	return request, nil
}

func (s *FileStore) ListPairingRequests(account Account, deviceID string) []PairingRequest {
	deviceID = strings.TrimSpace(deviceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PairingRequest, 0)
	for _, request := range s.state.PairingRequests {
		if request.AccountIDHash != account.AccountIDHash {
			continue
		}
		if deviceID != "" && request.HostDeviceID != deviceID && request.ControllerDeviceID != deviceID {
			continue
		}
		out = append(out, request)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == PairingStatusPending && out[j].Status != PairingStatusPending {
			return true
		}
		if out[i].Status != PairingStatusPending && out[j].Status == PairingStatusPending {
			return false
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func (s *FileStore) GetPairingRequest(account Account, requestID string) (PairingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, request := range s.state.PairingRequests {
		if request.AccountIDHash == account.AccountIDHash && request.RequestID == strings.TrimSpace(requestID) {
			return request, nil
		}
	}
	return PairingRequest{}, apiErr(404, "pairing_request_not_found", "pairing request not found")
}

func (s *FileStore) ResolvePairingRequest(account Account, requestID string, input PairingResolveInput, now time.Time) (PairingRequest, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	status := strings.TrimSpace(input.Status)
	if status != PairingStatusApproved && status != PairingStatusDenied {
		return PairingRequest{}, apiErr(400, "pairing_status_invalid", "status must be approved or denied")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, request := range s.state.PairingRequests {
		if request.AccountIDHash != account.AccountIDHash || request.RequestID != strings.TrimSpace(requestID) {
			continue
		}
		if request.Status != PairingStatusPending {
			return PairingRequest{}, apiErr(409, "pairing_request_resolved", "pairing request is already resolved")
		}
		nowText := now.Format(time.RFC3339Nano)
		request.Status = status
		request.ResolverDeviceID = strings.TrimSpace(input.ResolverDeviceID)
		request.ResolvedAt = nowText
		request.UpdatedAt = nowText
		s.state.PairingRequests[i] = request
		if err := s.writeLocked(); err != nil {
			return PairingRequest{}, err
		}
		return request, nil
	}
	return PairingRequest{}, apiErr(404, "pairing_request_not_found", "pairing request not found")
}

func (s *FileStore) EnqueueRelayEnvelope(account Account, envelope RelayEnvelope, now time.Time) (RelayEnvelope, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	envelope.AccountIDHash = account.AccountIDHash
	envelope.EnvelopeID = strings.TrimSpace(envelope.EnvelopeID)
	if envelope.EnvelopeID == "" {
		envelope.EnvelopeID = "env_" + randomHex(16)
	}
	envelope.CreatedAt = now.Format(time.RFC3339Nano)
	if err := validateRelayEnvelope(envelope); err != nil {
		return RelayEnvelope{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deviceLocked(account.AccountIDHash, envelope.FromDeviceID); !ok {
		return RelayEnvelope{}, apiErr(404, "from_device_not_found", "from device not found")
	}
	fromDevice, _ := s.deviceLocked(account.AccountIDHash, envelope.FromDeviceID)
	if fromDevice.Status == DeviceStatusRevoked {
		return RelayEnvelope{}, apiErr(403, "from_device_revoked", "from device has been removed from mesh")
	}
	toDevice, ok := s.deviceLocked(account.AccountIDHash, envelope.ToDeviceID)
	if !ok {
		return RelayEnvelope{}, apiErr(404, "to_device_not_found", "to device not found")
	}
	if toDevice.Status == DeviceStatusRevoked {
		return RelayEnvelope{}, apiErr(403, "to_device_revoked", "to device has been removed from mesh")
	}
	s.state.RelayEnvelopes = append(s.state.RelayEnvelopes, envelope)
	if err := s.writeLocked(); err != nil {
		return RelayEnvelope{}, err
	}
	return envelope, nil
}

func (s *FileStore) ListRelayEnvelopes(account Account, deviceID string, limit int) []RelayEnvelope {
	deviceID = strings.TrimSpace(deviceID)
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RelayEnvelope, 0)
	for _, envelope := range s.state.RelayEnvelopes {
		if envelope.AccountIDHash != account.AccountIDHash || envelope.ToDeviceID != deviceID {
			continue
		}
		out = append(out, envelope)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *FileStore) AckRelayEnvelope(account Account, envelopeID, deviceID string) error {
	envelopeID = strings.TrimSpace(envelopeID)
	deviceID = strings.TrimSpace(deviceID)
	if envelopeID == "" {
		return apiErr(400, "relay_envelope_id_required", "relay envelope id required")
	}
	if deviceID == "" {
		return apiErr(400, "device_id_required", "device_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, envelope := range s.state.RelayEnvelopes {
		if envelope.AccountIDHash == account.AccountIDHash && envelope.EnvelopeID == envelopeID {
			if envelope.ToDeviceID != deviceID {
				return apiErr(403, "relay_envelope_receiver_mismatch", "relay envelope receiver mismatch")
			}
			s.state.RelayEnvelopes = append(s.state.RelayEnvelopes[:i], s.state.RelayEnvelopes[i+1:]...)
			if err := s.writeLocked(); err != nil {
				return err
			}
			return nil
		}
	}
	return apiErr(404, "relay_envelope_not_found", "relay envelope not found")
}

func (s *FileStore) deviceLocked(accountIDHash, deviceID string) (DeviceRecord, bool) {
	for _, device := range s.state.Devices {
		if device.AccountIDHash == accountIDHash && device.DeviceID == deviceID {
			return device, true
		}
	}
	return DeviceRecord{}, false
}

func filterRelayEnvelopes(envelopes []RelayEnvelope, keep func(RelayEnvelope) bool) []RelayEnvelope {
	out := envelopes[:0]
	for _, envelope := range envelopes {
		if keep(envelope) {
			out = append(out, envelope)
		}
	}
	return out
}

func (s *FileStore) normalizeLocked() {
	sort.Slice(s.state.Devices, func(i, j int) bool {
		if s.state.Devices[i].AccountIDHash != s.state.Devices[j].AccountIDHash {
			return s.state.Devices[i].AccountIDHash < s.state.Devices[j].AccountIDHash
		}
		return s.state.Devices[i].DeviceID < s.state.Devices[j].DeviceID
	})
	sort.Slice(s.state.PairingRequests, func(i, j int) bool {
		if s.state.PairingRequests[i].AccountIDHash != s.state.PairingRequests[j].AccountIDHash {
			return s.state.PairingRequests[i].AccountIDHash < s.state.PairingRequests[j].AccountIDHash
		}
		return s.state.PairingRequests[i].CreatedAt < s.state.PairingRequests[j].CreatedAt
	})
}

func (s *FileStore) writeLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)[:n]
}
