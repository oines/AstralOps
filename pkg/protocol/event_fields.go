package protocol

type AstralEventNormalizedFieldSpec struct {
	Name string
}

func AstralEventNormalizedFieldSpecs() map[AstralEventKind][]AstralEventNormalizedFieldSpec {
	return map[AstralEventKind][]AstralEventNormalizedFieldSpec{
		AstralEventKindApprovalRequested:       normalizedFields("source", "approval_id", "request_id", "kind", "turn_id", "item_id", "command", "tool_name", "tool_input", "params", "reason", "changes", "diff", "decision", "available_decisions", "command_actions", "additional_permissions", "network_approval_context", "proposed_execpolicy_amendment", "proposed_network_amendments", "text", "plan", "question", "options", "path", "cwd"),
		AstralEventKindApprovalResolved:        normalizedFields("source", "approval_id", "request_id", "response", "decision"),
		AstralEventKindApprovalResponded:       normalizedFields("source", "approval_id", "request_id", "response", "decision"),
		AstralEventKindAskRequested:            normalizedFields("source", "ask_id", "request_id", "kind", "turn_id", "item_id", "question", "prompt", "text", "options", "fields", "multi_select", "multiSelect", "allow_custom", "allowCustom", "is_secret", "isSecret", "schema", "requested_schema", "requestedSchema", "url", "mode", "params"),
		AstralEventKindAskResolved:             normalizedFields("source", "ask_id", "request_id", "response", "answers"),
		AstralEventKindControlContext:          normalizedFields("source", "scope", "model", "usage", "model_usage", "token_usage", "total_tokens", "cumulative_total_tokens", "cumulative_input_tokens", "cumulative_output_tokens", "cumulative_cached_input_tokens", "cumulative_cache_creation_input_tokens", "input_tokens", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "reasoning_tokens", "model_context_window", "used_percent", "native_session_id", "native_thread_id"),
		AstralEventKindControlError:            normalizedFields("source", "message", "code", "status", "details", "reason"),
		AstralEventKindControlInterrupt:        normalizedFields("source", "status", "turn_id", "request_id"),
		AstralEventKindControlModel:            normalizedFields("source", "method", "params"),
		AstralEventKindControlNotification:     normalizedFields("source", "visibility", "notification_id", "reason", "title", "body", "target", "source_event"),
		AstralEventKindControlPairingApproved:  normalizedFields("request_id", "host_device_id", "controller_device_id", "status"),
		AstralEventKindControlPairingDenied:    normalizedFields("request_id", "host_device_id", "controller_device_id", "status"),
		AstralEventKindControlPairingRequested: normalizedFields("request_id", "host_device_id", "controller_device_id", "controller_device_name", "controller_device_kind", "controller_public_key_fingerprint", "scope", "capabilities", "status", "source", "cloud_request_id"),
		AstralEventKindControlRateLimit:        normalizedFields("source", "limits"),
		AstralEventKindControlRaw:              normalizedFields("source", "type", "subtype", "method", "original_kind", "error", "normalized", "raw", "value"),
		AstralEventKindControlStatus:           normalizedFields("source", "kind", "status", "permission_mode", "active_flags", "name", "message"),
		AstralEventKindControlSteer:            normalizedFields("source", "status", "turn_id"),
		AstralEventKindControlTerminalAttached: normalizedFields("terminal_id", "viewer_id", "input_lease_id", "status"),
		AstralEventKindControlTerminalClosed:   normalizedFields("terminal_id", "status", "reason"),
		AstralEventKindControlTerminalDetached: normalizedFields("terminal_id", "viewer_id", "released", "status", "reason"),
		AstralEventKindControlTerminalOpened:   normalizedFields("terminal_id", "status", "shell", "cwd"),
		AstralEventKindControlTrustGranted:     normalizedFields("host_device_id", "controller_device_id", "scope", "capabilities", "pairing_request_id"),
		AstralEventKindControlTrustRevoked:     normalizedFields("host_device_id", "controller_device_id", "revoked_at", "released_terminal_writers"),
		AstralEventKindControlWarning:          normalizedFields("source", "kind", "name", "status", "message", "attempt", "max_retries", "retry_delay_ms", "error_status", "error"),
		AstralEventKindHookCompleted:           normalizedFields("source", "id", "name", "hook_event_name", "status", "stdout", "stderr", "output", "exit_code", "outcome"),
		AstralEventKindHookProgress:            normalizedFields("source", "id", "name", "hook_event_name", "status", "stdout", "stderr", "output", "exit_code", "outcome"),
		AstralEventKindHookStarted:             normalizedFields("source", "id", "name", "hook_event_name", "status", "stdout", "stderr", "output", "exit_code", "outcome"),
		AstralEventKindMemoryCompacted:         normalizedFields("source", "turn_id", "metadata", "item_id"),
		AstralEventKindMemoryCompacting:        normalizedFields("source", "status", "message"),
		AstralEventKindMessageAssistant:        normalizedFields("source", "text", "item_id", "message_id", "native_message_uuid", "media", "attachments", "source_session_id", "source_seq", "fork_projection", "suppress_notification"),
		AstralEventKindMessageDelta:            normalizedFields("source", "text", "item_id", "message_id"),
		AstralEventKindMessageMedia:            normalizedFields("source", "status", "item_id", "media_id", "mime_type", "kind", "name", "detail", "width", "height", "size", "path", "saved_path", "revised_prompt"),
		AstralEventKindMessageStarted:          normalizedFields("source", "item_id", "message_id"),
		AstralEventKindMessageUser:             normalizedFields("source", "text", "input", "attachments", "media", "source_session_id", "source_seq", "fork_projection", "suppress_notification"),
		AstralEventKindPlanDelta:               normalizedFields("source", "text", "item_id"),
		AstralEventKindPlanUpdated:             normalizedFields("source", "turn_id", "item_id", "plan", "text"),
		AstralEventKindQueueCancelled:          normalizedFields("queue_id", "reason", "text", "internal"),
		AstralEventKindQueueDequeued:           normalizedFields("queue_id", "text", "internal"),
		AstralEventKindQueueFailed:             normalizedFields("queue_id", "reason", "message", "text", "internal"),
		AstralEventKindQueueQueued:             normalizedFields("queue_id", "session_id", "text", "internal"),
		AstralEventKindQueueSteered:            normalizedFields("queue_id", "text", "internal"),
		AstralEventKindReasoningCompleted:      normalizedFields("source", "item_id", "text", "status", "summary"),
		AstralEventKindReasoningDelta:          normalizedFields("source", "item_id", "text"),
		AstralEventKindReasoningStarted:        normalizedFields("source", "item_id", "text", "status", "summary"),
		AstralEventKindSessionDeleted:          normalizedFields("session_id", "reason", "message"),
		AstralEventKindSessionNative:           normalizedFields("source", "type", "subtype", "status", "preview", "name", "title", "summary", "firstPrompt", "aiTitle", "customTitle", "native_session_id", "native_thread_id"),
		AstralEventKindSessionStarted:          normalizedFields("id", "workspace_id", "agent", "title", "status", "source", "native_ref", "managed_by_astralops", "native_session_id", "native_thread_id", "forked_from_session_id", "forked_from_event_seq", "forked_from_title", "created_at", "updated_at"),
		AstralEventKindSessionUpdated:          normalizedFields("source", "type", "subtype", "title", "summary", "description", "recent_action", "summarizes_uuid", "thread_name", "name"),
		AstralEventKindToolCompleted:           normalizedFields("source", "id", "name", "item_id", "category", "status", "result", "text", "content", "is_error", "command", "cwd", "path", "file_path", "hidden", "visibility"),
		AstralEventKindToolDiff:                normalizedFields("source", "turn_id", "item_id", "diff", "patch"),
		AstralEventKindToolOutputDelta:         normalizedFields("source", "id", "name", "item_id", "category", "text"),
		AstralEventKindToolProgress:            normalizedFields("source", "id", "name", "parent_tool_use_id", "elapsed_time_seconds", "task_id", "category", "text", "status"),
		AstralEventKindToolStarted:             normalizedFields("source", "id", "name", "item_id", "category", "input", "params", "command", "cwd", "effective_command", "remote_command", "native_command", "text", "status"),
		AstralEventKindToolTodo:                normalizedFields("source", "todos", "text"),
		AstralEventKindTurnCancelled:           normalizedFields("source", "turn_id", "status", "reason"),
		AstralEventKindTurnCompleted:           normalizedFields("source", "turn_id", "status", "duration_ms"),
		AstralEventKindTurnFailed:              normalizedFields("source", "turn_id", "status", "message", "reason"),
		AstralEventKindTurnReplaced:            normalizedFields("source", "replacement_session_id", "source_session_id", "from_seq", "to_seq", "start_seq", "end_seq", "status"),
		AstralEventKindTurnStarted:             normalizedFields("source", "turn_id", "status"),
		AstralEventKindWorkspaceConnection:     normalizedFields("workspace_id", "target", "status", "endpoint", "port", "remote_cwd", "remote_user", "remote_host", "remote_os", "remote_arch", "remote_shell", "display_cwd", "helper_path", "helper_status", "capabilities", "message", "retry_attempt", "retry_max", "updated_at"),
		AstralEventKindWorkspaceCreated:        normalizedFields("id", "name", "target", "agent", "local_projection_root", "local_cwd", "ssh", "created_at", "updated_at"),
		AstralEventKindWorkspaceRemoved:        normalizedFields("workspace_id", "id", "reason"),
	}
}

func normalizedFields(names ...string) []AstralEventNormalizedFieldSpec {
	out := make([]AstralEventNormalizedFieldSpec, 0, len(names))
	for _, name := range names {
		out = append(out, AstralEventNormalizedFieldSpec{Name: name})
	}
	return out
}
