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
	Host                 HostInfo              `json:"host"`
	Workspaces           []Workspace           `json:"workspaces"`
	Sessions             []Session             `json:"sessions"`
	WorkspaceConnections []WorkspaceConnection `json:"workspace_connections,omitempty"`
	Events               []AstralEvent         `json:"events"`
	SessionViews         []sessionView         `json:"session_views"`
	InitialSessionEvents []AstralEvent         `json:"initial_session_events,omitempty"`
	Workbench            workbenchState        `json:"workbench"`
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
	workspaces := a.store.listWorkspaces()
	sessions := a.store.listSessions("")
	workspaceByID := map[string]Workspace{}
	for _, workspace := range workspaces {
		workspaceByID[workspace.ID] = workspace
	}

	connections := make([]WorkspaceConnection, 0)
	for _, workspace := range workspaces {
		if workspace.Target != "ssh" {
			continue
		}
		connections = append(connections, sanitizeControlWorkspaceConnection(a.ssh.getConnection(workspace)))
	}

	views := make([]sessionView, 0, len(sessions))
	for _, session := range sessions {
		view, ok := a.buildSessionView(session.ID)
		if !ok {
			continue
		}
		views = append(views, sanitizeControlSessionView(view, workspaceByID[view.Session.WorkspaceID]))
	}

	result := hostSnapshotResult{
		Host:                 a.store.hostInfo(),
		Workspaces:           sanitizeControlWorkspaces(workspaces),
		Sessions:             sanitizeControlSessions(sessions),
		WorkspaceConnections: connections,
		Events:               sanitizeControlEvents(a.store.queryEventsWindow("", "", 0, 0, eventLimit)),
		SessionViews:         views,
		Workbench:            a.buildWorkbenchState(),
	}
	if params.RestoreOnLaunch && len(sessions) > 0 {
		result.InitialSessionEvents = sanitizeControlEvents(a.store.queryEventsWindow("", sessions[0].ID, 0, 0, eventLimit))
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
