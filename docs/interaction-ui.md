# AstralOps Interaction UI Semantics

This document records Ask, plan, approval, elicitation, and pending interaction semantics per runtime.

## Claude Code Local Interaction Semantics

```text
With the current `claude -p --output-format stream-json` runtime, AskUserQuestion and ExitPlanMode are not live resumable ServerRequests.
Claude emits the tool_use, then the CLI emits an error tool_result because no interactive answer exists, and the turn can finish with permission_denials.
AstralOps may show an Ask/plan/permission surface, but responding must be treated as a follow-up turn sent with --resume, not as unblocking the same Claude turn.
Do not label Claude confirmations as if they continue the current turn unless a future real control-protocol fixture proves that behavior.
Real SSH/local samples show Claude may call AskUserQuestion repeatedly inside one stream-json turn after each non-interactive Ask denial.
In that shape, earlier AskUserQuestion requests in the same turn are stale attempts; only the latest AskUserQuestion from that turn may remain actionable in the UI.
Cancelling, skipping, or answering that latest Ask must not reveal older AskUserQuestion attempts from the same turn.
```

## Interaction UI Rules

```text
Claude AskUserQuestion: render all observed questions. Questions with options are choice inputs; multiSelect allows multiple choices; free text is only shown when no options are present or a real question shape explicitly permits custom/other input. Submitting emits ask.resolved and starts a Claude follow-up turn with the answer payload. Skipping emits ask.resolved with an empty/declined answer payload and also follows up; it does not resume the same turn.
Claude ExitPlanMode: render the plan text and accept/decline choices. Decline may include user feedback text. Submitting emits approval.responded and starts a Claude follow-up turn. Do not present it as a live in-turn unblock until a future control-protocol fixture proves that behavior.
Codex command/file approvals: render concrete command/cwd or file paths/changes. Accept/decline responds to the original JSON-RPC ServerRequest and the same turn continues. No custom input should be shown unless a real request schema adds it.
Codex permissions approval: render cwd/reason/permissions. UI may offer one-turn and session-scoped acceptance. Decline is a JSON-RPC error response; same turn receives the rejection.
Codex item/tool/requestUserInput: render all questions, options, multiSelect, isOther/custom input, and isSecret password-style input. Submitting sends answers keyed by question id to the original ServerRequest so the same turn continues. Skipping sends an empty answers object.
Codex mcpServer/elicitation/request: render a dedicated MCP elicitation surface. Form mode collects JSON content matching requestedSchema; URL mode shows the URL and returns accept/decline/cancel. Respond to the original ServerRequest with action/content/_meta; do not treat it as a generic AskUserQuestion.
The secondary button label must name the actual response: refuse, cancel, skip, or do not accept. Do not label a response-sending button "ignore" when it actually resolves or rejects the agent request.
```

## Pending Interaction Display Rules

```text
Permission, command, file-change, Ask, MCP elicitation, and plan confirmation surfaces must show the concrete decision target from AstralEvent.normalized, such as command, cwd, tool name, reason, file/change summary, prompt, or params.
Do not show generic approval text when normalized data contains a more specific target.
Pending interaction detail rows must carry machine-readable keys such as `command`, `cwd`, `path`, or `reason`; Core/Gateway logic must branch on those keys, not translated UI labels.
```
