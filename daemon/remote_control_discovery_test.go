package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestRemoteControlDiscoveryRoundTrip(t *testing.T) {
	identity := DeviceIdentity{
		DeviceID:             "dev_host",
		DeviceName:           "Host",
		PublicKeyFingerprint: "sha256:HOST",
	}
	conn, err := startRemoteControlDiscovery(identity, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	candidates, err := discoverRemoteControlHostsAt(ctx, []net.UDPAddr{{IP: net.ParseIP("127.0.0.1"), Port: port}})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one candidate", candidates)
	}
	candidate := candidates[0]
	if candidate.DeviceID != "dev_host" || candidate.DeviceName != "Host" || candidate.PublicKeyFingerprint != "sha256:HOST" {
		t.Fatalf("candidate = %#v, want host identity", candidate)
	}
	if candidate.Port != port || candidate.Host == "" || candidate.BaseURL == "" || len(candidate.Addresses) == 0 {
		t.Fatalf("candidate address = %#v, want address fields", candidate)
	}
}

func TestRemoteControlCandidateFromDiscoveryPacket(t *testing.T) {
	body, err := json.Marshal(remoteControlDiscoveryPacket{
		Type:    remoteControlDiscoveryResponseType,
		Version: controlProtocolVersion,
		Candidate: &LanHostCandidate{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: "sha256:HOST",
			Host:                 "10.0.0.10",
			Port:                 43900,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	candidate, ok := remoteControlCandidateFromDiscoveryPacket(body)
	if !ok {
		t.Fatal("candidate was not parsed")
	}
	if candidate.BaseURL != "http://10.0.0.10:43900" || len(candidate.Addresses) != 1 || candidate.Addresses[0] != "10.0.0.10" {
		t.Fatalf("candidate = %#v, want derived address fields", candidate)
	}
}

func TestRemoteControlCandidateIgnoresDiscoveryBaseURL(t *testing.T) {
	body, err := json.Marshal(remoteControlDiscoveryPacket{
		Type:    remoteControlDiscoveryResponseType,
		Version: controlProtocolVersion,
		Candidate: &LanHostCandidate{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: "sha256:HOST",
			Host:                 "10.0.0.10",
			Port:                 43900,
			BaseURL:              "http://203.0.113.10:9999",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	candidate, ok := remoteControlCandidateFromDiscoveryPacket(body)
	if !ok {
		t.Fatal("candidate was not parsed")
	}
	if candidate.BaseURL != "http://10.0.0.10:43900" {
		t.Fatalf("candidate BaseURL = %q, want derived LAN address", candidate.BaseURL)
	}
}

func TestRemoteControlCandidateRejectsIncompleteDiscoveryPacket(t *testing.T) {
	body, err := json.Marshal(remoteControlDiscoveryPacket{
		Type:    remoteControlDiscoveryResponseType,
		Version: controlProtocolVersion,
		Candidate: &LanHostCandidate{
			DeviceID: "dev_host",
			Host:     "10.0.0.10",
			Port:     43900,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := remoteControlCandidateFromDiscoveryPacket(body); ok {
		t.Fatal("candidate parsed without public_key_fingerprint")
	}
}

func TestRemoteControlCandidateRejectsNonIPv4Host(t *testing.T) {
	body, err := json.Marshal(remoteControlDiscoveryPacket{
		Type:    remoteControlDiscoveryResponseType,
		Version: controlProtocolVersion,
		Candidate: &LanHostCandidate{
			DeviceID:             "dev_host",
			PublicKeyFingerprint: "sha256:HOST",
			Host:                 "example.test",
			Port:                 43900,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := remoteControlCandidateFromDiscoveryPacket(body); ok {
		t.Fatal("candidate parsed with non-IPv4 host")
	}
}

func TestRemoteControlIPv4Broadcast(t *testing.T) {
	broadcast := remoteControlIPv4Broadcast(net.IPv4(10, 0, 0, 92), net.IPv4Mask(255, 255, 255, 0))
	if broadcast.String() != "10.0.0.255" {
		t.Fatalf("broadcast = %s, want 10.0.0.255", broadcast)
	}
}
