package main

import (
	remotecontrol "github.com/oines/astralops/daemon/internal/remotecontrol"
)

func sanitizeControlEvents(events []AstralEvent) []AstralEvent {
	return remotecontrol.SanitizeEvents(events)
}

func sanitizeControlEvent(event AstralEvent) AstralEvent {
	return remotecontrol.SanitizeEvent(event)
}

func sanitizeControlEventNormalized(kind string, normalized any) any {
	return remotecontrol.SanitizeEventNormalized(kind, normalized)
}

func sanitizeControlWorkspaces(workspaces []Workspace) []Workspace {
	return remotecontrol.SanitizeWorkspaces(workspaces)
}

func sanitizeControlWorkspace(workspace Workspace) Workspace {
	return remotecontrol.SanitizeWorkspace(workspace)
}

func sanitizeControlWorkspaceConnection(connection WorkspaceConnection) WorkspaceConnection {
	return remotecontrol.SanitizeWorkspaceConnection(connection)
}

func sanitizeControlSessions(sessions []Session) []Session {
	return remotecontrol.SanitizeSessions(sessions)
}

func sanitizeControlSession(session Session) Session {
	return remotecontrol.SanitizeSession(session)
}

func sanitizeControlSessionView(view sessionView, workspace Workspace) sessionView {
	return remotecontrol.SanitizeSessionView(view, workspace)
}

func sanitizeControlPendingInteraction(pending *pendingInteractionView, workspace Workspace) *pendingInteractionView {
	return remotecontrol.SanitizePendingInteraction(pending, workspace)
}

func sanitizeControlDecisionPath(value string, workspace Workspace) string {
	return remotecontrol.SanitizeDecisionPath(value, workspace)
}
