# AstralOps Event Coverage Audit

Last audited: 2026-05-23.

This is a snapshot of covered and uncovered Claude Code and Codex event shapes. Do not update the audit date unless a fresh fixture audit has been run.

## Claude Code Runtime

Claude Code local currently uses:

```text
claude -p --output-format stream-json --verbose --include-partial-messages --include-hook-events
```

It is not yet a full Claude SDK/control-protocol host. This means stdout stream-json is covered, but control requests/responses/cancel requests and most hook lifecycle events are not yet implemented.

## Claude Code Covered From Real Fixtures

```text
system -> session.native
assistant text/partial text -> message.delta
assistant thinking/partial thinking -> reasoning.delta
assistant tool_use TodoWrite -> tool.todo
assistant tool_use AskUserQuestion -> ask.requested
assistant tool_use ExitPlanMode -> plan.updated
assistant tool_use Read/LS/Glob/Grep/WebSearch/Write/Edit/MultiEdit/Bash -> tool.started with category
user tool_result -> tool.completed
result.permission_denials ExitPlanMode -> approval.requested(kind=plan)
result.permission_denials AskUserQuestion -> ignored as duplicate ask denial from non-interactive Claude Code output
result.permission_denials WebSearch -> approval.requested(kind=permission)
result.usage/modelUsage -> control.context
system compact_boundary -> memory.compacted
system status -> control.status
system api_retry -> control.warning
system local_command_output -> message.assistant
system hook_started/hook_progress/hook_response -> hook.started/hook.progress/hook.completed
tool_progress -> tool.progress
rate_limit_event -> control.rate_limit (hidden in normal UI)
```

## Claude Code Not Yet Covered

```text
Full hook input payload events: PreToolUse, PostToolUse, PostToolUseFailure, Notification, UserPromptSubmit, SessionStart, SessionEnd, Stop, StopFailure, SubagentStart, SubagentStop, PreCompact, PostCompact, PermissionRequest, PermissionDenied, Setup, TeammateIdle, TaskCreated, TaskCompleted, Elicitation, ElicitationResult, ConfigChange, WorktreeCreate, WorktreeRemove, InstructionsLoaded, CwdChanged, FileChanged.
Current UI can render hook_started/progress/response lifecycle and hook_event_name, but AstralOps is not yet receiving every hook input payload as a first-class event.
SDK output events: auth_status, task_notification, task_started, task_progress, session_state_changed, files_persisted, tool_use_summary, elicitation_complete, prompt_suggestion.
Control protocol: interrupt, can_use_tool, initialize, set_permission_mode, set_model, set_max_thinking_tokens, mcp_status, get_context_usage, hook_callback, mcp_message, rewind_files, cancel_async_message, seed_read_state, mcp_set_servers, reload_plugins, mcp_reconnect, mcp_toggle, stop_task, apply_flag_settings, get_settings, elicitation.
```

## Codex Runtime

Codex local currently uses:

```text
codex app-server --listen stdio://
```

It handles core ServerNotification/ServerRequest shapes from local fixtures.

## Codex Covered From Real Fixtures And Source-Backed Shapes

```text
thread/started -> session.native
thread/status/changed -> control.status, preserving activeFlags such as waitingOnApproval
turn/started -> turn.started
turn/completed -> turn.completed / turn.failed
turn/plan/updated -> plan.updated
turn/diff/updated -> tool.diff
item/agentMessage/delta -> message.delta
item/reasoning/summaryTextDelta, item/reasoning/textDelta -> reasoning.delta
item/reasoning/summaryPartAdded -> reasoning.started
item/plan/delta -> plan.delta
item/started/completed: AgentMessage, Plan, Reasoning, CommandExecution, FileChange, McpToolCall, DynamicToolCall, CollabAgentToolCall, WebSearch, ContextCompaction, todo-like items
command/exec/outputDelta, item/commandExecution/outputDelta, item/fileChange/outputDelta -> tool.output_delta
serverRequest/resolved -> approval.resolved
thread/compacted -> memory.compacted
thread/tokenUsage/updated -> control.context
account/rateLimits/updated -> control.rate_limit
mcpServer/startupStatus/updated starting/ready -> control.status
mcpServer/startupStatus/updated failed -> control.warning
error/configWarning/deprecationNotice/model reroute -> control.*
ServerRequest: command approval, file approval, permissions approval, tool user input, MCP elicitation, dynamic tool call
item/started/completed ImageGeneration from real local fixture -> message.media
```

## Codex Not Yet Covered

```text
thread/archived, thread/unarchived, thread/closed, skills/changed, thread/name/updated, hook/started, hook/completed, item/autoApprovalReview/started, item/autoApprovalReview/completed, rawResponseItem/completed, item/commandExecution/terminalInteraction, item/mcpToolCall/progress, mcpServer/oauthLogin/completed, account/updated, app/list/updated, fs/changed, fuzzyFileSearch/sessionUpdated, fuzzyFileSearch/sessionCompleted, realtime events, Windows sandbox/login notifications.
ServerRequest account/chatgptAuthTokens/refresh is not handled.
ThreadItem UserMessage, HookPrompt, ImageView, EnteredReviewMode, ExitedReviewMode need real local fixtures before semantic UI mapping.
```
