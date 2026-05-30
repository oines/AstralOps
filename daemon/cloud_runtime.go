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
	selfDeviceID := a.store.hostInfo().Identity.DeviceID
	if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
		log.Printf("astralops cloud sync: %v", err)
	}
	relayClient, _, hasRelay, err := cloudRelayClientFromCloud(ctx, client)
	if err != nil {
		log.Printf("astralops cloud account relay: %v", err)
	}

	ticker := time.NewTicker(cloudSyncInterval)
	defer ticker.Stop()
	relayTicker := time.NewTicker(controlRelayPollInterval)
	defer relayTicker.Stop()
	defer a.cloudMarkOffline(settings, selfDeviceID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
				log.Printf("astralops cloud sync: %v", err)
			}
			nextRelayClient, _, nextHasRelay, err := cloudRelayClientFromCloud(ctx, client)
			if err != nil {
				log.Printf("astralops cloud account relay: %v", err)
			} else {
				relayClient = nextRelayClient
				hasRelay = nextHasRelay
			}
		case <-relayTicker.C:
			if !hasRelay {
				continue
			}
			relayCtx, relayCancel := context.WithTimeout(ctx, cloudSyncTimeout)
			if err := a.cloudPollRelayEnvelopes(relayCtx, relayClient); err != nil {
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
	accountCtx, accountCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	account, err := client.GetAccount(accountCtx)
	accountCancel()
	if err != nil {
		return err
	}
	relayClient, relay, hasRelay := relayClientFromCloudAccount(account, client.HTTPClient)
	relayURL := ""
	if hasRelay {
		relayURL = relay.RelayURL
	}
	devicesCtx, devicesCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	devices, err := client.ListDevices(devicesCtx)
	devicesCancel()
	if err != nil {
		return err
	}
	selfRevoked, err := a.cloudSyncRevokedDevices(devices)
	if err != nil {
		return err
	}
	a.setCloudSelfRevoked(selfRevoked)
	if selfRevoked {
		a.handleCloudSelfRevoked()
		return fmt.Errorf("current device has been removed from cloud mesh")
	}

	registerCtx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer cancel()
	settings := a.currentSettings()
	if _, err := client.RegisterDevice(registerCtx, a.store.hostInfo().Identity, settings.RemoteControl.Enabled, true, relayURL); err != nil {
		return err
	}

	heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer heartbeatCancel()
	heartbeat, err := client.HeartbeatDevice(heartbeatCtx, a.store.hostInfo().Identity.DeviceID, relayURL)
	if err != nil {
		return err
	}
	if err := a.store.updateCloudMembership(account, heartbeat); err != nil {
		return err
	}
	approvedCtx, approvedCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer approvedCancel()
	if err := a.cloudSyncApprovedPairingKnownHosts(approvedCtx, client, devices); err != nil {
		return err
	}
	if settings.RemoteControl.Enabled {
		pairingCtx, pairingCancel := context.WithTimeout(ctx, cloudSyncTimeout)
		defer pairingCancel()
		if err := a.cloudSyncPendingPairingRequests(pairingCtx, client); err != nil {
			return err
		}
		if hasRelay {
			relayCtx, relayCancel := context.WithTimeout(ctx, cloudSyncTimeout)
			defer relayCancel()
			if err := a.cloudPollRelayEnvelopes(relayCtx, relayClient); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloudRelayClientFromCloud(ctx context.Context, client CloudClient) (RelayClient, string, bool, error) {
	account, err := client.GetAccount(ctx)
	if err != nil {
		return RelayClient{}, "", false, err
	}
	relayClient, relay, ok := relayClientFromCloudAccount(account, client.HTTPClient)
	if !ok {
		return RelayClient{}, "", false, nil
	}
	return relayClient, relay.RelayURL, true, nil
}

func (a *app) cloudSyncRevokedDevices(devices []CloudDeviceRecord) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	self := strings.TrimSpace(a.store.hostInfo().Identity.DeviceID)
	selfRevoked := false
	for _, device := range devices {
		deviceID := strings.TrimSpace(device.DeviceID)
		if deviceID == "" || normalizeCloudDeviceStatus(device.Status) != cloudDeviceStatusRevoked {
			continue
		}
		if deviceID == self {
			selfRevoked = true
			continue
		}
		if _, ok := a.store.trustedControlGrant(deviceID); ok {
			if _, err := a.revokeTrustedControlDevice(deviceID, ""); err != nil {
				return selfRevoked, err
			}
		} else {
			a.closeControlSessionsForDevice(deviceID, "mesh_device_revoked")
			a.releaseTerminalWritersForDevice(deviceID)
		}
		if _, _, err := a.store.markKnownHostRevoked(deviceID); err != nil {
			return selfRevoked, err
		}
		if _, err := a.store.denyPendingPairingRequestsForDevice(deviceID); err != nil {
			return selfRevoked, err
		}
	}
	return selfRevoked, nil
}

func (a *app) setCloudSelfRevoked(revoked bool) {
	if a == nil {
		return
	}
	a.cloudMu.Lock()
	a.cloudSelfRevoked = revoked
	a.cloudMu.Unlock()
}

func (a *app) currentDeviceCloudRevoked() bool {
	if a == nil {
		return false
	}
	a.cloudMu.Lock()
	defer a.cloudMu.Unlock()
	return a.cloudSelfRevoked
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
			if knownHostRevoked(known) {
				continue
			}
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
		if normalizeCloudDeviceStatus(controller.Status) == cloudDeviceStatusRevoked {
			if request, ok := a.store.pairingRequestByCloudRequestID(signal.RequestID); ok && request.Status == PairingStatusPending {
				if _, err := a.store.denyPairingRequest(request.RequestID); err != nil {
					return err
				}
			}
			continue
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

func (a *app) cloudMarkOffline(settings CloudSettings, deviceID string) {
	if a == nil || a.store == nil || !settings.Enabled {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	if _, err := client.MarkDeviceOffline(ctx, deviceID); err != nil {
		log.Printf("astralops cloud offline: %v", err)
	}
}

func cloudSettingsChanged(left, right CloudSettings) bool {
	left = normalizedAppSettings(AppSettings{Cloud: left}).Cloud
	right = normalizedAppSettings(AppSettings{Cloud: right}).Cloud
	return left.Enabled != right.Enabled || left.BaseURL != right.BaseURL || left.AccountToken != right.AccountToken
}
