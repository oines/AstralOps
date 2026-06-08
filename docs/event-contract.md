# AstralOps Event Contract

Do not rely on chat context for the AstralOps event contract. Treat this file as the project-level source of truth for normalized event families, UI states, raw event references, and normalization rules.

## Normalized Event Families

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

## First-Class UI States And Event Meanings

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
last user message edit / turn replacement
transcript media / attachments
rate limit
compact boundary
projection hydrated / pushed
SSH degraded / reconnected
```

## Observed Codex Raw Event References

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

## Normalization Rules

```text
Claude/Codex native history is the transcript fact source. AstralEvent is a runtime/wire projection DTO, not a persistent transcript journal.
AstralOps stores control state and transcript overlays separately from agent transcript history. It must not append agent message/tool/reasoning/plan/turn transcript events to events/*.jsonl as a second fact source.
For SSH workspaces, native Claude/Codex history is still Host-local. Do not read transcript history from the SSH remote host or proxy-agent.
Claude/Codex raw events may be kept in AstralEvent.raw for local debug/replay while a runtime event is in memory. Raw is not a persistent AstralOps fact source and must not be projected to Controllers.
UI and AstralOps business logic must consume AstralEvent.kind + AstralEvent.normalized.
Do not invent ad hoc event families outside the normalized list above without updating this file and protocol docs/tests.
Do not implement speculative fallback mappings for Claude/Codex events.
When adding or changing event normalization/rendering, first capture real local Claude Code/Codex samples into fixtures and implement against those exact observed shapes.
If an event is not covered by a fixture, preserve it only as hidden control.raw for debug/replay. Do not create generic visible UI for it, and do not map it into a semantic event until a real fixture proves the shape.
Do not add "best guess" UI branches for event names that have not been observed locally.
turn.replaced is an AstralOps/Core-generated semantic event, not a Claude/Codex raw mapping. It marks a replaced transcript seq range after editing and resending the last user message. Normal transcript, pending interaction projection, and interaction responses must treat events in that seq range as stale/hidden.
message.user attachments and message.media are first-class transcript media surfaces. UI clients render media from AstralEvent.normalized only. Local filesystem paths in normalized media are Host-private references for Core/runtime/media serving; clients must not treat them as directly readable remote paths.
Remote controllers must fetch media through Host/Core media capabilities using event_seq + media_id over the encrypted control/data channel.
If a new event normalization or rendering path changes user-visible state, update fixtures and contract tests before changing UI behavior.
```

## Remote Projection And Cloud Boundary Rules

```text
Remote control event projection must be allowlist-based. Host/runtime internals such as raw payloads, native session/thread IDs, local workspace paths, SSH config, and private transcript media paths must never be sent to Controllers.
New normalized fields are not remote-visible by default; making them visible requires protocol docs and tests that prove the field is safe to project.
Cloud device registry and relay code may store or route only account/device public metadata, presence, routing metadata, trust/revocation state, and opaque sealed envelopes.
Do not add cloud fields for workspace/session/event payloads, prompts, approvals, file trees, PTY output, SSH config, attachments, or media content.
Daemon must not send cloud account tokens to relay.
Cloud may issue short-lived relay credentials containing account_id_hash, relay_id, kid, iat, and exp; relay validates those credentials locally and routes only opaque envelopes under the derived account namespace.
Desktop UI account/mesh status must be read through the local daemon.
UI-facing cloud account status may show account_id_hash, relay_id, relay_url, credential availability, and credential expiration, but must not expose the relay credential body or cloud account token.
When Desktop UI removes a cloud mesh device and also needs local trust revoked, it must express that as a daemon/Core request such as `revoke_local_trust`; renderer code must not independently compose cloud registry state and Host trust state into a semantic outcome.
Session input while a turn is running must be modeled as an explicit Core decision: start, queue, or steer. Controller UI must not independently decide continuation semantics for remote control.
Desktop app settings, shell theme/window behavior, notifications, logs, and auto updates are local shell concerns unless a future Host management capability explicitly says otherwise.
```
