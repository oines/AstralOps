package main

import (
	"path/filepath"
	"strings"
)

var controlPrivatePathKeys = map[string]bool{
	"path":       true,
	"saved_path": true,
	"savedPath":  true,
	"local_path": true,
	"localPath":  true,
	"file_path":  true,
	"filePath":   true,
}

func sanitizeControlEvents(events []AstralEvent) []AstralEvent {
	out := make([]AstralEvent, len(events))
	for index, event := range events {
		out[index] = sanitizeControlEvent(event)
	}
	return out
}

func sanitizeControlEvent(event AstralEvent) AstralEvent {
	event.Raw = nil
	event.Normalized = sanitizeControlEventNormalized(event.Kind, event.Normalized)
	return event
}

func sanitizeControlEventNormalized(kind string, normalized any) any {
	cloned := cloneJSONValue(normalized)
	value, ok := cloned.(map[string]any)
	if !ok {
		return cloned
	}
	if strings.HasPrefix(kind, "message.") {
		if attachments, ok := value["attachments"]; ok {
			value["attachments"] = sanitizeControlEventMediaReferences(attachments)
		}
		if media, ok := value["media"]; ok {
			value["media"] = sanitizeControlEventMediaReferences(media)
		}
	}
	if kind == "message.media" {
		sanitizeControlEventMediaReference(value)
	}
	return value
}

func sanitizeControlEventMediaReferences(value any) any {
	switch items := value.(type) {
	case []any:
		out := make([]any, 0, len(items))
		for _, item := range items {
			media, ok := item.(map[string]any)
			if !ok {
				out = append(out, item)
				continue
			}
			sanitizeControlEventMediaReference(media)
			out = append(out, media)
		}
		return out
	case map[string]any:
		sanitizeControlEventMediaReference(items)
		return value
	default:
		return value
	}
}

func sanitizeControlEventMediaReference(value map[string]any) {
	for key := range controlPrivatePathKeys {
		delete(value, key)
	}
}

func sanitizeControlWorkspaces(workspaces []Workspace) []Workspace {
	out := make([]Workspace, len(workspaces))
	for index, workspace := range workspaces {
		out[index] = sanitizeControlWorkspace(workspace)
	}
	return out
}

func sanitizeControlWorkspace(workspace Workspace) Workspace {
	workspace.LocalProjectionRoot = ""
	workspace.LocalCWD = ""
	workspace.SSH = nil
	workspace.NativeSessionID = ""
	workspace.NativeThreadID = ""
	return workspace
}

func sanitizeControlSessions(sessions []Session) []Session {
	out := make([]Session, len(sessions))
	for index, session := range sessions {
		out[index] = sanitizeControlSession(session)
	}
	return out
}

func sanitizeControlSession(session Session) Session {
	session.NativeSessionID = ""
	session.NativeThreadID = ""
	session.ForkedFromNativeAnchor = ""
	return session
}

func sanitizeControlSessionView(view sessionView, workspace Workspace) sessionView {
	view.Session = sanitizeControlSession(view.Session)
	view.PendingInteraction = sanitizeControlPendingInteraction(view.PendingInteraction, workspace)
	return view
}

func sanitizeControlPendingInteraction(pending *pendingInteractionView, workspace Workspace) *pendingInteractionView {
	if pending == nil || len(pending.DetailRows) == 0 {
		return pending
	}
	out := *pending
	out.DetailRows = make([]interactionDetailRow, len(pending.DetailRows))
	for index, row := range pending.DetailRows {
		if row.Label == "目录" || row.Label == "路径" {
			row.Value = sanitizeControlDecisionPath(row.Value, workspace)
		}
		out.DetailRows[index] = row
	}
	return &out
}

func sanitizeControlDecisionPath(value string, workspace Workspace) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch workspace.Target {
	case "local":
		return sanitizeLocalControlDecisionPath(value, workspace.LocalCWD)
	case "ssh":
		if workspace.SSH != nil {
			return sanitizeRemoteControlDecisionPath(value, workspace.SSH.RemoteCWD)
		}
	}
	return value
}

func sanitizeLocalControlDecisionPath(value, root string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return value
	}
	if !filepath.IsAbs(value) {
		return value
	}
	target := filepath.Clean(value)
	if !localPathIsSameOrDescendant(root, target) {
		return value
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return value
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func sanitizeRemoteControlDecisionPath(value, root string) string {
	root = remotePathClean(root)
	value = remotePathClean(value)
	if root == "" || value == "" || !remotePathIsAbs(value) {
		return value
	}
	rel, err := remotePathRel(root, value)
	if err != nil || pathEscapesRoot(rel) {
		return value
	}
	if rel == "." {
		return "."
	}
	return rel
}
