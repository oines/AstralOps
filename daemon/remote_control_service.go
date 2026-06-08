package main

import (
	"context"
	"sync"

	internalterminal "github.com/oines/astralops/daemon/internal/core/terminal"
	internalssh "github.com/oines/astralops/daemon/internal/ssh"
	"github.com/oines/astralops/pkg/controllercore"
	"github.com/oines/astralops/pkg/hostcore"
)

type remoteControlService struct {
	store                *store
	controlMu            *sync.Mutex
	controlSessions      *map[string]*controlWSConn
	controlRelaySessions *map[string]*controlRelaySession

	buildHostSnapshotFn                  func(hostSnapshotParams) hostSnapshotResult
	buildWorkbenchStateFn                func() workbenchState
	buildSessionViewFn                   func(string) (sessionView, bool)
	queryEventsWindowFn                  func(workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent
	emitFn                               func(AstralEvent)
	workspaceServiceFn                   func() *workspaceService
	sshServiceFn                         func() *internalssh.Service
	prepareControlEventSubscriptionFn    func(eventSubscriptionParams) (eventSubscriptionResult, error)
	streamControlEventsFn                func(context.Context, eventSubscriptionResult, controlConnection, string)
	mediaServiceFn                       func() *mediaService
	sessionsFn                           func() *sessionService
	createWorkspaceFn                    func(createWorkspaceRequest) (Workspace, error)
	deleteWorkspaceFn                    func(string) (map[string]any, error)
	terminalServiceFn                    func() *internalterminal.Service
	browseHostFileSystemFn               func(context.Context, hostFileSystemBrowseParams) (hostFileSystemBrowseResult, error)
	revokeTrustedControlDeviceFn         func(string, string) (hostTrustRevokeResult, error)
	approvePairingRequestFn              func(string) (pairingRequestResolveResult, error)
	denyPairingRequestFn                 func(string) (pairingRequestResolveResult, error)
	detachTerminalViewersForControlFn    func(string, string)
	cloudMeshActiveFn                    func() bool
	cloudMeshActiveForFn                 func(cloudMembershipRole) bool
	hostCoreManagerFn                    func() *hostcore.Core
	cloudClientFromSettingsFn            func() (CloudClient, error)
	cloudSyncApprovedPairingKnownHostsFn func(context.Context, CloudClient, []CloudDeviceRecord) error
	hostRemoteSessionManagerFn           func() *hostRemoteSessionManager
	controllerManagedTransportFn         func() *controllercore.ManagedTransport
	currentDeviceCloudRevokedFn          func() bool
}

func (a *app) remoteControlService() *remoteControlService {
	return newRemoteControlService(remoteControlServiceDepsFromApp(a))
}

func newRemoteControlService(deps remoteControlService) *remoteControlService {
	return &deps
}

func remoteControlServiceDepsFromApp(a *app) remoteControlService {
	if a == nil {
		return remoteControlService{}
	}
	return remoteControlService{
		store:                                a.store,
		controlMu:                            &a.controlMu,
		controlSessions:                      &a.controlSessions,
		controlRelaySessions:                 &a.controlRelaySessions,
		buildHostSnapshotFn:                  a.buildHostSnapshot,
		buildWorkbenchStateFn:                a.buildWorkbenchState,
		buildSessionViewFn:                   a.buildSessionView,
		queryEventsWindowFn:                  a.sessionProjections().QueryEventsWindow,
		emitFn:                               a.emit,
		workspaceServiceFn:                   a.workspaceService,
		sshServiceFn:                         a.sshService,
		prepareControlEventSubscriptionFn:    a.prepareControlEventSubscription,
		streamControlEventsFn:                a.streamControlEvents,
		mediaServiceFn:                       a.mediaService,
		sessionsFn:                           a.sessions,
		createWorkspaceFn:                    a.createWorkspace,
		deleteWorkspaceFn:                    a.deleteWorkspace,
		terminalServiceFn:                    a.terminalService,
		browseHostFileSystemFn:               a.browseHostFileSystem,
		revokeTrustedControlDeviceFn:         a.revokeTrustedControlDevice,
		approvePairingRequestFn:              a.approvePairingRequest,
		denyPairingRequestFn:                 a.denyPairingRequest,
		detachTerminalViewersForControlFn:    a.detachTerminalViewersForControlSession,
		cloudMeshActiveFn:                    a.cloudMeshActive,
		cloudMeshActiveForFn:                 a.cloudMeshActiveFor,
		hostCoreManagerFn:                    a.hostCoreManager,
		cloudClientFromSettingsFn:            a.cloudClientFromSettings,
		cloudSyncApprovedPairingKnownHostsFn: a.cloudSyncApprovedPairingKnownHosts,
		hostRemoteSessionManagerFn:           a.hostRemoteSessionManager,
		controllerManagedTransportFn:         a.controllerManagedTransport,
		currentDeviceCloudRevokedFn:          a.currentDeviceCloudRevoked,
	}
}

func (s *remoteControlService) buildHostSnapshot(params hostSnapshotParams) hostSnapshotResult {
	if s.buildHostSnapshotFn == nil {
		return hostSnapshotResult{}
	}
	return s.buildHostSnapshotFn(params)
}

func (s *remoteControlService) buildWorkbenchState() workbenchState {
	if s.buildWorkbenchStateFn == nil {
		return workbenchState{}
	}
	return s.buildWorkbenchStateFn()
}

func (s *remoteControlService) buildSessionView(sessionID string) (sessionView, bool) {
	if s.buildSessionViewFn == nil {
		return sessionView{}, false
	}
	return s.buildSessionViewFn(sessionID)
}

func (s *remoteControlService) queryEventsWindow(workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent {
	if s.queryEventsWindowFn != nil {
		return s.queryEventsWindowFn(workspaceID, sessionID, afterSeq, beforeSeq, limit)
	}
	return nil
}

func (s *remoteControlService) emit(event AstralEvent) {
	if s.emitFn != nil {
		s.emitFn(event)
	}
}

func (s *remoteControlService) workspaceService() *workspaceService {
	if s.workspaceServiceFn == nil {
		return nil
	}
	return s.workspaceServiceFn()
}

func (s *remoteControlService) prepareControlEventSubscription(params eventSubscriptionParams) (eventSubscriptionResult, error) {
	if s.prepareControlEventSubscriptionFn == nil {
		return eventSubscriptionResult{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.prepareControlEventSubscriptionFn(params)
}

func (s *remoteControlService) streamControlEvents(ctx context.Context, result eventSubscriptionResult, conn controlConnection, requestID string) {
	if s.streamControlEventsFn != nil {
		s.streamControlEventsFn(ctx, result, conn, requestID)
	}
}

func (s *remoteControlService) mediaService() *mediaService {
	if s.mediaServiceFn == nil {
		return nil
	}
	return s.mediaServiceFn()
}

func (s *remoteControlService) sessions() *sessionService {
	if s.sessionsFn == nil {
		return nil
	}
	return s.sessionsFn()
}

func (s *remoteControlService) createWorkspace(req createWorkspaceRequest) (Workspace, error) {
	if s.createWorkspaceFn == nil {
		return Workspace{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.createWorkspaceFn(req)
}

func (s *remoteControlService) deleteWorkspace(workspaceID string) (map[string]any, error) {
	if s.deleteWorkspaceFn == nil {
		return nil, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.deleteWorkspaceFn(workspaceID)
}

func (s *remoteControlService) terminalService() *internalterminal.Service {
	if s.terminalServiceFn == nil {
		return nil
	}
	return s.terminalServiceFn()
}

func (s *remoteControlService) sshService() *internalssh.Service {
	if s.sshServiceFn == nil {
		return nil
	}
	return s.sshServiceFn()
}

func (s *remoteControlService) browseHostFileSystem(ctx context.Context, params hostFileSystemBrowseParams) (hostFileSystemBrowseResult, error) {
	if s.browseHostFileSystemFn == nil {
		return hostFileSystemBrowseResult{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.browseHostFileSystemFn(ctx, params)
}

func (s *remoteControlService) revokeTrustedControlDevice(controllerDeviceID, exceptConnectionID string) (hostTrustRevokeResult, error) {
	if s.revokeTrustedControlDeviceFn == nil {
		return hostTrustRevokeResult{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.revokeTrustedControlDeviceFn(controllerDeviceID, exceptConnectionID)
}

func (s *remoteControlService) approvePairingRequest(requestID string) (pairingRequestResolveResult, error) {
	if s.approvePairingRequestFn == nil {
		return pairingRequestResolveResult{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.approvePairingRequestFn(requestID)
}

func (s *remoteControlService) denyPairingRequest(requestID string) (pairingRequestResolveResult, error) {
	if s.denyPairingRequestFn == nil {
		return pairingRequestResolveResult{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.denyPairingRequestFn(requestID)
}

func (s *remoteControlService) detachTerminalViewersForControlSession(connectionID, reason string) {
	if s.detachTerminalViewersForControlFn != nil {
		s.detachTerminalViewersForControlFn(connectionID, reason)
	}
}

func (s *remoteControlService) cloudMeshActive() bool {
	return s.cloudMeshActiveFn != nil && s.cloudMeshActiveFn()
}

func (s *remoteControlService) cloudMeshActiveFor(role cloudMembershipRole) bool {
	return s.cloudMeshActiveForFn != nil && s.cloudMeshActiveForFn(role)
}

func (s *remoteControlService) hostCoreManager() *hostcore.Core {
	if s.hostCoreManagerFn == nil {
		return nil
	}
	return s.hostCoreManagerFn()
}

func (s *remoteControlService) cloudClientFromSettings() (CloudClient, error) {
	if s.cloudClientFromSettingsFn == nil {
		return CloudClient{}, newActionError(503, "remote_control_unavailable", "remote control service is not initialized")
	}
	return s.cloudClientFromSettingsFn()
}

func (s *remoteControlService) cloudSyncApprovedPairingKnownHosts(ctx context.Context, client CloudClient, devices []CloudDeviceRecord) error {
	if s.cloudSyncApprovedPairingKnownHostsFn == nil {
		return nil
	}
	return s.cloudSyncApprovedPairingKnownHostsFn(ctx, client, devices)
}

func (s *remoteControlService) hostRemoteSessionManager() *hostRemoteSessionManager {
	if s.hostRemoteSessionManagerFn == nil {
		return nil
	}
	return s.hostRemoteSessionManagerFn()
}

func (s *remoteControlService) controllerManagedTransport() *controllercore.ManagedTransport {
	if s.controllerManagedTransportFn == nil {
		return nil
	}
	return s.controllerManagedTransportFn()
}

func (s *remoteControlService) currentDeviceCloudRevoked() bool {
	return s.currentDeviceCloudRevokedFn != nil && s.currentDeviceCloudRevokedFn()
}

func (s *remoteControlService) wsSessions() map[string]*controlWSConn {
	if s == nil || s.controlSessions == nil {
		return nil
	}
	if *s.controlSessions == nil {
		*s.controlSessions = map[string]*controlWSConn{}
	}
	return *s.controlSessions
}

func (s *remoteControlService) relaySessions() map[string]*controlRelaySession {
	if s == nil || s.controlRelaySessions == nil {
		return nil
	}
	if *s.controlRelaySessions == nil {
		*s.controlRelaySessions = map[string]*controlRelaySession{}
	}
	return *s.controlRelaySessions
}

func (a *app) executeControlRequest(req ControlRequest) (ControlResponse, error) {
	return a.remoteControlService().executeControlRequest(req)
}

func (a *app) executeControlRequestWithConnection(req ControlRequest, conn controlConnection) (ControlResponse, error) {
	return a.remoteControlService().executeControlRequestWithConnection(req, conn)
}

func (a *app) executeControlRequestWithContext(ctx context.Context, req ControlRequest, conn controlConnection) (ControlResponse, error) {
	return a.remoteControlService().executeControlRequestWithContext(ctx, req, conn)
}

func (a *app) executeAuthorizedControlRequestWithContext(ctx context.Context, req ControlRequest, conn controlConnection, grant TrustGrant) (ControlResponse, error) {
	return a.remoteControlService().executeAuthorizedControlRequestWithContext(ctx, req, conn, grant)
}

func (a *app) afterControlResponse(conn controlConnection, req ControlRequest, response ControlResponse) func() {
	return a.remoteControlService().afterControlResponse(conn, req, response)
}

func (a *app) registerControlSession(conn *controlWSConn) {
	a.remoteControlService().registerControlSession(conn)
}

func (a *app) unregisterControlSession(connectionID string) {
	a.remoteControlService().unregisterControlSession(connectionID)
}

func (a *app) closeControlSessionsForDevice(controllerDeviceID, reason string) int {
	return a.remoteControlService().closeControlSessionsForDevice(controllerDeviceID, reason)
}

func (a *app) closeAllControlSessions(reason string) int {
	return a.remoteControlService().closeAllControlSessions(reason)
}

func (a *app) closeControlSessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID string) int {
	return a.remoteControlService().closeControlSessionsForDeviceExcept(controllerDeviceID, reason, exceptConnectionID)
}

func (a *app) activeControlSessionCountForDevice(controllerDeviceID string) int {
	return a.remoteControlService().activeControlSessionCountForDevice(controllerDeviceID)
}

func (a *app) buildRemoteHostRecords(ctx context.Context, discover bool) []remoteHostRecord {
	return a.remoteControlService().buildRemoteHostRecords(ctx, discover)
}

func (a *app) remoteHostTarget(hostDeviceID string) (controlClientTarget, error) {
	return a.remoteControlService().remoteHostTarget(hostDeviceID)
}

func (a *app) remoteHostTargetWithPreference(hostDeviceID string, preferRelay bool) (controlClientTarget, error) {
	return a.remoteControlService().remoteHostTargetWithPreference(hostDeviceID, preferRelay)
}

func (a *app) remoteControlResponse(hostDeviceID string, capability ControlCapability, action ControlAction, params map[string]any) (ControlResponse, error) {
	return a.remoteControlService().remoteControlResponse(hostDeviceID, capability, action, params)
}
