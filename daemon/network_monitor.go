package main

import (
	"context"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	networkMonitorInterval = 2 * time.Second
	networkMonitorDebounce = 800 * time.Millisecond
)

type networkMonitor struct {
	app *app

	mu          sync.Mutex
	fingerprint string
	generation  int64
}

func newNetworkMonitor(a *app) *networkMonitor {
	return &networkMonitor{app: a, fingerprint: networkFingerprint()}
}

func (m *networkMonitor) start(ctx context.Context) {
	if m == nil {
		return
	}
	go m.loop(ctx)
}

func (m *networkMonitor) loop(ctx context.Context) {
	ticker := time.NewTicker(networkMonitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := networkFingerprint()
			m.mu.Lock()
			changed := next != "" && next != m.fingerprint
			m.mu.Unlock()
			if !changed {
				continue
			}
			timer := time.NewTimer(networkMonitorDebounce)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			m.handleChange(networkFingerprint())
		}
	}
}

func (m *networkMonitor) handleChange(next string) {
	if m == nil || next == "" {
		return
	}
	m.mu.Lock()
	if next == m.fingerprint {
		m.mu.Unlock()
		return
	}
	m.fingerprint = next
	m.generation++
	generation := m.generation
	m.mu.Unlock()

	if m.app == nil {
		return
	}
	// Network changes are only hints. End-to-end HostRemoteSession health owns
	// connection invalidation, otherwise transient interface/IP churn can kill a
	// freshly recovered remote session a few seconds after reconnecting.
	log.Printf("astralops network changed generation=%d hint=true", generation)
	m.app.refreshMeshStateAsync(true)
	m.app.syncCloudRegistrationSoon(m.app.currentSettings())
}

func (m *networkMonitor) Generation() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generation
}

func networkFingerprint() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	parts := make([]string, 0)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := networkAddrIP(addr)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			parts = append(parts, iface.Name+"="+ip.String())
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func networkAddrIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		host := strings.TrimSpace(addr.String())
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		return net.ParseIP(host)
	}
}
