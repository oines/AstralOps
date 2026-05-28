package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"strconv"
	"time"
)

const (
	envRemoteControlDiscovery = "ASTRALOPS_CONTROL_DISCOVERY"

	remoteControlDiscoveryRequestType  = "astralops.discovery.request"
	remoteControlDiscoveryResponseType = "astralops.discovery.response"
	defaultRemoteControlDiscoveryPort  = 43900
)

type LanHostCandidate struct {
	DeviceID             string   `json:"device_id"`
	DeviceName           string   `json:"device_name,omitempty"`
	AccountIDHash        string   `json:"account_id_hash,omitempty"`
	PublicKeyFingerprint string   `json:"public_key_fingerprint"`
	Host                 string   `json:"host"`
	Port                 int      `json:"port"`
	BaseURL              string   `json:"base_url"`
	Addresses            []string `json:"addresses"`
}

type remoteControlDiscoveryPacket struct {
	Type      string            `json:"type"`
	Version   string            `json:"version"`
	Candidate *LanHostCandidate `json:"candidate,omitempty"`
}

func startRemoteControlDiscovery(identity DeviceIdentity, controlPort int) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: controlPort})
	if err != nil {
		return nil, err
	}
	actualPort := controlPort
	if actualPort == 0 {
		actualPort = conn.LocalAddr().(*net.UDPAddr).Port
	}
	go serveRemoteControlDiscovery(conn, identity, actualPort)
	return conn, nil
}

func serveRemoteControlDiscovery(conn *net.UDPConn, identity DeviceIdentity, controlPort int) {
	buf := make([]byte, 4096)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("remote control discovery read: %v", err)
			continue
		}
		var req remoteControlDiscoveryPacket
		if err := json.Unmarshal(buf[:n], &req); err != nil {
			continue
		}
		if req.Type != remoteControlDiscoveryRequestType || req.Version != controlProtocolVersion {
			continue
		}
		candidate := remoteControlDiscoveryCandidate(identity, controlPort, remoteAddr)
		if candidate.Host == "" {
			continue
		}
		body, err := json.Marshal(remoteControlDiscoveryPacket{
			Type:      remoteControlDiscoveryResponseType,
			Version:   controlProtocolVersion,
			Candidate: &candidate,
		})
		if err != nil {
			continue
		}
		if _, err := conn.WriteToUDP(body, remoteAddr); err != nil {
			log.Printf("remote control discovery write: %v", err)
		}
	}
}

func remoteControlDiscoveryCandidate(identity DeviceIdentity, controlPort int, remoteAddr *net.UDPAddr) LanHostCandidate {
	host := remoteControlLocalIPv4ForRemote(remoteAddr)
	if host == "" {
		host = remoteControlDefaultIPv4()
	}
	addresses := []string{}
	if host != "" {
		addresses = append(addresses, host)
	}
	return LanHostCandidate{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		AccountIDHash:        "local-dev",
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Host:                 host,
		Port:                 controlPort,
		BaseURL:              "http://" + net.JoinHostPort(host, strconv.Itoa(controlPort)),
		Addresses:            addresses,
	}
}

func discoverRemoteControlHostsWithTimeout(timeout time.Duration, port int) ([]LanHostCandidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return discoverRemoteControlHosts(ctx, port)
}

func discoverRemoteControlHosts(ctx context.Context, port int) ([]LanHostCandidate, error) {
	if port <= 0 {
		port = defaultRemoteControlDiscoveryPort
	}
	return discoverRemoteControlHostsAt(ctx, remoteControlBroadcastTargets(port))
}

func discoverRemoteControlHostsAt(ctx context.Context, targets []net.UDPAddr) ([]LanHostCandidate, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	request, err := json.Marshal(remoteControlDiscoveryPacket{
		Type:    remoteControlDiscoveryRequestType,
		Version: controlProtocolVersion,
	})
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		_, _ = conn.WriteToUDP(request, &target)
	}

	candidates := []LanHostCandidate{}
	seen := map[string]bool{}
	buf := make([]byte, 4096)
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if err := conn.SetReadDeadline(deadline); err != nil {
				return nil, err
			}
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return candidates, nil
			}
			select {
			case <-ctx.Done():
				return candidates, nil
			default:
				return nil, err
			}
		}
		candidate, ok := remoteControlCandidateFromDiscoveryPacket(buf[:n])
		if !ok {
			continue
		}
		key := candidate.DeviceID + "|" + candidate.Host + "|" + strconv.Itoa(candidate.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, candidate)
	}
}

func remoteControlCandidateFromDiscoveryPacket(body []byte) (LanHostCandidate, bool) {
	var packet remoteControlDiscoveryPacket
	if err := json.Unmarshal(body, &packet); err != nil {
		return LanHostCandidate{}, false
	}
	if packet.Type != remoteControlDiscoveryResponseType || packet.Version != controlProtocolVersion || packet.Candidate == nil {
		return LanHostCandidate{}, false
	}
	candidate := *packet.Candidate
	if candidate.DeviceID == "" || candidate.PublicKeyFingerprint == "" || candidate.Host == "" || candidate.Port <= 0 {
		return LanHostCandidate{}, false
	}
	if len(candidate.Addresses) == 0 {
		candidate.Addresses = []string{candidate.Host}
	}
	if candidate.BaseURL == "" {
		candidate.BaseURL = "http://" + net.JoinHostPort(candidate.Host, strconv.Itoa(candidate.Port))
	}
	return candidate, true
}

func remoteControlBroadcastTargets(port int) []net.UDPAddr {
	targets := []net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	seen := map[string]bool{net.IPv4bcast.String(): true}
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, mask := remoteControlIPv4AndMask(addr)
			if ip == nil || mask == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			broadcast := remoteControlIPv4Broadcast(ip, mask)
			if broadcast == nil || seen[broadcast.String()] {
				continue
			}
			seen[broadcast.String()] = true
			targets = append(targets, net.UDPAddr{IP: broadcast, Port: port})
		}
	}
	return targets
}

func remoteControlIPv4AndMask(addr net.Addr) (net.IP, net.IPMask) {
	ipNet, ok := addr.(*net.IPNet)
	if !ok {
		return nil, nil
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return nil, nil
	}
	return ip, ipNet.Mask
}

func remoteControlIPv4Broadcast(ip net.IP, mask net.IPMask) net.IP {
	ip = ip.To4()
	if ip == nil || len(mask) < net.IPv4len {
		return nil
	}
	if len(mask) != net.IPv4len {
		mask = mask[len(mask)-net.IPv4len:]
	}
	out := make(net.IP, net.IPv4len)
	for i := range out {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func remoteControlDefaultIPv4() string {
	conn, err := net.Dial("udp4", "198.51.100.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil || addr.IP.IsLoopback() || addr.IP.IsUnspecified() {
		return ""
	}
	ipv4 := addr.IP.To4()
	if ipv4 == nil {
		return ""
	}
	return ipv4.String()
}

func remoteControlLocalIPv4ForRemote(remoteAddr *net.UDPAddr) string {
	if remoteAddr == nil || remoteAddr.IP == nil {
		return ""
	}
	conn, err := net.DialUDP("udp4", nil, remoteAddr)
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil {
		return ""
	}
	return ip.String()
}
