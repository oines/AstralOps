package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	remoteHostStatusLAN     = "lan"
	remoteHostStatusRelay   = "relay"
	remoteHostStatusOnline  = "online"
	remoteHostStatusOffline = "offline"
	remoteHostDiscoveryTTL  = 1500 * time.Millisecond
	remoteHostCloudTimeout  = 4 * time.Second
	remoteHostLANTimeout    = 2 * time.Second

	remoteHostAuthorizationNeedsPairing = "needs_pairing"
	remoteHostAuthorizationPending      = "pending"
	remoteHostAuthorizationApproved     = "approved"
	remoteHostAuthorizationDenied       = "denied"
	remoteHostAuthorizationKnown        = "known"
)

type remoteHostsResponse struct {
	Hosts []remoteHostRecord `json:"hosts"`
}

type remoteHostRecord struct {
	DeviceID             string                 `json:"device_id"`
	DeviceName           string                 `json:"device_name,omitempty"`
	DeviceKind           string                 `json:"device_kind,omitempty"`
	PublicKeyFingerprint string                 `json:"public_key_fingerprint"`
	KnownIdentity        bool                   `json:"known_identity,omitempty"`
	Status               string                 `json:"status"`
	Connection           string                 `json:"connection"`
	AuthorizationState   string                 `json:"authorization_state,omitempty"`
	PairingRequestID     string                 `json:"pairing_request_id,omitempty"`
	PairingStatus        string                 `json:"pairing_status,omitempty"`
	LastBaseURL          string                 `json:"last_base_url,omitempty"`
	LANBaseURL           string                 `json:"lan_base_url,omitempty"`
	Capabilities         []string               `json:"capabilities,omitempty"`
	Control              remoteHostControlState `json:"control"`
}

func (a *app) handleRemoteHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.mesh != nil {
		state, err := a.mesh.refresh(r.Context(), truthyQuery(r.URL.Query().Get("discover")))
		if err != nil {
			writeRemoteHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, remoteHostsResponse{Hosts: state.Hosts})
		return
	}
	writeJSON(w, http.StatusOK, remoteHostsResponse{Hosts: a.buildRemoteHostRecords(r.Context(), truthyQuery(r.URL.Query().Get("discover")))})
}

func (a *app) buildRemoteHostRecords(ctx context.Context, discover bool) []remoteHostRecord {
	hosts := map[string]remoteHostRecord{}
	if !a.cloudMeshActiveFor(cloudMembershipRole{CanControl: true}) {
		return []remoteHostRecord{}
	}
	a.mergeCloudRemoteHosts(ctx, hosts)
	if discover {
		a.mergeDiscoveredRemoteHosts(hosts)
	}
	out := make([]remoteHostRecord, 0, len(hosts))
	for _, host := range hosts {
		if a.remoteManager != nil {
			host.Control = a.remoteManager.controlState(host.DeviceID)
		}
		out = append(out, host)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connection != out[j].Connection {
			return remoteHostConnectionRank(out[i].Connection) < remoteHostConnectionRank(out[j].Connection)
		}
		left := strings.ToLower(firstString(out[i].DeviceName, out[i].DeviceID))
		right := strings.ToLower(firstString(out[j].DeviceName, out[j].DeviceID))
		return left < right
	})
	return out
}

func (a *app) mergeCloudRemoteHosts(ctx context.Context, hosts map[string]remoteHostRecord) {
	if a == nil || a.store == nil {
		return
	}
	client, err := a.cloudClientFromSettings()
	if err != nil {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, remoteHostCloudTimeout)
	defer cancel()
	devices, err := client.ListDevices(reqCtx)
	if err != nil {
		return
	}
	_ = a.cloudSyncApprovedPairingKnownHosts(reqCtx, client, devices)
	selfID := a.store.hostInfo().Identity.DeviceID
	pairingSignals := a.remoteHostPairingSignalsByHost(reqCtx, client, selfID)
	for _, device := range devices {
		if normalizeCloudDeviceStatus(device.Status) == cloudDeviceStatusRevoked {
			delete(hosts, device.DeviceID)
			continue
		}
		if device.DeviceID == "" || device.DeviceID == selfID || !device.CanHost {
			continue
		}
		existing := hosts[device.DeviceID]
		if known, ok := a.store.knownHost(device.DeviceID); ok {
			existing = remoteHostRecordFromKnownHost(known)
		}
		if existing.PublicKeyFingerprint != "" && device.PublicKeyFingerprint != "" && existing.PublicKeyFingerprint != device.PublicKeyFingerprint {
			continue
		}
		record := remoteHostRecordFromCloudDevice(device, existing)
		record = remoteHostRecordWithPairingState(record, pairingSignals[device.DeviceID])
		hosts[device.DeviceID] = record
	}
}

func (a *app) remoteHostPairingSignalsByHost(ctx context.Context, client CloudClient, controllerDeviceID string) map[string]CloudPairingSignal {
	controllerDeviceID = strings.TrimSpace(controllerDeviceID)
	if controllerDeviceID == "" {
		return nil
	}
	signals, err := client.ListPairingSignals(ctx, controllerDeviceID)
	if err != nil {
		return nil
	}
	out := map[string]CloudPairingSignal{}
	for _, signal := range signals {
		if strings.TrimSpace(signal.ControllerDeviceID) != controllerDeviceID {
			continue
		}
		hostID := strings.TrimSpace(signal.HostDeviceID)
		if hostID == "" || hostID == controllerDeviceID {
			continue
		}
		if existing, ok := out[hostID]; ok && !cloudPairingSignalNewer(signal, existing) {
			continue
		}
		out[hostID] = signal
	}
	return out
}

func cloudPairingSignalNewer(left, right CloudPairingSignal) bool {
	leftTime := firstString(strings.TrimSpace(left.UpdatedAt), strings.TrimSpace(left.CreatedAt))
	rightTime := firstString(strings.TrimSpace(right.UpdatedAt), strings.TrimSpace(right.CreatedAt))
	if leftTime == "" || rightTime == "" {
		return left.RequestID > right.RequestID
	}
	return leftTime > rightTime
}

func (a *app) mergeDiscoveredRemoteHosts(hosts map[string]remoteHostRecord) {
	candidates, err := discoverRemoteControlHostsWithTimeout(remoteHostDiscoveryTTL, defaultRemoteControlDiscoveryPort)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: remoteHostLANTimeout}
	for _, candidate := range candidates {
		existing, ok := hosts[candidate.DeviceID]
		if !ok {
			continue
		}
		known, ok := a.store.knownHost(candidate.DeviceID)
		if !ok || known.PublicKeyFingerprint != candidate.PublicKeyFingerprint {
			continue
		}
		if knownHostRevoked(known) {
			continue
		}
		hostInfo, err := controlClientHostInfoWithClient(candidate.BaseURL, client)
		if err != nil {
			continue
		}
		if err := validateKnownLanHost(candidate, known, hostInfo); err != nil {
			continue
		}
		known = a.rememberRemoteHostLANRoute(hostInfo, candidate.BaseURL, known)
		if a.remoteManager != nil {
			a.remoteManager.clearLANFailure(candidate.DeviceID)
		}
		next := remoteHostRecordFromHostInfo(hostInfo, known, candidate.BaseURL)
		if next.Capabilities == nil {
			next.Capabilities = existing.Capabilities
		}
		next.AuthorizationState = existing.AuthorizationState
		next.PairingRequestID = existing.PairingRequestID
		next.PairingStatus = existing.PairingStatus
		hosts[candidate.DeviceID] = next
	}
}

func remoteHostRecordFromCloudDevice(device CloudDeviceRecord, existing remoteHostRecord) remoteHostRecord {
	record := existing
	if record.DeviceID == "" {
		record.DeviceID = device.DeviceID
	}
	if record.DeviceName == "" {
		record.DeviceName = device.DeviceName
	}
	if record.DeviceKind == "" {
		record.DeviceKind = device.DeviceKind
	}
	if record.PublicKeyFingerprint == "" {
		record.PublicKeyFingerprint = device.PublicKeyFingerprint
	}
	if len(record.Capabilities) == 0 {
		record.Capabilities = normalizeCapabilities(device.Capabilities)
	}
	if record.AuthorizationState == "" {
		if record.KnownIdentity {
			record.AuthorizationState = remoteHostAuthorizationKnown
		} else {
			record.AuthorizationState = remoteHostAuthorizationNeedsPairing
		}
	}
	if record.Connection == remoteHostStatusLAN {
		return record
	}
	if device.Status == cloudDeviceStatusOnline {
		record.Status = remoteHostStatusOnline
		record.Connection = remoteHostStatusRelay
	} else if record.Connection == "" {
		record.Status = remoteHostStatusOffline
		record.Connection = remoteHostStatusOffline
	}
	return record
}

func remoteHostRecordFromKnownHost(host KnownHost) remoteHostRecord {
	return remoteHostRecord{
		DeviceID:             host.DeviceID,
		DeviceName:           host.DeviceName,
		PublicKeyFingerprint: host.PublicKeyFingerprint,
		KnownIdentity:        true,
		Status:               remoteHostStatusOffline,
		Connection:           remoteHostStatusOffline,
		LastBaseURL:          host.LastBaseURL,
		AuthorizationState:   remoteHostAuthorizationKnown,
	}
}

func remoteHostConnectionRank(connection string) int {
	switch connection {
	case remoteHostStatusLAN:
		return 0
	case "relay":
		return 1
	case remoteHostStatusOffline:
		return 3
	default:
		return 4
	}
}

func remoteHostRecordFromHostInfo(info HostInfo, known KnownHost, lanBaseURL string) remoteHostRecord {
	identity := info.Identity
	name := strings.TrimSpace(identity.DeviceName)
	if name == "" {
		name = known.DeviceName
	}
	return remoteHostRecord{
		DeviceID:             identity.DeviceID,
		DeviceName:           name,
		DeviceKind:           identity.DeviceKind,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		KnownIdentity:        true,
		Status:               remoteHostStatusLAN,
		Connection:           remoteHostStatusLAN,
		LastBaseURL:          known.LastBaseURL,
		LANBaseURL:           strings.TrimRight(strings.TrimSpace(lanBaseURL), "/"),
		Capabilities:         info.Capabilities,
		AuthorizationState:   remoteHostAuthorizationKnown,
	}
}

func remoteHostRecordWithPairingState(record remoteHostRecord, signal CloudPairingSignal) remoteHostRecord {
	status := strings.TrimSpace(signal.Status)
	if signal.RequestID != "" {
		record.PairingRequestID = strings.TrimSpace(signal.RequestID)
		record.PairingStatus = status
	}
	switch status {
	case PairingStatusPending:
		record.AuthorizationState = remoteHostAuthorizationPending
	case PairingStatusDenied:
		record.AuthorizationState = remoteHostAuthorizationDenied
	case PairingStatusApproved:
		if record.KnownIdentity {
			record.AuthorizationState = remoteHostAuthorizationApproved
		} else {
			record.AuthorizationState = remoteHostAuthorizationNeedsPairing
		}
	default:
		if record.KnownIdentity {
			record.AuthorizationState = firstString(record.AuthorizationState, remoteHostAuthorizationKnown)
		} else {
			record.AuthorizationState = remoteHostAuthorizationNeedsPairing
		}
	}
	return record
}

func (a *app) handleRemoteHostAction(w http.ResponseWriter, r *http.Request) {
	parts, ok := remoteHostPathParts(r.URL.Path)
	if !ok || len(parts) < 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	hostDeviceID := parts[0]
	route := parts[1:]

	switch {
	case len(route) == 1 && route[0] == "workbench" && r.Method == http.MethodGet:
		if r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			a.handleRemoteHostWorkbenchSSE(w, r, hostDeviceID)
			return
		}
		a.writeRemoteWorkbenchResult(w, hostDeviceID)
	case len(route) == 1 && route[0] == "snapshot" && r.Method == http.MethodGet:
		eventLimit, _ := strconv.Atoi(r.URL.Query().Get("event_limit"))
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionHostSnapshot, map[string]any{
			"event_limit":       eventLimit,
			"restore_on_launch": truthyQuery(r.URL.Query().Get("restore_on_launch")),
		})
	case len(route) == 1 && route[0] == "host" && r.Method == http.MethodGet:
		target, err := a.remoteHostTarget(hostDeviceID)
		if err != nil {
			writeRemoteHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, target.HostInfo)
	case len(route) == 1 && route[0] == "workspaces" && r.Method == http.MethodGet:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionWorkspaces, nil)
	case len(route) == 1 && route[0] == "workspaces" && r.Method == http.MethodPost:
		var req createWorkspaceRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionWorkspaceCreate, map[string]any{
			"name":      req.Name,
			"target":    req.Target,
			"agent":     req.Agent,
			"local_cwd": req.LocalCWD,
			"ssh":       req.SSH,
		})
	case len(route) == 2 && route[0] == "fs" && route[1] == "browse" && r.Method == http.MethodPost:
		var req hostFileSystemBrowseParams
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityHostFileSystemBrowse, ControlActionHostFileSystemBrowse, map[string]any{
			"target": req.Target,
			"path":   req.Path,
			"ssh":    req.SSH,
		})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "files" && r.Method == http.MethodGet:
		a.writeRemoteWorkspaceFilesResult(w, hostDeviceID, map[string]any{
			"workspace_id": route[1],
			"path":         r.URL.Query().Get("path"),
			"mode":         "list",
		})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "connection" && r.Method == http.MethodGet:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionWorkspaceConnection, map[string]any{"workspace_id": route[1]})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "connect" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionWorkspaceConnect, map[string]any{"workspace_id": route[1]})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "disconnect" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionWorkspaceDisconnect, map[string]any{"workspace_id": route[1]})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "terminal" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityTerminalOpen, ControlActionTerminalOpen, map[string]any{
			"workspace_id": route[1],
			"cols":         defaultTerminalCols,
			"rows":         defaultTerminalRows,
		})
	case len(route) == 4 && route[0] == "workspaces" && route[2] == "terminals" && r.Method == http.MethodDelete:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityTerminalInput, ControlActionTerminalClose, map[string]any{"terminal_id": route[3]})
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "pty" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket"):
		a.handleRemoteHostWorkspacePTY(w, r, hostDeviceID, route[1])
	case len(route) == 3 && route[0] == "workspaces" && route[2] == "exec" && r.Method == http.MethodPost:
		var req struct {
			Command   string `json:"command"`
			CWD       string `json:"cwd"`
			TimeoutMS int    `json:"timeout_ms"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityWorkspaceExec, ControlActionWorkspaceExec, map[string]any{
			"workspace_id": route[1],
			"command":      req.Command,
			"cwd":          req.CWD,
			"timeout_ms":   req.TimeoutMS,
		})
	case len(route) == 1 && route[0] == "sessions" && r.Method == http.MethodGet:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionSessions, map[string]any{
			"workspace_id": r.URL.Query().Get("workspace_id"),
		})
	case len(route) == 1 && route[0] == "sessions" && r.Method == http.MethodPost:
		var req createSessionRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionSessionCreate, map[string]any{
			"workspace_id": req.WorkspaceID,
			"agent":        req.Agent,
		})
	case len(route) == 2 && route[0] == "pairing" && route[1] == "requests" && r.Method == http.MethodGet:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityHostManage, ControlActionHostPairingList, nil)
	case len(route) == 4 && route[0] == "pairing" && route[1] == "requests" && route[3] == "approve" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityHostManage, ControlActionHostPairingApprove, map[string]any{"request_id": route[2]})
	case len(route) == 4 && route[0] == "pairing" && route[1] == "requests" && route[3] == "deny" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityHostManage, ControlActionHostPairingDeny, map[string]any{"request_id": route[2]})
	case len(route) == 3 && route[0] == "sessions" && route[2] == "view" && r.Method == http.MethodGet:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionSessionView, map[string]any{"session_id": route[1]})
	case len(route) == 3 && route[0] == "sessions" && route[2] == "input" && r.Method == http.MethodPost:
		var req struct {
			Input           string            `json:"input"`
			Model           string            `json:"model"`
			ReasoningEffort string            `json:"reasoning_effort"`
			PermissionMode  string            `json:"permission_mode"`
			Attachments     []InputAttachment `json:"attachments"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionSessionInput, map[string]any{
			"session_id":       route[1],
			"input":            req.Input,
			"model":            req.Model,
			"reasoning_effort": req.ReasoningEffort,
			"permission_mode":  req.PermissionMode,
			"attachments":      sanitizeInputAttachments(req.Attachments),
		})
	case len(route) == 3 && route[0] == "sessions" && route[2] == "interrupt" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionInterrupt, map[string]any{"session_id": route[1]})
	case len(route) == 3 && route[0] == "sessions" && route[2] == "fork" && r.Method == http.MethodPost:
		var req forkSessionRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionSessionFork, map[string]any{"session_id": route[1], "event_seq": req.EventSeq})
	case len(route) == 3 && route[0] == "sessions" && route[2] == "edit-last-user-message" && r.Method == http.MethodPost:
		var req editLastUserMessageRequest
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilitySessionEdit, ControlActionSessionEdit, map[string]any{
			"session_id":       route[1],
			"event_seq":        req.EventSeq,
			"input":            req.Input,
			"model":            req.Model,
			"reasoning_effort": req.ReasoningEffort,
			"permission_mode":  req.PermissionMode,
		})
	case len(route) == 2 && route[0] == "sessions" && r.Method == http.MethodDelete:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionSessionDelete, map[string]any{"session_id": route[1]})
	case len(route) == 5 && route[0] == "sessions" && route[2] == "queue" && route[4] == "cancel" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionQueueCancel, map[string]any{"session_id": route[1], "queue_id": route[3]})
	case len(route) == 5 && route[0] == "sessions" && route[2] == "queue" && route[4] == "steer" && r.Method == http.MethodPost:
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreControl, ControlActionQueueSteer, map[string]any{"session_id": route[1], "queue_id": route[3]})
	case len(route) == 1 && route[0] == "events" && r.Method == http.MethodGet:
		if r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			a.handleRemoteHostEventsSSE(w, r, hostDeviceID)
			return
		}
		afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
		beforeSeq, _ := strconv.ParseInt(r.URL.Query().Get("before_seq"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityCoreRead, ControlActionEvents, map[string]any{
			"workspace_id": r.URL.Query().Get("workspace_id"),
			"session_id":   r.URL.Query().Get("session_id"),
			"after_seq":    afterSeq,
			"before_seq":   beforeSeq,
			"limit":        limit,
		})
	case len(route) == 3 && route[0] == "approvals" && route[2] == "respond" && r.Method == http.MethodPost:
		var response map[string]any
		if err := decodeJSON(r.Body, &response); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		a.writeRemoteControlResult(w, hostDeviceID, CapabilityInteractionRespond, ControlActionInteractionRespond, map[string]any{
			"interaction_id": route[1],
			"response":       response,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *app) writeRemoteWorkbenchResult(w http.ResponseWriter, hostDeviceID string) {
	manager := a.remoteControlManager()
	if manager == nil {
		writeRemoteHostError(w, errors.New("remote control manager is not initialized"))
		return
	}
	response, err := manager.Request(context.Background(), hostDeviceID, CapabilityCoreRead, ControlActionHostSnapshot, map[string]any{"event_limit": 1})
	if err != nil {
		writeRemoteHostError(w, err)
		return
	}
	if !response.OK {
		writeControlHTTPResult(w, response, ControlActionHostSnapshot)
		return
	}
	workbench, err := remoteWorkbenchFromSnapshotResult(response.Result)
	if err != nil {
		writeRemoteHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workbench)
}

func (a *app) handleRemoteHostWorkbenchSSE(w http.ResponseWriter, r *http.Request, hostDeviceID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	manager := a.remoteControlManager()
	if manager == nil {
		writeSSE(w, flusher, "workbench.error", map[string]string{"error": "remote control manager is not initialized"})
		return
	}
	load := func() (workbenchState, error) {
		response, err := manager.Request(r.Context(), hostDeviceID, CapabilityCoreRead, ControlActionHostSnapshot, map[string]any{"event_limit": 1})
		if err != nil {
			return workbenchState{}, err
		}
		if !response.OK {
			return workbenchState{}, controlResponseActionError(response, ControlActionHostSnapshot)
		}
		return remoteWorkbenchFromSnapshotResult(response.Result)
	}

	current := workbenchState{}
	if next, err := load(); err == nil {
		writeSSE(w, flusher, "workbench.patch", diffWorkbenchState(current, next))
		current = next
	} else {
		writeSSE(w, flusher, "workbench.error", map[string]string{"error": err.Error()})
	}
	stream, err := manager.SubscribeEvents(r.Context(), hostDeviceID, eventSubscriptionParams{})
	if err != nil {
		writeSSE(w, flusher, "workbench.error", map[string]string{"error": err.Error()})
		return
	}
	defer stream.Close()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
		case _, ok := <-stream.Events:
			if !ok {
				writeSSE(w, flusher, "workbench.error", map[string]string{"error": "remote workbench stream closed"})
				return
			}
			next, err := load()
			if err != nil {
				writeSSE(w, flusher, "workbench.error", map[string]string{"error": err.Error()})
				continue
			}
			patch := diffWorkbenchState(current, next)
			if len(patch.Ops) > 0 {
				writeSSE(w, flusher, "workbench.patch", patch)
			}
			current = next
		}
	}
}

func remoteWorkbenchFromSnapshotResult(result any) (workbenchState, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return workbenchState{}, err
	}
	var snapshot struct {
		Workbench workbenchState `json:"workbench"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return workbenchState{}, err
	}
	if snapshot.Workbench.Workspaces == nil {
		return workbenchState{}, errors.New("remote snapshot response missing workbench")
	}
	return snapshot.Workbench, nil
}

func (a *app) handleRemoteHostWorkspacePTY(w http.ResponseWriter, r *http.Request, hostDeviceID, workspaceID string) {
	local, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer local.Close()

	manager := a.remoteControlManager()
	if manager == nil {
		_ = local.WriteJSON(map[string]any{"type": "error", "message": "remote control manager is not initialized"})
		return
	}
	terminalID := strings.TrimSpace(r.URL.Query().Get("terminal_id"))
	var terminal *remoteManagedTerminalStream
	if terminalID != "" {
		terminal, err = manager.AttachTerminal(r.Context(), hostDeviceID, terminalID)
	} else {
		terminal, err = manager.OpenTerminal(r.Context(), hostDeviceID, workspaceID)
	}
	if err != nil {
		_ = local.WriteJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer terminal.Detach()

	localWriter := &remotePTYLocalWriter{}
	localWriter.write(local, map[string]any{
		"type":        "ready",
		"terminal_id": terminal.terminalID,
		"shell":       terminal.shell,
		"cwd":         terminal.cwd,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame := range terminal.Frames() {
			if frame.Response != nil && !frame.Response.OK {
				localWriter.write(local, map[string]any{"type": "error", "message": controlResponseMessage(*frame.Response)})
				continue
			}
			if frame.Terminal == nil || frame.Terminal.TerminalID != terminal.terminalID {
				continue
			}
			switch frame.Type {
			case terminalFrameOutput:
				if frame.Terminal.Data != "" {
					localWriter.write(local, map[string]any{"type": "output", "data": frame.Terminal.Data})
				}
			case terminalFrameClosed:
				localWriter.write(local, map[string]any{"type": "exit", "reason": frame.Terminal.Reason})
				return
			}
		}
		localWriter.write(local, map[string]any{"type": "exit"})
	}()

	clientReads := make(chan remotePTYClientRead, 1)
	go func() {
		defer close(clientReads)
		for {
			var message ptyClientMessage
			if err := local.ReadJSON(&message); err != nil {
				select {
				case clientReads <- remotePTYClientRead{err: err}:
				case <-done:
				}
				return
			}
			select {
			case clientReads <- remotePTYClientRead{message: message}:
			case <-done:
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case read, ok := <-clientReads:
			if !ok {
				return
			}
			if read.err != nil {
				return
			}
			if err := remoteTerminalHandleManagedClientMessage(terminal, read.message); err != nil {
				localWriter.write(local, map[string]any{"type": "error", "message": err.Error()})
			}
		}
	}
}

type remotePTYClientRead struct {
	message ptyClientMessage
	err     error
}

type remotePTYLocalWriter struct {
	mu sync.Mutex
}

func (w *remotePTYLocalWriter) write(conn interface{ WriteJSON(any) error }, payload map[string]any) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return conn.WriteJSON(payload) == nil
}

func remoteTerminalHandleClientMessage(conn controlClientFrameConn, st *store, terminalID string, message ptyClientMessage) error {
	switch message.Type {
	case "input":
		return remoteTerminalRequest(conn, st, ControlRequest{
			RequestID:  "remote_pty_input_" + randomID(8),
			Capability: CapabilityTerminalInput,
			Action:     ControlActionTerminalInput,
			Params:     map[string]any{"terminal_id": terminalID, "data": message.Data},
		})
	case "resize":
		if message.Cols > 0 && message.Rows > 0 {
			return remoteTerminalRequest(conn, st, ControlRequest{
				RequestID:  "remote_pty_resize_" + randomID(8),
				Capability: CapabilityTerminalInput,
				Action:     ControlActionTerminalResize,
				Params:     map[string]any{"terminal_id": terminalID, "cols": message.Cols, "rows": message.Rows},
			})
		}
	case "close":
		return remoteTerminalClose(conn, st, terminalID)
	}
	return nil
}

func remoteTerminalHandleManagedClientMessage(terminal *remoteManagedTerminalStream, message ptyClientMessage) error {
	switch message.Type {
	case "input":
		return terminal.Input(message.Data)
	case "resize":
		return terminal.Resize(int(message.Cols), int(message.Rows))
	case "close":
		return terminal.Close()
	}
	return nil
}

func remoteTerminalRequest(conn controlClientFrameConn, st *store, req ControlRequest) error {
	req.ControllerDeviceID = st.deviceIdentity.DeviceID
	return conn.WritePlain(controlPlainFrame{Type: "request", Request: &req})
}

func remoteTerminalClose(conn controlClientFrameConn, st *store, terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return nil
	}
	return remoteTerminalRequest(conn, st, ControlRequest{
		RequestID:  "remote_pty_close_" + randomID(8),
		Capability: CapabilityTerminalInput,
		Action:     ControlActionTerminalClose,
		Params:     map[string]any{"terminal_id": terminalID},
	})
}

func controlResponseMessage(response ControlResponse) string {
	if response.Error == nil {
		return "remote control request failed"
	}
	if response.Error.Message != "" {
		return response.Error.Message
	}
	if response.Error.Code != "" {
		return response.Error.Code
	}
	return "remote control request failed"
}

func (a *app) remoteHostTarget(hostDeviceID string) (controlClientTarget, error) {
	if !a.cloudMeshActiveFor(cloudMembershipRole{CanControl: true}) {
		return controlClientTarget{}, cloudMeshInactiveError()
	}
	return a.remoteTargetResolver().ResolveKnownHost(hostDeviceID)
}

func (a *app) rememberRemoteHostLANRoute(hostInfo HostInfo, baseURL string, fallback KnownHost) KnownHost {
	known, err := a.store.rememberKnownHost(hostInfo, baseURL)
	if err != nil {
		return fallback
	}
	return known
}

func (a *app) remoteTargetResolver() remoteTargetResolver {
	return remoteTargetResolver{
		store:                     a.store,
		cloudClient:               a.cloudClientFromSettings,
		currentDeviceCloudRevoked: a.currentDeviceCloudRevoked,
		rememberLANRoute:          a.rememberRemoteHostLANRoute,
	}
}

func (a *app) writeRemoteControlResult(w http.ResponseWriter, hostDeviceID, capability, action string, params map[string]any) {
	response, err := a.remoteControlResponse(hostDeviceID, capability, action, params)
	if err != nil {
		writeRemoteHostError(w, err)
		return
	}
	writeControlHTTPResult(w, response, action)
}

func (a *app) writeRemoteWorkspaceFilesResult(w http.ResponseWriter, hostDeviceID string, params map[string]any) {
	response, err := a.remoteControlResponse(hostDeviceID, CapabilityWorkspaceFilesRead, ControlActionWorkspaceFilesRead, params)
	if err != nil {
		writeRemoteHostError(w, err)
		return
	}
	if !response.OK {
		writeControlHTTPResult(w, response, ControlActionWorkspaceFilesRead)
		return
	}
	result := mapValue(response.Result)
	writeJSON(w, http.StatusOK, map[string]any{
		"root":    "",
		"path":    stringValue(result["path"]),
		"entries": result["entries"],
	})
}

func (a *app) remoteControlResponse(hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error) {
	manager := a.remoteControlManager()
	if manager == nil {
		return ControlResponse{}, errors.New("remote control manager is not initialized")
	}
	response, err := manager.Request(context.Background(), hostDeviceID, capability, action, params)
	if err != nil {
		var actionErr *actionError
		if errors.As(err, &actionErr) && actionErr.Code == controlAuthorizationRequiredCode {
			return ControlResponse{}, actionErr
		}
		return ControlResponse{}, fmt.Errorf("remote control request failed: %w", err)
	}
	return response, nil
}

func (a *app) handleRemoteHostEventsSSE(w http.ResponseWriter, r *http.Request, hostDeviceID string) {
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	replayLimit := eventSubscriptionMaxReplayLimit
	if value := strings.TrimSpace(r.URL.Query().Get("replay_limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			replayLimit = parsed
		}
	}
	manager := a.remoteControlManager()
	if manager == nil {
		writeRemoteHostError(w, errors.New("remote control manager is not initialized"))
		return
	}
	stream, err := manager.SubscribeEvents(r.Context(), hostDeviceID, eventSubscriptionParams{
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		SessionID:   r.URL.Query().Get("session_id"),
		AfterSeq:    afterSeq,
		ReplayLimit: replayLimit,
	})
	if err != nil {
		writeRemoteHostError(w, err)
		return
	}
	defer stream.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
		case event, ok := <-stream.Events:
			if !ok {
				if r.Context().Err() == nil {
					writeSSE(w, flusher, "remote-error", map[string]string{"error": "remote event stream closed"})
				}
				return
			}
			writeSSE(w, flusher, "astral-event", event)
		}
	}
}

func writeControlHTTPResult(w http.ResponseWriter, response ControlResponse, action string) {
	if response.OK {
		status := http.StatusOK
		if action == ControlActionSessionCreate || action == ControlActionSessionFork || action == ControlActionWorkspaceCreate {
			status = http.StatusCreated
		}
		writeJSON(w, status, response.Result)
		return
	}
	status := http.StatusBadGateway
	message := "remote control request failed"
	code := "remote_control_failed"
	if response.Error != nil {
		if response.Error.Status > 0 {
			status = response.Error.Status
		}
		if response.Error.Message != "" {
			message = response.Error.Message
		}
		if response.Error.Code != "" {
			code = response.Error.Code
		}
	}
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func writeRemoteHostError(w http.ResponseWriter, err error) {
	var actionErr *actionError
	if errors.As(err, &actionErr) {
		writeJSON(w, actionErr.Status, map[string]string{"error": actionErr.Message, "code": actionErr.Code})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "code": "remote_host_unavailable"})
}

func remoteHostPathParts(path string) ([]string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/remote/hosts/"), "/")
	if rest == "" || rest == path {
		return nil, false
	}
	raw := strings.Split(rest, "/")
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if item == "" {
			continue
		}
		decoded, err := url.PathUnescape(item)
		if err != nil {
			return nil, false
		}
		parts = append(parts, decoded)
	}
	return parts, true
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
