# AstralOps Agent Notes

This file is the project-level source of truth for **hard rules every change must follow**.
Reference material (event coverage, interaction UI specs, fixtures) lives under `docs/` — read it when a task needs it, not on every edit.

---

## 1. Public repository secrets

This repo is public. Never commit real secrets of any kind: OAuth client secrets, database URLs, service credentials, access/refresh/account tokens, relay/cloud tokens, private keys, VPS credentials, `.env` files, local DB files, dumps, or production config with real values.

Commit only sanitized examples (`.env.example` with placeholders). If a task needs credentials, keep values out-of-band and document only the variable names.

Production cloud control-plane code belongs in the private `oines/AstralOps-Cloud` repo. Public code may keep client contracts, protocol docs, relay-facing client code, and test-only fake brokers — but must not add a deployable production cloud account service here.

Relay and cloud stay separate. Public `relay/` routes only opaque encrypted envelopes. Cloud may return account relay config, but must not mount `/v1/relay/*` or store relay payloads.

---

## 2. Single source of truth for protocol

Remote control **action names, capability names, parameter schemas, and result schemas** have exactly one protocol source of truth, shared by Host, Controller, Desktop, mobile, and TypeScript. Do not add duplicate string tables or ad hoc action/capability literals in separate modules.

Adding or changing a remote control action requires updating the protocol docs and contract tests for every surface that can send, receive, or render it.

Transport payloads may be JSON, but business handlers must decode params into typed schemas before acting. Unknown fields, missing required fields, and wrong types must fail explicitly, never be silently ignored.

The AstralOps normalized event families are fixed (`docs/event-contract.md`). Do not invent new event families without updating that doc, protocol types, and tests. Do not implement speculative fallback mappings for Claude/Codex events.

---

## 3. One semantic writer per state

Every business state has exactly **one** semantic writer. Stores, projections, caches, UI state, mobile bridges, and remote snapshots may display or cache derived state — but must not independently decide:

- session status
- pending interaction state
- remote Host session state
- workspace connection state
- cloud mesh state / relay health

When a projection diverges from its source, fix the projection and treat the source owner as authoritative. Do not add a second reconciliation loop unless the protocol explicitly defines two sources of truth.

---

## 4. Error propagation

Cross-boundary errors must preserve a stable **code**, a user-visible **message**, and machine-readable **details** when they exist. Do not collapse typed errors into string-only messages at any boundary (HTTP, JSON-RPC, E2EE control, daemon, mobile bridge).

Do not wrap the same error repeatedly (`failed: failed: failed`). Add context once, at the owning boundary, and preserve the original cause for logs.

---

## 5. Remote control projection is allowlist-based

Remote control event projection is **allowlist-based**. Host/runtime internals — raw payloads, native session/thread IDs, local workspace paths, SSH config, private transcript media paths — must never reach Controllers.

New normalized fields are **not** remote-visible by default. Making a field visible requires protocol docs and tests proving it is safe to project.

Remote controllers fetch media via Host/Core-issued handles (`event_seq` + `media_id`) over the encrypted channel. They must not construct, cache, or send back Host-private local paths.

Cloud/relay may store or route only public metadata: account/device public metadata, presence, routing metadata, trust/revocation state, opaque sealed envelopes. No cloud fields for workspace/session/event payloads, prompts, approvals, file trees, PTY output, SSH config, attachments, or media. Daemon must not send cloud account tokens to relay.

---

## 6. Control-plane-first, no patch-stacking

When fixing a bug or adding behavior, first identify the **control plane** that should own the decision, lifecycle, state transition, retry, or recovery. Do not patch each symptom across separate handlers, UI effects, transport adapters, or daemon endpoints if one control surface should coordinate them. If no clear control plane exists, pause and decide whether to create or consolidate one **before** writing implementation code.

If a real issue points to an architectural mismatch, stop and name the architectural fix or refactor boundary for user confirmation — do not layer another patch on the mismatch.

Prefer deleting or narrowing unsupported branches over keeping "just in case" behavior. Temporary compatibility code is allowed only when tied to a specific observed version/shape and documented with the fixture or source that requires it.

---

## 7. Route judgment

When an approach starts looking like a dead end, disproportionately complex, or misaligned with the product goal — pause before continuing. Name the product goal, the simpler alternative, and why continuing (or changing course) fits the architecture. Only continue when the chosen route is still clearly better than the simpler option.

Do not keep drilling into a complex approach just because work has already started. Change course early when a simpler, better-scoped choice fits.

Long-running goals must carry this constraint in the goal itself, not only in chat context.

---

## 8. Core / UI boundary

Frontend clients are delivery and rendering surfaces, not business-logic owners.

Daemon/Core decides facts, derived state, pending interactions, and notification intent/title/body/target/de-duplication. Clients must not map agent/runtime events into notification copy, session state, pending-action semantics, or business decisions.

For notifications, clients consume `control.notification.normalized` as a delivery payload and apply only local delivery policy (focused/unfocused, foreground/background, system permission, click handling). Client notification code must not branch on source event kinds. A new client needing different delivery extends the Core notification payload — it does not duplicate notification logic.

Session input while a turn is running is an explicit Core decision: **start, queue, or steer**. Controllers must not independently decide continuation semantics.

---

## 9. Scope discipline

When implementing a feature, if you notice problematic code outside the feature scope, do not silently fix or refactor it. Finish the requested feature, then report the issue to the user with the file/path and why it matters.

---

## Reference docs (read on demand, not every edit)

- `docs/event-contract.md` — normalized event families, UI states, raw event references, normalization rules
- `docs/event-coverage.md` — current Claude/Codex event coverage audit (snapshot, last audited 2026-05-23)
- `docs/interaction-ui.md` — Ask/plan/approval/elicitation rendering semantics per runtime
- `docs/desktop-ui-design-language.md` — desktop visual language, density, spacing, copy rules
- `docs/real-task-validation.md` — real-task validation priorities (loops, stale interactions, stuck states)
