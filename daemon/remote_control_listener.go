package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	envRemoteControlDevPairing     = "ASTRALOPS_DEV_REMOTE_PAIRING"
	defaultRemoteControlListenAddr = "0.0.0.0:43900"
)

type remoteControlRuntime struct {
	server        *http.Server
	listener      net.Listener
	discoveryConn *net.UDPConn
	settings      RemoteControlSettings
	listenAddr    string
}

func (a *app) applyRemoteControlSettings(settings RemoteControlSettings) error {
	settings = normalizedRemoteControlSettings(settings)
	a.remoteControlMu.Lock()
	defer a.remoteControlMu.Unlock()

	current := a.remoteControl
	if !settings.Enabled {
		if current != nil {
			current.close()
			a.remoteControl = nil
		}
		return nil
	}
	if current != nil && current.settings.ListenAddr == settings.ListenAddr {
		if current.settings.LANDiscovery != settings.LANDiscovery {
			if err := current.applyDiscovery(a, settings.LANDiscovery); err != nil {
				return err
			}
			current.settings = settings
		}
		return nil
	}

	next, err := startRemoteControlRuntime(a, settings)
	if err != nil {
		return err
	}
	if current != nil {
		current.close()
	}
	a.remoteControl = next
	return nil
}

func (a *app) remoteControlListenAddr() string {
	a.remoteControlMu.Lock()
	defer a.remoteControlMu.Unlock()
	if a.remoteControl == nil {
		return ""
	}
	return a.remoteControl.listenAddr
}

func startRemoteControlRuntime(a *app, settings RemoteControlSettings) (*remoteControlRuntime, error) {
	settings = normalizedRemoteControlSettings(settings)
	if !settings.Enabled {
		return nil, nil
	}
	ln, err := net.Listen("tcp", settings.ListenAddr)
	if err != nil {
		return nil, err
	}
	runtime := &remoteControlRuntime{
		listener:   ln,
		settings:   settings,
		listenAddr: ln.Addr().String(),
	}
	if settings.LANDiscovery {
		if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
			discoveryConn, err := startRemoteControlDiscoveryForApp(a, tcpAddr.Port)
			if err != nil {
				_ = ln.Close()
				return nil, fmt.Errorf("remote control discovery: %w", err)
			} else {
				runtime.discoveryConn = discoveryConn
				log.Printf("astralops remote control UDP discovery listening on port %d", tcpAddr.Port)
			}
		}
	}
	handler := remoteControlHandler(a, os.Getenv(envRemoteControlDevPairing) == "1")
	runtime.server = &http.Server{Handler: withCORS(handler)}
	go func() {
		log.Printf("astralops remote control listening on %s", ln.Addr().String())
		if err := runtime.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Printf("remote control listener: %v", err)
		}
	}()
	return runtime, nil
}

func (r *remoteControlRuntime) applyDiscovery(a *app, enabled bool) error {
	if r == nil {
		return nil
	}
	if enabled && r.discoveryConn == nil {
		tcpAddr, ok := r.listener.Addr().(*net.TCPAddr)
		if !ok {
			return fmt.Errorf("remote control listener address is not TCP")
		}
		conn, err := startRemoteControlDiscoveryForApp(a, tcpAddr.Port)
		if err != nil {
			return fmt.Errorf("remote control discovery: %w", err)
		}
		r.discoveryConn = conn
		log.Printf("astralops remote control UDP discovery listening on port %d", tcpAddr.Port)
	}
	if !enabled && r.discoveryConn != nil {
		_ = r.discoveryConn.Close()
		r.discoveryConn = nil
	}
	return nil
}

func (r *remoteControlRuntime) close() {
	if r == nil {
		return
	}
	if r.discoveryConn != nil {
		_ = r.discoveryConn.Close()
		r.discoveryConn = nil
	}
	if r.server != nil {
		_ = r.server.Close()
		return
	}
	if r.listener != nil {
		_ = r.listener.Close()
	}
}

func normalizedRemoteControlSettings(settings RemoteControlSettings) RemoteControlSettings {
	settings.ListenAddr = strings.TrimSpace(settings.ListenAddr)
	if settings.ListenAddr == "" {
		settings.ListenAddr = defaultRemoteControlListenAddr
	}
	return settings
}

func remoteControlSettingsChanged(left, right RemoteControlSettings) bool {
	left = normalizedRemoteControlSettings(left)
	right = normalizedRemoteControlSettings(right)
	return left.Enabled != right.Enabled || left.ListenAddr != right.ListenAddr || left.LANDiscovery != right.LANDiscovery
}

func validateRemoteControlListenAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid remote_control.listen_addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid remote_control.listen_addr %q: invalid port", addr)
	}
	if strings.TrimSpace(host) == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	if strings.ContainsAny(host, " \t\r\n/") {
		return fmt.Errorf("invalid remote_control.listen_addr %q: invalid host", addr)
	}
	return nil
}

func remoteControlHandler(a *app, devPairing bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/host", a.handleRemoteControlHost)
	mux.HandleFunc("/v1/pairing/requests", a.handleRemoteControlPairingRequestSubmit)
	mux.HandleFunc("/v1/pairing/requests/", a.handleRemoteControlPairingRequestStatus)
	mux.HandleFunc("/v1/control/ws", a.handleRemoteControlWS)
	if devPairing {
		mux.HandleFunc("/v1/trust/devices", a.handleTrustDevices)
		mux.HandleFunc("/v1/trust/devices/", a.handleTrustDeviceAction)
	}
	return mux
}

func (a *app) handleRemoteControlHost(w http.ResponseWriter, r *http.Request) {
	if !a.requireCloudMeshRemoteControl(w) {
		return
	}
	a.handleHost(w, r)
}

func (a *app) handleRemoteControlPairingRequestSubmit(w http.ResponseWriter, r *http.Request) {
	if !a.requireCloudMeshRemoteControl(w) {
		return
	}
	a.handlePairingRequestSubmit(w, r)
}

func (a *app) handleRemoteControlPairingRequestStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireCloudMeshRemoteControl(w) {
		return
	}
	a.handlePairingRequestStatus(w, r)
}

func (a *app) handleRemoteControlWS(w http.ResponseWriter, r *http.Request) {
	if !a.requireCloudMeshRemoteControl(w) {
		return
	}
	a.handleControlWS(w, r)
}
