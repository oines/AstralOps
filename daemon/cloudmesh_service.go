package main

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type cloudmeshService struct {
	store               *store
	settings            *settingsStore
	cloudMu             *sync.Mutex
	cloudCancel         *context.CancelFunc
	cloudSettings       *CloudSettings
	cloudSelfRevoked    *bool
	cloudRelayConnected *bool

	currentSettings                 func() AppSettings
	refreshMeshStateAsync           func(bool)
	revokeTrustedControlDevice      func(string, string) (hostTrustRevokeResult, error)
	closeControlSessionsForDevice   func(string, string) int
	closeAllControlSessions         func(string) int
	releaseTerminalWritersForDevice func(string) int
	emitPairingRequested            func(PairingRequest)
	controllerInvalidateAll         func(string)
	hostRoleEnabled                 func() bool
	expireIdleControlRelaySessions  func(time.Time)
	handleControlRelayEnvelope      func(context.Context, relayEnvelopeTransport, RelayEnvelope) error
}

func (a *app) cloudmeshService() *cloudmeshService {
	if a == nil {
		return nil
	}
	return newCloudmeshService(cloudmeshDepsFromApp(a))
}

func newCloudmeshService(deps cloudmeshService) *cloudmeshService {
	return &deps
}

func cloudmeshDepsFromApp(a *app) cloudmeshService {
	if a == nil {
		return cloudmeshService{}
	}
	deps := cloudmeshService{
		store:                           a.store,
		settings:                        a.settings,
		cloudMu:                         &a.cloudMu,
		cloudCancel:                     &a.cloudCancel,
		cloudSettings:                   &a.cloudSettings,
		cloudSelfRevoked:                &a.cloudSelfRevoked,
		cloudRelayConnected:             &a.cloudRelayConnected,
		currentSettings:                 a.currentSettings,
		refreshMeshStateAsync:           a.refreshMeshStateAsync,
		revokeTrustedControlDevice:      a.revokeTrustedControlDevice,
		closeControlSessionsForDevice:   a.closeControlSessionsForDevice,
		closeAllControlSessions:         a.closeAllControlSessions,
		releaseTerminalWritersForDevice: a.releaseTerminalWritersForDevice,
		emitPairingRequested:            a.emitPairingRequested,
		hostRoleEnabled:                 a.hostRoleEnabled,
		expireIdleControlRelaySessions:  a.expireIdleControlRelaySessions,
		handleControlRelayEnvelope:      a.remoteControlService().handleControlRelayEnvelope,
	}
	deps.controllerInvalidateAll = func(reason string) {
		if transport := a.controllerManagedTransport(); transport != nil {
			transport.InvalidateAll(reason)
		}
	}
	return deps
}

func (a *app) applyCloudSettings(settings CloudSettings) error {
	return a.cloudmeshService().applyCloudSettings(settings)
}

func (a *app) restartCloudSync(settings CloudSettings) error {
	return a.cloudmeshService().restartCloudSync(settings)
}

func (a *app) applyCloudSettingsWithOffline(settings CloudSettings, markOfflineOnStop bool) error {
	return a.cloudmeshService().applyCloudSettingsWithOffline(settings, markOfflineOnStop)
}

func (a *app) cloudSyncLoop(ctx context.Context, settings CloudSettings, markOfflineOnStop bool) {
	a.cloudmeshService().cloudSyncLoop(ctx, settings, markOfflineOnStop)
}

func (a *app) startCloudRelayWebSocketLoop(ctx context.Context, cloudClient CloudClient, initialClient RelayClient, relayURL string, hasRelay bool) context.CancelFunc {
	return a.cloudmeshService().startCloudRelayWebSocketLoop(ctx, cloudClient, initialClient, relayURL, hasRelay)
}

func (a *app) syncCloudRegistrationSoon(settings AppSettings) {
	a.cloudmeshService().syncCloudRegistrationSoon(settings)
}

func (a *app) cloudRegisterAndHeartbeat(ctx context.Context, client CloudClient) error {
	return a.cloudmeshService().cloudRegisterAndHeartbeat(ctx, client)
}

func (a *app) cloudSyncRevokedDevices(devices []CloudDeviceRecord) (bool, error) {
	return a.cloudmeshService().cloudSyncRevokedDevices(devices)
}

func (a *app) setCloudSelfRevoked(revoked bool) {
	a.cloudmeshService().setCloudSelfRevoked(revoked)
}

func (a *app) currentDeviceCloudRevoked() bool {
	return a.cloudmeshService().currentDeviceCloudRevoked()
}

func (a *app) cloudSyncApprovedPairingKnownHosts(ctx context.Context, client CloudClient, devices []CloudDeviceRecord) error {
	return a.cloudmeshService().cloudSyncApprovedPairingKnownHosts(ctx, client, devices)
}

func (a *app) cloudSyncPendingPairingRequests(ctx context.Context, client CloudClient) error {
	return a.cloudmeshService().cloudSyncPendingPairingRequests(ctx, client)
}

func (a *app) cloudResolvePairingSignal(ctx context.Context, client CloudClient, request PairingRequest) {
	a.cloudmeshService().cloudResolvePairingSignal(ctx, client, request)
}

func (a *app) syncCloudPairingResolution(request PairingRequest) {
	a.cloudmeshService().syncCloudPairingResolution(request)
}

func (a *app) cloudMarkOffline(settings CloudSettings, deviceID string) {
	a.cloudmeshService().cloudMarkOffline(settings, deviceID)
}

func (a *app) cloudMeshActive() bool {
	return a.cloudmeshService().cloudMeshActive()
}

func (a *app) cloudMeshActiveFor(role cloudMembershipRole) bool {
	return a.cloudmeshService().cloudMeshActiveFor(role)
}

func (a *app) requireCloudMeshRemoteControl(w http.ResponseWriter) bool {
	return a.cloudmeshService().requireCloudMeshRemoteControl(w)
}

func (a *app) cancelCloudSync() {
	a.cloudmeshService().cancelCloudSync()
}

func (a *app) logoutCloudMesh(ctx context.Context, removeSelf bool) (cloudMeshLogoutResult, error) {
	return a.cloudmeshService().logoutCloudMesh(ctx, removeSelf)
}

func (a *app) handleCloudSelfRevoked() {
	a.cloudmeshService().handleCloudSelfRevoked()
}

func (a *app) meshCloudState(ctx context.Context, settings CloudSettings) *meshCloudState {
	return a.cloudmeshService().meshCloudState(ctx, settings)
}

func (a *app) setCloudRelayConnected(connected bool) {
	a.cloudmeshService().setCloudRelayConnected(connected)
}

func (a *app) currentCloudRelayConnected() bool {
	return a.cloudmeshService().currentCloudRelayConnected()
}

func (a *app) cloudPollRelayEnvelopes(ctx context.Context, client RelayClient) error {
	return a.cloudmeshService().cloudPollRelayEnvelopes(ctx, client)
}

func (a *app) cloudRunRelayWebSocket(ctx context.Context, client RelayClient) error {
	return a.cloudmeshService().cloudRunRelayWebSocket(ctx, client)
}
