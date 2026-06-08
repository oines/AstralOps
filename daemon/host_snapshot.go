package main

import (
	"net/http"
	"strconv"
)

const (
	hostSnapshotDefaultEventLimit = 1000
	hostSnapshotMaxEventLimit     = 5000
)

type hostSnapshotParams struct {
	EventLimit      int  `json:"event_limit"`
	RestoreOnLaunch bool `json:"restore_on_launch"`
}

type hostSnapshotResult struct {
	Host                 HostInfo                `json:"host"`
	Agents               map[AgentKind]agentInfo `json:"agents,omitempty"`
	Workspaces           []Workspace             `json:"workspaces"`
	Sessions             []Session               `json:"sessions"`
	WorkspaceConnections []WorkspaceConnection   `json:"workspace_connections,omitempty"`
	Events               []AstralEvent           `json:"events"`
	SessionViews         []sessionView           `json:"session_views"`
	InitialSessionEvents []AstralEvent           `json:"initial_session_events,omitempty"`
	Workbench            workbenchState          `json:"workbench"`
}

func (a *app) handleHostSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	eventLimit, _ := strconv.Atoi(r.URL.Query().Get("event_limit"))
	writeJSON(w, http.StatusOK, a.buildHostSnapshot(hostSnapshotParams{
		EventLimit:      eventLimit,
		RestoreOnLaunch: truthyQuery(r.URL.Query().Get("restore_on_launch")),
	}))
}

func (a *app) buildHostSnapshot(params hostSnapshotParams) hostSnapshotResult {
	eventLimit := normalizedHostSnapshotEventLimit(params.EventLimit)
	workbench := a.buildWorkbenchState()
	workspaces, sessions, _, connections := flattenRemoteWorkbenchState(workbench)

	result := hostSnapshotResult{
		Host:                 a.store.hostInfo(),
		Agents:               sanitizeControlAgents(a.agents),
		Workspaces:           workspaces,
		Sessions:             sessions,
		WorkspaceConnections: connections,
		Events:               []AstralEvent{},
		SessionViews:         []sessionView{},
		Workbench:            workbench,
	}
	if params.RestoreOnLaunch && len(sessions) > 0 {
		result.InitialSessionEvents = sanitizeControlEvents(a.sessionProjections().QueryEventsWindow("", sessions[0].ID, 0, 0, eventLimit))
	}
	return result
}

func normalizedHostSnapshotEventLimit(limit int) int {
	if limit <= 0 {
		return hostSnapshotDefaultEventLimit
	}
	if limit > hostSnapshotMaxEventLimit {
		return hostSnapshotMaxEventLimit
	}
	return limit
}
