package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type workbenchState struct {
	Version              int64                          `json:"version"`
	UpdatedAt            string                         `json:"updated_at"`
	Workspaces           map[string]Workspace           `json:"workspaces"`
	Sessions             map[string]Session             `json:"sessions"`
	SessionViews         map[string]sessionView         `json:"session_views"`
	WorkspaceConnections map[string]WorkspaceConnection `json:"workspace_connections"`
	TerminalTabs         map[string]terminalTab         `json:"terminal_tabs"`
	Panels               map[string]workbenchPanel      `json:"panels"`
}

type workbenchPanel struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	State     map[string]any `json:"state,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
}

type workbenchPatch struct {
	Version int64              `json:"version"`
	Ops     []workbenchPatchOp `json:"ops"`
}

type workbenchPatchOp struct {
	Op         string `json:"op"`
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Value      any    `json:"value,omitempty"`
}

func (a *app) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if truthyQuery(r.URL.Query().Get("stream")) {
		a.handleWorkbenchSSE(w, r)
		return
	}
	writeJSON(w, http.StatusOK, a.buildWorkbenchState())
}

func (a *app) buildWorkbenchState() workbenchState {
	workspaces := sanitizeControlWorkspaces(a.store.listWorkspaces())
	sessions := sanitizeControlSessions(a.store.listSessions(""))
	workspaceByID := map[string]Workspace{}
	for _, workspace := range workspaces {
		workspaceByID[workspace.ID] = workspace
	}

	state := workbenchState{
		Version:              a.workbenchVersion(),
		UpdatedAt:            time.Now().UTC().Format(time.RFC3339Nano),
		Workspaces:           map[string]Workspace{},
		Sessions:             map[string]Session{},
		SessionViews:         map[string]sessionView{},
		WorkspaceConnections: map[string]WorkspaceConnection{},
		TerminalTabs:         map[string]terminalTab{},
		Panels:               map[string]workbenchPanel{},
	}
	for _, workspace := range workspaces {
		state.Workspaces[workspace.ID] = workspace
		if workspace.Target == "ssh" {
			state.WorkspaceConnections[workspace.ID] = sanitizeControlWorkspaceConnection(a.ssh.getConnection(workspace))
		}
	}
	for _, session := range sessions {
		state.Sessions[session.ID] = session
		if view, ok := a.buildSessionView(session.ID); ok {
			state.SessionViews[session.ID] = sanitizeControlSessionView(view, workspaceByID[view.Session.WorkspaceID])
		}
	}
	a.terminalMu.Lock()
	terminals := a.terminals
	a.terminalMu.Unlock()
	if terminals != nil {
		for _, tab := range terminals.listTabs() {
			state.TerminalTabs[tab.TerminalID] = tab
		}
	}
	return state
}

func (a *app) workbenchVersion() int64 {
	events := a.store.allEvents()
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func (a *app) handleWorkbenchSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	current := workbenchState{}
	next := a.buildWorkbenchState()
	writeSSE(w, flusher, "workbench.patch", diffWorkbenchState(current, next))
	current = next

	client := a.hub.addSSE()
	defer a.hub.removeSSE(client)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano)})
		case _, ok := <-client.ch:
			if !ok {
				return
			}
			next := a.buildWorkbenchState()
			patch := diffWorkbenchState(current, next)
			if len(patch.Ops) > 0 {
				writeSSE(w, flusher, "workbench.patch", patch)
			}
			current = next
		}
	}
}

func diffWorkbenchState(prev, next workbenchState) workbenchPatch {
	patch := workbenchPatch{Version: next.Version}
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("workspaces", workspaceAnyMap(prev.Workspaces), workspaceAnyMap(next.Workspaces))...)
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("sessions", sessionAnyMap(prev.Sessions), sessionAnyMap(next.Sessions))...)
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("session_views", sessionViewAnyMap(prev.SessionViews), sessionViewAnyMap(next.SessionViews))...)
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("workspace_connections", workspaceConnectionAnyMap(prev.WorkspaceConnections), workspaceConnectionAnyMap(next.WorkspaceConnections))...)
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("terminal_tabs", terminalTabAnyMap(prev.TerminalTabs), terminalTabAnyMap(next.TerminalTabs))...)
	patch.Ops = append(patch.Ops, diffWorkbenchCollection("panels", panelAnyMap(prev.Panels), panelAnyMap(next.Panels))...)
	return patch
}

func diffWorkbenchCollection(collection string, prev, next map[string]any) []workbenchPatchOp {
	ops := []workbenchPatchOp{}
	for id, value := range next {
		if !jsonEqual(prev[id], value) {
			ops = append(ops, workbenchPatchOp{Op: "upsert", Collection: collection, ID: id, Value: value})
		}
	}
	for id := range prev {
		if _, ok := next[id]; !ok {
			ops = append(ops, workbenchPatchOp{Op: "remove", Collection: collection, ID: id})
		}
	}
	return ops
}

func jsonEqual(left, right any) bool {
	leftBody, _ := json.Marshal(left)
	rightBody, _ := json.Marshal(right)
	return string(leftBody) == string(rightBody)
}

func workspaceAnyMap(values map[string]Workspace) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sessionAnyMap(values map[string]Session) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sessionViewAnyMap(values map[string]sessionView) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func workspaceConnectionAnyMap(values map[string]WorkspaceConnection) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func terminalTabAnyMap(values map[string]terminalTab) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func panelAnyMap(values map[string]workbenchPanel) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	return out
}
