# AstralOps Agent Notes

## Event contract memory

Do not rely on chat context for the AstralOps event contract. Treat this file as the project-level source of truth.

AstralOps normalized event families:

```text
session.*
turn.*
message.*
reasoning.*
tool.*
approval.*
ask.*
plan.*
queue.*
workspace.*
memory.*
subagent.*
hook.*
control.*
```

UI states and event meanings that must be first-class:

```text
idle / running / requires_action / reconnecting / failed
assistant streaming
reasoning block
plan mode block
command output
file diff approval
Ask/user input form
MCP elicitation form/url
prompt queued / dequeued / cancelled
rate limit
compact boundary
projection hydrated / pushed
SSH degraded / reconnected
```

Codex raw event references observed from prior sessions:

```text
session_meta
turn_context
task_started
user_message
agent_message
token_count
turn_aborted
response_item.message
response_item.function_call
response_item.function_call_output
response_item.custom_tool_call
response_item.custom_tool_call_output
patch_apply_end
```

Implementation rule:

```text
Claude/Codex raw events must be preserved in AstralEvent.raw.
UI and AstralOps business logic must consume AstralEvent.kind + AstralEvent.normalized.
Do not invent ad hoc event families outside the normalized list above without updating this file and protocol docs/tests.
Do not implement speculative fallback mappings for Claude/Codex events.
When adding or changing event normalization/rendering, first capture real local Claude Code/Codex samples into fixtures and implement against those exact observed shapes.
If an event is not covered by a fixture, preserve it only as hidden control.raw for debug/replay. Do not create generic visible UI for it, and do not map it into a semantic event until a real fixture proves the shape.
Do not add "best guess" UI branches for event names that have not been observed locally.
```

Architecture and fallback rule:

```text
Do not add speculative fallback logic, broad catch-all mappings, or redundant defensive branches to make an uncertain case appear handled.
Every behavior mapping, permission response, state transition, and UI surface must be backed by a real Claude/Codex fixture, source-backed protocol shape, or an explicit rule in this file.
If a real issue points to an architectural mismatch, stop and identify the architectural fix or refactor boundary for user confirmation instead of layering another patch on top of the mismatch.
Prefer deleting or narrowing unsupported branches over preserving "just in case" behavior.
Temporary compatibility code is allowed only when tied to a specific observed version/shape and documented with the fixture or source evidence that requires it.
```

Real-task validation priority:

```text
Real Claude/Codex validation must prioritize user-visible task flow failures over isolated event rendering.
The highest-priority failures are repeated Ask/plan/permission loops, non-resumable confirmations presented as resumable, stale pending interactions after a turn has already failed or completed, tasks stuck in requires_action with no valid next action, and agents continuing to ask for the same missing permission after the user has accepted, declined, skipped, or cancelled.
Every local/SSH and default/full-permission test scenario must record whether the agent made forward progress, stopped correctly, or entered a loop/stuck state. A scenario is not passing just because the UI rendered the latest event.
If a real task exposes repeated questions, repeated plan confirmations, repeated permission prompts, or a mismatch between the displayed action and the actual agent continuation semantics, treat it as a blocker before expanding coverage to lower-risk event types.
```

UI implementation rule:

```text
Visible UI copy must not use emoji or decorative Unicode symbols. Use plain text and lucide icons for affordances/status. Keyboard hints must be plain labels such as Enter, Cmd+Enter, or ESC.
Permission, command, file-change, Ask, MCP elicitation, and plan confirmation surfaces must show the concrete decision target from AstralEvent.normalized, such as command, cwd, tool name, reason, file/change summary, prompt, or params. Do not show generic approval text when normalized data contains a more specific target.
```

## Current event coverage audit

Last audited: 2026-05-23.

Claude Code local currently uses `claude -p --output-format stream-json --verbose --include-partial-messages --include-hook-events`; it is not yet a full Claude SDK/control-protocol host. This means stdout stream-json is covered, but control requests/responses/cancel requests and most hook lifecycle events are not yet implemented.

Claude Code covered from real fixtures:

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
system compact_boundary -> memory.compacted
system status -> control.status
system api_retry -> control.warning
system local_command_output -> message.assistant
system hook_started/hook_progress/hook_response -> hook.started/hook.progress/hook.completed
tool_progress -> tool.progress
rate_limit_event -> control.rate_limit (hidden in normal UI)
```

Claude Code local interaction semantics:

```text
With the current `claude -p --output-format stream-json` runtime, AskUserQuestion and ExitPlanMode are not live resumable ServerRequests. Claude emits the tool_use, then the CLI emits an error tool_result because no interactive answer exists, and the turn can finish with permission_denials. AstralOps may show an Ask/plan/permission surface, but responding must be treated as a follow-up turn sent with --resume, not as unblocking the same Claude turn. Do not label Claude confirmations as if they continue the current turn unless a future real control-protocol fixture proves that behavior.
Real SSH/local samples show Claude may call AskUserQuestion repeatedly inside one stream-json turn after each non-interactive Ask denial. In that shape, earlier AskUserQuestion requests in the same turn are stale attempts; only the latest AskUserQuestion from that turn may remain actionable in the UI. Cancelling, skipping, or answering that latest Ask must not reveal older AskUserQuestion attempts from the same turn.
```

Claude Code not yet covered:

```text
Full hook input payload events: PreToolUse, PostToolUse, PostToolUseFailure, Notification, UserPromptSubmit, SessionStart, SessionEnd, Stop, StopFailure, SubagentStart, SubagentStop, PreCompact, PostCompact, PermissionRequest, PermissionDenied, Setup, TeammateIdle, TaskCreated, TaskCompleted, Elicitation, ElicitationResult, ConfigChange, WorktreeCreate, WorktreeRemove, InstructionsLoaded, CwdChanged, FileChanged. Current UI can render hook_started/progress/response lifecycle and hook_event_name, but AstralOps is not yet receiving every hook input payload as a first-class event.
SDK output events: auth_status, task_notification, task_started, task_progress, session_state_changed, files_persisted, tool_use_summary, elicitation_complete, prompt_suggestion.
Control protocol: interrupt, can_use_tool, initialize, set_permission_mode, set_model, set_max_thinking_tokens, mcp_status, get_context_usage, hook_callback, mcp_message, rewind_files, cancel_async_message, seed_read_state, mcp_set_servers, reload_plugins, mcp_reconnect, mcp_toggle, stop_task, apply_flag_settings, get_settings, elicitation.
```

Codex local currently uses `codex app-server --listen stdio://` and handles core ServerNotification/ServerRequest shapes from local fixtures.

Codex covered from real fixtures/source-backed shapes:

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
account/rateLimits/updated -> control.rate_limit
mcpServer/startupStatus/updated starting/ready -> control.status
mcpServer/startupStatus/updated failed -> control.warning
error/configWarning/deprecationNotice/model reroute -> control.*
ServerRequest: command approval, file approval, permissions approval, tool user input, MCP elicitation, dynamic tool call
```

Interaction UI semantics:

```text
Claude AskUserQuestion: render all observed questions. Questions with options are choice inputs; multiSelect allows multiple choices; free text is only shown when no options are present or a real question shape explicitly permits custom/other input. Submitting emits ask.resolved and starts a Claude follow-up turn with the answer payload. Skipping emits ask.resolved with an empty/declined answer payload and also follows up; it does not resume the same turn.
Claude ExitPlanMode: render the plan text and accept/decline choices. Decline may include user feedback text. Submitting emits approval.responded and starts a Claude follow-up turn. Do not present it as a live in-turn unblock until a future control-protocol fixture proves that behavior.
Codex command/file approvals: render concrete command/cwd or file paths/changes. Accept/decline responds to the original JSON-RPC ServerRequest and the same turn continues. No custom input should be shown unless a real request schema adds it.
Codex permissions approval: render cwd/reason/permissions. UI may offer one-turn and session-scoped acceptance. Decline is a JSON-RPC error response; same turn receives the rejection.
Codex item/tool/requestUserInput: render all questions, options, multiSelect, isOther/custom input, and isSecret password-style input. Submitting sends answers keyed by question id to the original ServerRequest so the same turn continues. Skipping sends an empty answers object.
Codex mcpServer/elicitation/request: render a dedicated MCP elicitation surface. Form mode collects JSON content matching requestedSchema; URL mode shows the URL and returns accept/decline/cancel. Respond to the original ServerRequest with action/content/_meta; do not treat it as a generic AskUserQuestion.
The secondary button label must name the actual response: refuse, cancel, skip, or do not accept. Do not label a response-sending button "ignore" when it actually resolves or rejects the agent request.
```

Codex not yet covered:

```text
thread/archived, thread/unarchived, thread/closed, skills/changed, thread/name/updated, thread/tokenUsage/updated, hook/started, hook/completed, item/autoApprovalReview/started, item/autoApprovalReview/completed, rawResponseItem/completed, item/commandExecution/terminalInteraction, item/mcpToolCall/progress, mcpServer/oauthLogin/completed, account/updated, app/list/updated, fs/changed, fuzzyFileSearch/sessionUpdated, fuzzyFileSearch/sessionCompleted, realtime events, Windows sandbox/login notifications.
ServerRequest account/chatgptAuthTokens/refresh is not handled.
ThreadItem UserMessage, HookPrompt, ImageView, ImageGeneration, EnteredReviewMode, ExitedReviewMode need real local fixtures before semantic UI mapping.
```
