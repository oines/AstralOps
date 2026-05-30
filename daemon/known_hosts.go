package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const knownHostsFileName = "known_hosts.json"

type KnownHost struct {
	DeviceID             string `json:"device_id"`
	DeviceName           string `json:"device_name,omitempty"`
	PublicKey            string `json:"public_key"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	Status               string `json:"status,omitempty"`
	LastBaseURL          string `json:"last_base_url,omitempty"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
	RevokedAt            string `json:"revoked_at,omitempty"`
}

func knownHostsPath(dataDir string) string {
	return filepath.Join(dataDir, deviceIdentityDir, knownHostsFileName)
}

func loadKnownHosts(dataDir string) (map[string]KnownHost, error) {
	path := knownHostsPath(dataDir)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]KnownHost{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []KnownHost
	if err := json.Unmarshal(body, &hosts); err != nil {
		return nil, err
	}
	out := map[string]KnownHost{}
	for _, host := range hosts {
		host = normalizeKnownHost(host)
		if host.DeviceID == "" || host.PublicKey == "" || host.PublicKeyFingerprint == "" {
			continue
		}
		out[host.DeviceID] = host
	}
	return out, nil
}

func (s *store) rememberKnownHost(info HostInfo, baseURL string) (KnownHost, error) {
	identity := info.Identity
	if strings.TrimSpace(identity.DeviceID) == "" || strings.TrimSpace(identity.PublicKey) == "" {
		return KnownHost{}, newActionError(400, "host_identity_invalid", "host identity is missing device_id or public_key")
	}
	publicKey, err := decodeDevicePublicKey(identity.PublicKey)
	if err != nil {
		return KnownHost{}, err
	}
	fingerprint := devicePublicKeyFingerprint(publicKey)
	if identity.PublicKeyFingerprint != "" && identity.PublicKeyFingerprint != fingerprint {
		return KnownHost{}, newActionError(400, "fingerprint_mismatch", "host public key fingerprint mismatch")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := normalizeKnownHost(s.knownHosts[identity.DeviceID]); knownHostRevoked(existing) {
		return KnownHost{}, newActionError(http.StatusForbidden, "known_host_revoked", "known Host has been removed from mesh")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	host := s.knownHosts[identity.DeviceID]
	if host.CreatedAt == "" {
		host.CreatedAt = now
	}
	host.DeviceID = strings.TrimSpace(identity.DeviceID)
	host.DeviceName = strings.TrimSpace(identity.DeviceName)
	host.PublicKey = strings.TrimSpace(identity.PublicKey)
	host.PublicKeyFingerprint = fingerprint
	host.LastBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	host.Status = ""
	host.RevokedAt = ""
	host.UpdatedAt = now
	if s.knownHosts == nil {
		s.knownHosts = map[string]KnownHost{}
	}
	s.knownHosts[host.DeviceID] = host
	if err := s.writeKnownHostsLocked(); err != nil {
		return KnownHost{}, err
	}
	return host, nil
}

func (s *store) knownHost(deviceID string) (KnownHost, bool) {
	deviceID = strings.TrimSpace(deviceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	host, ok := s.knownHosts[deviceID]
	return host, ok
}

func (s *store) markKnownHostRevoked(deviceID string) (KnownHost, bool, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return KnownHost{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	host, ok := s.knownHosts[deviceID]
	if !ok {
		return KnownHost{}, false, nil
	}
	host = normalizeKnownHost(host)
	if knownHostRevoked(host) {
		return host, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	host.Status = TrustStatusRevoked
	host.UpdatedAt = now
	host.RevokedAt = now
	s.knownHosts[deviceID] = host
	if err := s.writeKnownHostsLocked(); err != nil {
		return KnownHost{}, false, err
	}
	return host, true, nil
}

func (s *store) listKnownHosts() []KnownHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KnownHost, 0, len(s.knownHosts))
	for _, host := range s.knownHosts {
		out = append(out, host)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func (s *store) writeKnownHostsLocked() error {
	return writeKnownHostsFile(s.dataDir, s.knownHosts)
}

func writeKnownHostsFile(dataDir string, hostsByDevice map[string]KnownHost) error {
	hosts := make([]KnownHost, 0, len(hostsByDevice))
	for _, host := range hostsByDevice {
		hosts = append(hosts, normalizeKnownHost(host))
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].DeviceID < hosts[j].DeviceID })
	return writeJSONFile(knownHostsPath(dataDir), hosts, defaultHostFileMode)
}

func normalizeKnownHost(host KnownHost) KnownHost {
	host.DeviceID = strings.TrimSpace(host.DeviceID)
	host.DeviceName = strings.TrimSpace(host.DeviceName)
	host.PublicKey = strings.TrimSpace(host.PublicKey)
	host.PublicKeyFingerprint = strings.TrimSpace(host.PublicKeyFingerprint)
	host.Status = strings.TrimSpace(host.Status)
	host.LastBaseURL = strings.TrimRight(strings.TrimSpace(host.LastBaseURL), "/")
	host.RevokedAt = strings.TrimSpace(host.RevokedAt)
	return host
}

func knownHostRevoked(host KnownHost) bool {
	return strings.TrimSpace(host.Status) == TrustStatusRevoked || strings.TrimSpace(host.RevokedAt) != ""
}
