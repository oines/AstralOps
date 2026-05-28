package main

import "strings"

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

func sanitizeControlSessionView(view sessionView) sessionView {
	view.Session = sanitizeControlSession(view.Session)
	return view
}
