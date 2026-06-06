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
	projected, ok := ProjectNormalizedForRemote(protocol.EventNormalized(kind, normalized))
	if !ok {
		return map[string]any{}
	}
	return protocol.NormalizedMap(projected)
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

var controlEventProjectionKeys = map[string]map[string]bool{
	"approval.requested": {
		"source": true, "approval_id": true, "request_id": true, "kind": true, "turn_id": true, "item_id": true,
		"command": true, "tool_name": true, "tool_input": true, "params": true, "reason": true, "changes": true,
		"diff": true, "decision": true, "available_decisions": true, "command_actions": true, "additional_permissions": true,
		"network_approval_context": true, "proposed_execpolicy_amendment": true, "proposed_network_amendments": true,
		"text": true, "plan": true, "question": true, "options": true,
	},
	"approval.resolved": {"source": true, "approval_id": true, "request_id": true, "response": true, "decision": true},
	"approval.responded": {
		"source": true, "approval_id": true, "request_id": true, "response": true, "decision": true,
	},
	"ask.requested": {
		"source": true, "ask_id": true, "request_id": true, "kind": true, "turn_id": true, "item_id": true,
		"question": true, "prompt": true, "text": true, "options": true, "fields": true, "multi_select": true,
		"multiSelect": true, "allow_custom": true, "allowCustom": true, "is_secret": true, "isSecret": true,
		"schema": true, "requested_schema": true, "requestedSchema": true, "url": true, "mode": true,
	},
	"ask.resolved": {"source": true, "ask_id": true, "request_id": true, "response": true, "answers": true},
	"control.context": {
		"source": true, "scope": true, "model": true, "usage": true, "model_usage": true, "total_tokens": true,
		"cumulative_total_tokens": true, "cumulative_input_tokens": true, "cumulative_output_tokens": true,
		"cumulative_cached_input_tokens": true, "cumulative_cache_creation_input_tokens": true, "input_tokens": true,
		"output_tokens": true, "cached_input_tokens": true, "cache_creation_input_tokens": true,
		"model_context_window": true, "used_percent": true,
	},
	"control.error":            {"source": true, "message": true, "code": true, "status": true, "details": true},
	"control.interrupt":        {"source": true, "status": true, "turn_id": true},
	"control.model":            {"source": true, "method": true, "params": true},
	"control.notification":     {"source": true, "visibility": true, "notification_id": true, "reason": true, "title": true, "body": true, "target": true, "source_event": true},
	"control.pairing.approved": {"request_id": true, "host_device_id": true, "controller_device_id": true, "status": true},
	"control.pairing.denied":   {"request_id": true, "host_device_id": true, "controller_device_id": true, "status": true},
	"control.pairing.requested": {
		"request_id": true, "host_device_id": true, "controller_device_id": true, "controller_device_name": true,
		"controller_device_kind": true, "controller_public_key_fingerprint": true, "scope": true, "capabilities": true,
		"status": true, "source": true, "cloud_request_id": true,
	},
	"control.rate_limit":        {"source": true, "limits": true},
	"control.raw":               {"source": true, "type": true, "subtype": true, "method": true},
	"control.status":            {"source": true, "kind": true, "status": true, "permission_mode": true, "active_flags": true, "name": true, "message": true},
	"control.steer":             {"source": true, "status": true, "turn_id": true},
	"control.terminal.attached": {"terminal_id": true, "viewer_id": true, "input_lease_id": true, "status": true},
	"control.terminal.closed":   {"terminal_id": true, "status": true},
	"control.terminal.detached": {"terminal_id": true, "viewer_id": true, "released": true, "status": true},
	"control.terminal.opened":   {"terminal_id": true, "status": true},
	"control.trust.granted":     {"host_device_id": true, "controller_device_id": true, "scope": true, "capabilities": true, "pairing_request_id": true},
	"control.trust.revoked":     {"host_device_id": true, "controller_device_id": true, "revoked_at": true, "released_terminal_writers": true},
	"control.warning":           {"source": true, "kind": true, "name": true, "status": true, "message": true, "attempt": true, "max_retries": true, "retry_delay_ms": true, "error_status": true, "error": true},
	"hook.completed":            {"source": true, "id": true, "name": true, "hook_event_name": true, "status": true, "stdout": true, "stderr": true, "output": true, "exit_code": true, "outcome": true},
	"hook.progress":             {"source": true, "id": true, "name": true, "hook_event_name": true, "status": true, "stdout": true, "stderr": true, "output": true, "exit_code": true, "outcome": true},
	"hook.started":              {"source": true, "id": true, "name": true, "hook_event_name": true, "status": true, "stdout": true, "stderr": true, "output": true, "exit_code": true, "outcome": true},
	"memory.compacted":          {"source": true, "turn_id": true, "metadata": true},
	"memory.compacting":         {"source": true, "status": true, "message": true},
	"message.assistant":         {"source": true, "text": true, "item_id": true, "message_id": true, "native_message_uuid": true, "media": true, "attachments": true, "source_session_id": true, "source_seq": true, "fork_projection": true, "suppress_notification": true},
	"message.delta":             {"source": true, "text": true, "item_id": true, "message_id": true},
	"message.media":             {"source": true, "status": true, "item_id": true, "media_id": true, "mime_type": true, "kind": true, "name": true, "detail": true, "width": true, "height": true, "size": true},
	"message.started":           {"source": true, "item_id": true, "message_id": true},
	"message.user":              {"source": true, "text": true, "input": true, "attachments": true, "media": true, "source_session_id": true, "source_seq": true, "fork_projection": true, "suppress_notification": true},
	"plan.delta":                {"source": true, "text": true, "item_id": true},
	"plan.updated":              {"source": true, "turn_id": true, "item_id": true, "plan": true, "text": true},
	"queue.cancelled":           {"queue_id": true, "reason": true, "text": true, "internal": true},
	"queue.dequeued":            {"queue_id": true, "text": true, "internal": true},
	"queue.failed":              {"queue_id": true, "reason": true, "message": true, "text": true, "internal": true},
	"queue.queued":              {"queue_id": true, "session_id": true, "text": true, "internal": true},
	"queue.steered":             {"queue_id": true, "text": true, "internal": true},
	"reasoning.completed":       {"source": true, "item_id": true, "text": true, "status": true},
	"reasoning.delta":           {"source": true, "item_id": true, "text": true},
	"reasoning.started":         {"source": true, "item_id": true, "text": true, "status": true},
	"session.deleted":           {"session_id": true, "reason": true, "message": true},
	"session.native":            {"source": true, "type": true, "subtype": true, "status": true, "preview": true, "name": true, "title": true, "summary": true, "firstPrompt": true, "aiTitle": true, "customTitle": true},
	"session.started":           {"id": true, "workspace_id": true, "agent": true, "title": true, "status": true, "forked_from_session_id": true, "forked_from_event_seq": true, "forked_from_title": true, "created_at": true, "updated_at": true},
	"session.updated":           {"source": true, "type": true, "subtype": true, "title": true, "summary": true, "description": true, "recent_action": true, "summarizes_uuid": true, "thread_name": true, "name": true},
	"tool.completed":            {"source": true, "id": true, "name": true, "item_id": true, "category": true, "status": true, "result": true, "text": true, "hidden": true, "visibility": true},
	"tool.diff":                 {"source": true, "turn_id": true, "item_id": true, "diff": true, "patch": true},
	"tool.output_delta":         {"source": true, "id": true, "name": true, "item_id": true, "category": true, "text": true},
	"tool.progress":             {"source": true, "id": true, "name": true, "parent_tool_use_id": true, "elapsed_time_seconds": true, "task_id": true, "category": true, "text": true, "status": true},
	"tool.started":              {"source": true, "id": true, "name": true, "item_id": true, "category": true, "input": true, "params": true, "command": true, "effective_command": true, "remote_command": true, "native_command": true, "text": true},
	"tool.todo":                 {"source": true, "todos": true, "text": true},
	"turn.cancelled":            {"source": true, "turn_id": true, "status": true, "reason": true},
	"turn.completed":            {"source": true, "turn_id": true, "status": true, "duration_ms": true},
	"turn.failed":               {"source": true, "turn_id": true, "status": true, "message": true, "reason": true},
	"turn.replaced":             {"source": true, "replacement_session_id": true, "source_session_id": true, "from_seq": true, "to_seq": true, "start_seq": true, "end_seq": true, "status": true},
	"turn.started":              {"source": true, "turn_id": true, "status": true},
	"workspace.connection":      {"workspace_id": true, "target": true, "status": true, "endpoint": true, "port": true, "remote_cwd": true, "remote_user": true, "remote_host": true, "remote_os": true, "remote_arch": true, "remote_shell": true, "display_cwd": true, "capabilities": true, "message": true, "retry_attempt": true, "retry_max": true, "updated_at": true},
	"workspace.created":         {"id": true, "name": true, "target": true, "agent": true, "created_at": true, "updated_at": true},
	"workspace.removed":         {"workspace_id": true, "id": true, "reason": true},
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
	if _, ok := controlEventProjectionKeys[string(event.Kind)]; !ok {
		event.Normalized = protocol.EventNormalized(protocol.AstralEventKindControlRaw, map[string]any{})
		return event
	}
	projected, ok := ProjectNormalizedForRemote(event.Normalized)
	if !ok {
		event.Normalized = protocol.EventNormalized(protocol.AstralEventKindControlRaw, map[string]any{})
		return event
	}
	event.Normalized = projected
	return event
}

func ProjectNormalizedForRemote(payload protocol.AstralEventNormalized) (protocol.AstralEventNormalized, bool) {
	switch payload.(type) {
	case *protocol.ApprovalRequestedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindApprovalRequested, payload)
	case *protocol.ApprovalResolvedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindApprovalResolved, payload)
	case *protocol.ApprovalRespondedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindApprovalResponded, payload)
	case *protocol.AskRequestedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindAskRequested, payload)
	case *protocol.AskResolvedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindAskResolved, payload)
	case *protocol.ControlContextNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlContext, payload)
	case *protocol.ControlErrorNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlError, payload)
	case *protocol.ControlInterruptNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlInterrupt, payload)
	case *protocol.ControlModelNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlModel, payload)
	case *protocol.ControlNotificationNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlNotification, payload)
	case *protocol.ControlPairingApprovedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlPairingApproved, payload)
	case *protocol.ControlPairingDeniedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlPairingDenied, payload)
	case *protocol.ControlPairingRequestedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlPairingRequested, payload)
	case *protocol.ControlRateLimitNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlRateLimit, payload)
	case *protocol.ControlRawNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlRaw, payload)
	case *protocol.ControlStatusNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlStatus, payload)
	case *protocol.ControlSteerNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlSteer, payload)
	case *protocol.ControlTerminalAttachedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTerminalAttached, payload)
	case *protocol.ControlTerminalClosedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTerminalClosed, payload)
	case *protocol.ControlTerminalDetachedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTerminalDetached, payload)
	case *protocol.ControlTerminalOpenedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTerminalOpened, payload)
	case *protocol.ControlTrustGrantedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTrustGranted, payload)
	case *protocol.ControlTrustRevokedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlTrustRevoked, payload)
	case *protocol.ControlWarningNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindControlWarning, payload)
	case *protocol.HookCompletedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindHookCompleted, payload)
	case *protocol.HookProgressNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindHookProgress, payload)
	case *protocol.HookStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindHookStarted, payload)
	case *protocol.MemoryCompactedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMemoryCompacted, payload)
	case *protocol.MemoryCompactingNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMemoryCompacting, payload)
	case *protocol.MessageAssistantNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMessageAssistant, payload)
	case *protocol.MessageDeltaNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMessageDelta, payload)
	case *protocol.MessageMediaNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMessageMedia, payload)
	case *protocol.MessageStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMessageStarted, payload)
	case *protocol.MessageUserNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindMessageUser, payload)
	case *protocol.PlanDeltaNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindPlanDelta, payload)
	case *protocol.PlanUpdatedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindPlanUpdated, payload)
	case *protocol.QueueCancelledNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindQueueCancelled, payload)
	case *protocol.QueueDequeuedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindQueueDequeued, payload)
	case *protocol.QueueFailedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindQueueFailed, payload)
	case *protocol.QueueQueuedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindQueueQueued, payload)
	case *protocol.QueueSteeredNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindQueueSteered, payload)
	case *protocol.ReasoningCompletedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindReasoningCompleted, payload)
	case *protocol.ReasoningDeltaNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindReasoningDelta, payload)
	case *protocol.ReasoningStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindReasoningStarted, payload)
	case *protocol.SessionDeletedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindSessionDeleted, payload)
	case *protocol.SessionNativeNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindSessionNative, payload)
	case *protocol.SessionStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindSessionStarted, payload)
	case *protocol.SessionUpdatedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindSessionUpdated, payload)
	case *protocol.ToolCompletedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolCompleted, payload)
	case *protocol.ToolDiffNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolDiff, payload)
	case *protocol.ToolOutputDeltaNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolOutputDelta, payload)
	case *protocol.ToolProgressNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolProgress, payload)
	case *protocol.ToolStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolStarted, payload)
	case *protocol.ToolTodoNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindToolTodo, payload)
	case *protocol.TurnCancelledNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindTurnCancelled, payload)
	case *protocol.TurnCompletedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindTurnCompleted, payload)
	case *protocol.TurnFailedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindTurnFailed, payload)
	case *protocol.TurnReplacedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindTurnReplaced, payload)
	case *protocol.TurnStartedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindTurnStarted, payload)
	case *protocol.WorkspaceConnectionNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindWorkspaceConnection, payload)
	case *protocol.WorkspaceCreatedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindWorkspaceCreated, payload)
	case *protocol.WorkspaceRemovedNormalized:
		return projectTypedNormalizedForRemote(protocol.AstralEventKindWorkspaceRemoved, payload)
	default:
		return nil, false
	}
}

func projectTypedNormalizedForRemote(kind protocol.AstralEventKind, payload protocol.AstralEventNormalized) (protocol.AstralEventNormalized, bool) {
	projected := sanitizeControlEventNormalized(string(kind), payload)
	return protocol.EventNormalized(kind, projected), true
}

func sanitizeControlEventNormalized(kind string, normalized any) any {
	value := protocol.NormalizedMap(normalized)
	if len(value) == 0 {
		if allowed, ok := controlEventProjectionKeys[kind]; ok && len(allowed) == 0 {
			return map[string]any{}
		}
		return map[string]any{}
	}
	projected := projectControlEventFields(kind, value)
	if strings.HasPrefix(kind, "message.") {
		if attachments, ok := projected["attachments"]; ok {
			projected["attachments"] = sanitizeControlEventMediaReferences(attachments)
		}
		if media, ok := projected["media"]; ok {
			projected["media"] = sanitizeControlEventMediaReferences(media)
		}
	}
	if kind == "message.media" {
		sanitizeControlEventMediaReference(projected)
	}
	return projected
}

func projectControlEventFields(kind string, value map[string]any) map[string]any {
	allowed, ok := controlEventProjectionKeys[kind]
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for key := range allowed {
		if v, ok := value[key]; ok {
			out[key] = v
		}
	}
	return out
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
	session.NativeRef = nil
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
