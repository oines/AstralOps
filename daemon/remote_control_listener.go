package main

import (
	"log"
	"net"
	"net/http"
	"os"
)

const (
	envRemoteControlListen     = "ASTRALOPS_CONTROL_LISTEN"
	envRemoteControlDevPairing = "ASTRALOPS_DEV_REMOTE_PAIRING"
)

func startRemoteControlListener(a *app) (string, error) {
	addr := os.Getenv(envRemoteControlListen)
	if addr == "" {
		return "", nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	handler := remoteControlHandler(a, os.Getenv(envRemoteControlDevPairing) == "1")
	if os.Getenv(envRemoteControlDiscovery) != "0" {
		if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
			discoveryConn, err := startRemoteControlDiscovery(a.store.hostInfo().Identity, tcpAddr.Port)
			if err != nil {
				log.Printf("remote control discovery disabled: %v", err)
			} else {
				_ = discoveryConn
				log.Printf("astralops remote control UDP discovery listening on port %d", tcpAddr.Port)
			}
		}
	}
	go func() {
		log.Printf("astralops remote control listening on %s", ln.Addr().String())
		if err := http.Serve(ln, withCORS(handler)); err != nil && err != http.ErrServerClosed {
			log.Printf("remote control listener: %v", err)
		}
	}()
	return ln.Addr().String(), nil
}

func remoteControlHandler(a *app, devPairing bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/host", a.handleHost)
	mux.HandleFunc("/v1/control/ws", a.handleControlWS)
	if devPairing {
		mux.HandleFunc("/v1/trust/devices", a.handleTrustDevices)
		mux.HandleFunc("/v1/trust/devices/", a.handleTrustDeviceAction)
	}
	return mux
}
