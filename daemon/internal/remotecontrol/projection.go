package remotecontrol

import (
	"encoding/json"
	posixpath "path"
	"path/filepath"
	"strings"

	"github.com/oines/astralops/pkg/protocol"
)

type AstralEvent = protocol.AstralEvent
type Workspace = protocol.Workspace
type WorkspaceConnection = protocol.WorkspaceConnection
type Session = protocol.Session
type sessionView = protocol.SessionView
type pendingInteractionView = protocol.PendingInteractionView
type interactionDetailRow = protocol.InteractionDetailRow

func SanitizeEvents(events []protocol.AstralEvent) []protocol.AstralEvent {
	return sanitizeControlEvents(events)
}

func SanitizeEvent(event protocol.AstralEvent) protocol.AstralEvent {
	return sanitizeControlEvent(event)
}

func SanitizeEventNormalized(kind string, normalized any) any {
	return sanitizeControlEventNormalized(kind, normalized)
}

func SanitizeWorkspaces(workspaces []protocol.Workspace) []protocol.Workspace {
	return sanitizeControlWorkspaces(workspaces)
}

func SanitizeWorkspace(workspace protocol.Workspace) protocol.Workspace {
	return sanitizeControlWorkspace(workspace)
}

func SanitizeWorkspaceConnection(connection protocol.WorkspaceConnection) protocol.WorkspaceConnection {
	return sanitizeControlWorkspaceConnection(connection)
}

func SanitizeSessions(sessions []protocol.Session) []protocol.Session {
	return sanitizeControlSessions(sessions)
}

func SanitizeSession(session protocol.Session) protocol.Session {
	return sanitizeControlSession(session)
}

func SanitizeSessionView(view protocol.SessionView, workspace protocol.Workspace) protocol.SessionView {
	return sanitizeControlSessionView(view, workspace)
}

func SanitizePendingInteraction(pending *protocol.PendingInteractionView, workspace protocol.Workspace) *protocol.PendingInteractionView {
	return sanitizeControlPendingInteraction(pending, workspace)
}

func SanitizeDecisionPath(value string, workspace protocol.Workspace) string {
	return sanitizeControlDecisionPath(value, workspace)
}

var controlPrivatePathKeys = map[string]bool{
	"path":       true,
	"saved_path": true,
	"savedPath":  true,
	"local_path": true,
	"localPath":  true,
	"file_path":  true,
	"filePath":   true,
}

var controlPrivateRuntimeKeys = map[string]bool{
	"native_session_id":         true,
	"nativeSessionID":           true,
	"native_thread_id":          true,
	"nativeThreadID":            true,
	"forked_from_native_anchor": true,
	"forkedFromNativeAnchor":    true,
}

var controlPrivateWorkspaceKeys = map[string]bool{
	"local_cwd":             true,
	"localCWD":              true,
	"local_projection_root": true,
	"localProjectionRoot":   true,
	"ssh":                   true,
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
	sanitizeControlEventRuntimeInternals(value)
	if strings.HasPrefix(kind, "workspace.") {
		sanitizeControlEventWorkspaceInternals(value)
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

func sanitizeControlEventRuntimeInternals(value map[string]any) {
	for key := range controlPrivateRuntimeKeys {
		delete(value, key)
	}
}

func sanitizeControlEventWorkspaceInternals(value map[string]any) {
	for key := range controlPrivateWorkspaceKeys {
		delete(value, key)
	}
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

func sanitizeControlWorkspaceConnection(connection WorkspaceConnection) WorkspaceConnection {
	connection.HelperPath = ""
	connection.Raw = nil
	return connection
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
		if row.Key == "cwd" || row.Key == "path" {
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

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if json.Unmarshal(body, &out) != nil {
		return value
	}
	return out
}

func remotePathClean(value string) string {
	clean := posixpath.Clean(strings.TrimSpace(value))
	if clean == "." {
		return ""
	}
	return clean
}

func remotePathIsAbs(value string) bool {
	return posixpath.IsAbs(strings.TrimSpace(value))
}

func remotePathRel(root, target string) (string, error) {
	root = remotePathClean(root)
	target = remotePathClean(target)
	if root == "" {
		root = "/"
	}
	if target == "" {
		target = "/"
	}
	if root == target {
		return ".", nil
	}
	rootParts := splitRemotePath(root)
	targetParts := splitRemotePath(target)
	i := 0
	for i < len(rootParts) && i < len(targetParts) && rootParts[i] == targetParts[i] {
		i++
	}
	rel := make([]string, 0, len(rootParts)-i+len(targetParts)-i)
	for j := i; j < len(rootParts); j++ {
		rel = append(rel, "..")
	}
	rel = append(rel, targetParts[i:]...)
	if len(rel) == 0 {
		return ".", nil
	}
	return strings.Join(rel, "/"), nil
}

func splitRemotePath(value string) []string {
	value = strings.Trim(remotePathClean(value), "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`)
}

func localPathIsSameOrDescendant(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
