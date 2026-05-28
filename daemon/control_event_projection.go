package main

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
	switch kind {
	case "message.user":
		if attachments, ok := value["attachments"]; ok {
			value["attachments"] = sanitizeControlEventAttachments(attachments)
		}
	case "message.media":
		sanitizeControlEventMediaReference(value)
	}
	return value
}

func sanitizeControlEventAttachments(value any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		attachment, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		sanitizeControlEventMediaReference(attachment)
		out = append(out, attachment)
	}
	return out
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
