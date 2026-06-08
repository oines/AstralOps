package main

import (
	"net/http"

	"github.com/oines/astralops/daemon/internal/api"
)

func (a *app) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", a.handleHealth)
	mux.HandleFunc("/v1/control/ws", a.handleControlWS)
	mux.HandleFunc("/v1/host", a.auth(a.handleHost))
	mux.HandleFunc("/v1/snapshot", a.auth(a.handleHostSnapshot))
	mux.HandleFunc("/v1/workbench", a.auth(a.handleWorkbench))
	mux.HandleFunc("/v1/settings", a.auth(a.handleSettings))
	mux.HandleFunc("/v1/settings/", a.auth(a.handleSettingsAction))
	mux.HandleFunc("/v1/cloud/auth/callback", a.handleCloudAuthCallback)

	cloudAPI := api.NewCloudHandler(a.cloudCommands())
	mux.HandleFunc("/v1/cloud/auth/", a.auth(cloudAPI.HandleCloudAuthAction))
	mux.HandleFunc("/v1/cloud/account", a.auth(cloudAPI.HandleCloudAccount))
	mux.HandleFunc("/v1/cloud/account/relay", a.auth(cloudAPI.HandleCloudAccountRelay))
	mux.HandleFunc("/v1/cloud/relays", a.auth(cloudAPI.HandleCloudRelays))
	mux.HandleFunc("/v1/cloud/devices", a.auth(cloudAPI.HandleCloudDevices))
	mux.HandleFunc("/v1/cloud/devices/", a.auth(cloudAPI.HandleCloudDeviceAction))
	mux.HandleFunc("/v1/cloud/heartbeat", a.auth(cloudAPI.HandleCloudHeartbeat))
	mux.HandleFunc("/v1/cloud/pairing/requests", a.auth(cloudAPI.HandleCloudPairingRequests))
	mux.HandleFunc("/v1/cloud/pairing/requests/", a.auth(cloudAPI.HandleCloudPairingRequestAction))

	pairingAPI := api.NewPairingHandler(a.pairingCommands())
	mux.HandleFunc("/v1/pairing/requests", a.auth(pairingAPI.HandlePairingRequests))
	mux.HandleFunc("/v1/pairing/requests/", a.auth(pairingAPI.HandlePairingRequestAction))

	trustAPI := api.NewTrustHandler(a.trustCommands())
	mux.HandleFunc("/v1/trust/devices", a.auth(trustAPI.HandleTrustDevices))
	mux.HandleFunc("/v1/trust/devices/", a.auth(trustAPI.HandleTrustDeviceAction))

	meshAPI := api.NewMeshHandler(a.meshCommands())
	mux.HandleFunc("/v1/mesh/state", a.auth(meshAPI.HandleMeshState))

	remoteHostsAPI := api.NewRemoteHostsHandler(a.remoteHostCommands())
	mux.HandleFunc("/v1/remote/hosts", a.auth(remoteHostsAPI.HandleRemoteHosts))
	mux.HandleFunc("/v1/remote/hosts/", a.auth(remoteHostsAPI.HandleRemoteHostAction))

	workspacesAPI := api.NewWorkspacesHandler(a.workspaceCommands(), a.terminalCommands(), a.workspacePassthrough())
	mux.HandleFunc("/v1/fs/browse", a.auth(workspacesAPI.HandleHostFileSystemBrowse))
	mux.HandleFunc("/v1/workspaces", a.auth(workspacesAPI.HandleWorkspaces))
	mux.HandleFunc("/v1/workspaces/", a.auth(workspacesAPI.HandleWorkspaceAction))
	mux.HandleFunc("/v1/codex-exec/", a.auth(a.handleCodexExecServerWS))

	sessionsAPI := api.NewSessionsHandler(a.sessionCommands(), a.mediaCommands())
	mux.HandleFunc("/v1/sessions", a.auth(sessionsAPI.HandleSessions))
	mux.HandleFunc("/v1/sessions/", a.auth(sessionsAPI.HandleSessionAction))
	mux.HandleFunc("/v1/approvals/", a.auth(a.handleApprovalAction))

	eventsAPI := api.NewEventsHandler(a.eventCommands())
	mux.HandleFunc("/v1/events", a.auth(eventsAPI.HandleEvents))
}
