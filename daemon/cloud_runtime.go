package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	cloudSyncInterval = 60 * time.Second
	cloudSyncTimeout  = 15 * time.Second
)

func (a *app) applyCloudSettings(settings CloudSettings) error {
	settings = normalizedAppSettings(AppSettings{Cloud: settings}).Cloud
	if settings.Enabled {
		if err := validateCloudSettings(settings); err != nil {
			return err
		}
	}

	a.cloudMu.Lock()
	defer a.cloudMu.Unlock()
	if a.cloudCancel != nil {
		a.cloudCancel()
		a.cloudCancel = nil
	}
	a.cloudSettings = settings
	if !settings.Enabled {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cloudCancel = cancel
	go a.cloudSyncLoop(ctx, settings)
	return nil
}

func (a *app) cloudSyncLoop(ctx context.Context, settings CloudSettings) {
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
		log.Printf("astralops cloud sync: %v", err)
	}

	ticker := time.NewTicker(cloudSyncInterval)
	defer ticker.Stop()
	relayTicker := time.NewTicker(controlRelayPollInterval)
	defer relayTicker.Stop()
	defer a.cloudMarkOffline(settings)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
				log.Printf("astralops cloud sync: %v", err)
			}
		case <-relayTicker.C:
			relayCtx, relayCancel := context.WithTimeout(ctx, cloudSyncTimeout)
			if err := a.cloudPollRelayEnvelopes(relayCtx, client); err != nil {
				log.Printf("astralops cloud relay: %v", err)
			}
			relayCancel()
		}
	}
}

func (a *app) cloudRegisterAndHeartbeat(ctx context.Context, client CloudClient) error {
	if a == nil || a.store == nil {
		return nil
	}
	registerCtx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer cancel()
	settings := a.currentSettings()
	if _, err := client.RegisterDevice(registerCtx, a.store.hostInfo().Identity, settings.RemoteControl.Enabled, true, ""); err != nil {
		return err
	}

	heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer heartbeatCancel()
	if _, err := client.HeartbeatDevice(heartbeatCtx, a.store.hostInfo().Identity.DeviceID, ""); err != nil {
		return err
	}
	approvedCtx, approvedCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer approvedCancel()
	if err := a.cloudSyncApprovedPairingKnownHosts(approvedCtx, client, nil); err != nil {
		return err
	}
	if settings.RemoteControl.Enabled {
		pairingCtx, pairingCancel := context.WithTimeout(ctx, cloudSyncTimeout)
		defer pairingCancel()
		if err := a.cloudSyncPendingPairingRequests(pairingCtx, client); err != nil {
			return err
		}
		relayCtx, relayCancel := context.WithTimeout(ctx, cloudSyncTimeout)
		defer relayCancel()
		if err := a.cloudPollRelayEnvelopes(relayCtx, client); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) cloudSyncApprovedPairingKnownHosts(ctx context.Context, client CloudClient, devices []CloudDeviceRecord) error {
	if a == nil || a.store == nil {
		return nil
	}
	self := a.store.hostInfo().Identity.DeviceID
	if strings.TrimSpace(self) == "" {
		return nil
	}
	signals, err := client.ListPairingSignals(ctx, self)
	if err != nil {
		return err
	}
	needsDevices := false
	for _, signal := range signals {
		if strings.TrimSpace(signal.ControllerDeviceID) == self && strings.TrimSpace(signal.Status) == PairingStatusApproved {
			needsDevices = true
			break
		}
	}
	if !needsDevices {
		return nil
	}
	if devices == nil {
		devices, err = client.ListDevices(ctx)
		if err != nil {
			return err
		}
	}
	devicesByID := map[string]CloudDeviceRecord{}
	for _, device := range devices {
		devicesByID[strings.TrimSpace(device.DeviceID)] = device
	}
	for _, signal := range signals {
		if strings.TrimSpace(signal.ControllerDeviceID) != self || strings.TrimSpace(signal.Status) != PairingStatusApproved {
			continue
		}
		hostID := strings.TrimSpace(signal.HostDeviceID)
		if hostID == "" || hostID == self {
			continue
		}
		host, ok := devicesByID[hostID]
		if !ok {
			return fmt.Errorf("approved cloud pairing Host %s not found", hostID)
		}
		if !host.CanHost || normalizeCloudDeviceStatus(host.Status) == cloudDeviceStatusRevoked {
			continue
		}
		if signal.HostPublicKeyFingerprint != "" && host.PublicKeyFingerprint != "" && signal.HostPublicKeyFingerprint != host.PublicKeyFingerprint {
			return fmt.Errorf("approved cloud pairing Host public key fingerprint mismatch for %s", hostID)
		}
		if known, ok := a.store.knownHost(hostID); ok {
			if normalizeKnownHost(known).PublicKeyFingerprint != strings.TrimSpace(host.PublicKeyFingerprint) {
				return fmt.Errorf("known Host public key fingerprint mismatch for approved cloud pairing %s", hostID)
			}
			continue
		}
		if _, err := a.store.rememberKnownHost(hostInfoFromCloudDevice(host), ""); err != nil {
			return err
		}
	}
	return nil
}

func hostInfoFromCloudDevice(device CloudDeviceRecord) HostInfo {
	return HostInfo{
		Identity: DeviceIdentity{
			DeviceID:             strings.TrimSpace(device.DeviceID),
			DeviceName:           strings.TrimSpace(device.DeviceName),
			DeviceKind:           strings.TrimSpace(device.DeviceKind),
			PublicKey:            strings.TrimSpace(device.PublicKey),
			PublicKeyFingerprint: strings.TrimSpace(device.PublicKeyFingerprint),
			Capabilities:         normalizeCapabilities(device.Capabilities),
			CreatedAt:            strings.TrimSpace(device.UpdatedAt),
			UpdatedAt:            strings.TrimSpace(device.UpdatedAt),
		},
		Capabilities: normalizeCapabilities(device.Capabilities),
	}
}

func (a *app) cloudSyncPendingPairingRequests(ctx context.Context, client CloudClient) error {
	if a == nil || a.store == nil {
		return nil
	}
	host := a.store.hostInfo().Identity
	signals, err := client.ListPairingSignals(ctx, host.DeviceID)
	if err != nil {
		return err
	}
	hasPendingForHost := false
	for _, signal := range signals {
		if strings.TrimSpace(signal.HostDeviceID) == host.DeviceID && strings.TrimSpace(signal.Status) == PairingStatusPending {
			hasPendingForHost = true
			break
		}
	}
	if !hasPendingForHost {
		return nil
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return err
	}
	devicesByID := map[string]CloudDeviceRecord{}
	for _, device := range devices {
		devicesByID[strings.TrimSpace(device.DeviceID)] = device
	}
	for _, signal := range signals {
		if strings.TrimSpace(signal.HostDeviceID) != host.DeviceID || strings.TrimSpace(signal.Status) != PairingStatusPending {
			continue
		}
		if request, ok := a.store.pairingRequestByCloudRequestID(signal.RequestID); ok && request.Status != PairingStatusPending {
			a.cloudResolvePairingSignal(ctx, client, request)
			continue
		}
		if _, ok := a.store.trustedControlGrant(strings.TrimSpace(signal.ControllerDeviceID)); ok {
			a.cloudResolvePairingSignal(ctx, client, PairingRequest{
				CloudRequestID:     signal.RequestID,
				ControllerDeviceID: signal.ControllerDeviceID,
				Status:             PairingStatusApproved,
			})
			continue
		}
		controller, ok := devicesByID[strings.TrimSpace(signal.ControllerDeviceID)]
		if !ok {
			return fmt.Errorf("cloud pairing %s controller device %s not found", signal.RequestID, signal.ControllerDeviceID)
		}
		request, created, err := a.store.upsertCloudPairingRequest(signal, controller)
		if err != nil {
			var actionErr *actionError
			if errors.As(err, &actionErr) && actionErr.Status == http.StatusConflict && actionErr.Code == "controller_already_trusted" {
				a.cloudResolvePairingSignal(ctx, client, PairingRequest{
					CloudRequestID:     signal.RequestID,
					ControllerDeviceID: signal.ControllerDeviceID,
					Status:             PairingStatusApproved,
				})
				continue
			}
			return err
		}
		if created {
			a.emitPairingRequested(request)
		}
	}
	return nil
}

func (a *app) cloudResolvePairingSignal(ctx context.Context, client CloudClient, request PairingRequest) {
	cloudRequestID := strings.TrimSpace(request.CloudRequestID)
	if cloudRequestID == "" || (request.Status != PairingStatusApproved && request.Status != PairingStatusDenied) {
		return
	}
	if _, err := client.ResolvePairingSignal(ctx, cloudRequestID, request.Status, a.store.hostInfo().Identity.DeviceID); err != nil {
		log.Printf("astralops cloud pairing resolve %s: %v", cloudRequestID, err)
	}
}

func (a *app) syncCloudPairingResolution(request PairingRequest) {
	if a == nil || a.store == nil || strings.TrimSpace(request.CloudRequestID) == "" {
		return
	}
	if request.Status != PairingStatusApproved && request.Status != PairingStatusDenied {
		return
	}
	settings := a.currentSettings().Cloud
	if !settings.Enabled {
		return
	}
	if err := validateCloudSettings(settings); err != nil {
		log.Printf("astralops cloud pairing settings: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloudSyncTimeout)
	defer cancel()
	a.cloudResolvePairingSignal(ctx, CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}, request)
}

func (a *app) cloudMarkOffline(settings CloudSettings) {
	if a == nil || a.store == nil || !settings.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	if _, err := client.MarkDeviceOffline(ctx, a.store.hostInfo().Identity.DeviceID); err != nil {
		log.Printf("astralops cloud offline: %v", err)
	}
}

func cloudSettingsChanged(left, right CloudSettings) bool {
	left = normalizedAppSettings(AppSettings{Cloud: left}).Cloud
	right = normalizedAppSettings(AppSettings{Cloud: right}).Cloud
	return left.Enabled != right.Enabled || left.BaseURL != right.BaseURL || left.AccountToken != right.AccountToken
}
