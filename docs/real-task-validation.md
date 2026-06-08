# AstralOps Real-Task Validation

Real Claude/Codex validation must prioritize user-visible task flow failures over isolated event rendering.

The highest-priority failures are:

```text
repeated Ask/plan/permission loops
non-resumable confirmations presented as resumable
stale pending interactions after a turn has already failed or completed
tasks stuck in requires_action with no valid next action
agents continuing to ask for the same missing permission after the user has accepted, declined, skipped, or cancelled
```

Every local/SSH and default/full-permission test scenario must record whether the agent made forward progress, stopped correctly, or entered a loop/stuck state.

A scenario is not passing just because the UI rendered the latest event.

If a real task exposes repeated questions, repeated plan confirmations, repeated permission prompts, or a mismatch between the displayed action and the actual agent continuation semantics, treat it as a blocker before expanding coverage to lower-risk event types.
