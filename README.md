# AstralOps

AstralOps is a macOS desktop workstation for running Claude Code and Codex against local or SSH-backed workspaces.

It is not a cloud agent service. The desktop app starts a local Go sidecar, keeps event history on disk, and lets the native agents run on your machine while AstralOps projects files and command execution into the selected workspace.

## What It Does

- Runs Claude Code and Codex sessions from one desktop UI.
- Supports local workspaces and sparse SSH workspaces.
- Streams normalized agent events into a shared transcript model.
- Renders reasoning, messages, command output, file changes, plans, approvals, Ask forms, MCP elicitation, rate limits, and compaction boundaries.
- Handles approval, cancel, interrupt, queued prompt, and follow-up semantics for both agents.
- Uses real runtime events as fixtures before adding semantic UI mappings.

## Architecture

AstralOps has four main pieces:

- `apps/desktop`: Electron, React, and Vite desktop UI.
- `daemon`: local Go sidecar (`astralopsd`) with HTTP/WebSocket APIs, runtime orchestration, event storage, SSH projection, and agent adapters.
- `proxy-agent`: small Go helper copied to SSH hosts for file operations, async command execution, and PTY sessions.
- `protocol`: shared TypeScript event and data contracts.

Runtime state is stored under:

```text
~/.AstralOps
```

The daemon listens on `127.0.0.1` and writes a short-lived runtime token to `~/.AstralOps/runtime/daemon.json`.

## SSH Workspaces

SSH support is a sparse projection, not a full remote filesystem mount.

For an SSH workspace with remote cwd `/root`, AstralOps creates a local projection root like:

```text
~/.AstralOps/projections/<workspace_id>/root
```

File tools hydrate remote files on demand:

```text
remote /root/a.txt
-> local projection/root/a.txt
-> agent reads or edits the projected file
-> successful writes are pushed back to /root/a.txt
```

Command execution is remote:

```text
agent command
-> AstralOps bridge
-> ssh proxy-agent
-> remote shell
```

Claude and Codex perceive this differently:

- Claude Code runs locally with its cwd set to the projection root. Claude hooks map `Read`, `LS`, `Glob`, `Grep`, `Bash`, `Write`, `Edit`, and `MultiEdit` to remote operations.
- Codex app-server runs locally, but its turn cwd is the remote cwd and its process execution goes through AstralOps' Codex exec-server bridge.

This means SSH workspaces behave like remote workspaces for normal file and shell operations, but the native agent process and its account-level configuration still live locally.

## Current Limitations

- SSH projection is sparse. Files that have not been read, listed, or otherwise hydrated may not exist locally.
- Claude Code SSH support uses `claude -p --output-format stream-json` plus hooks, not the full Claude SDK/control protocol.
- Claude plan and Ask interactions in the current headless runtime are handled as follow-up turns, not live in-turn resumptions.
- Agent memory and account-level config are not fully isolated per SSH workspace yet. Claude and Codex may still load their local user-level configuration.
- Some non-critical Codex MCP startup warnings can appear if local connector services are unavailable.
- macOS is the primary desktop target right now.

## Requirements

- macOS
- Node.js and npm
- Go
- Claude Code CLI on `PATH` for Claude sessions
- Codex CLI/app-server on `PATH` for Codex sessions
- SSH access for SSH workspaces

## Development

Install dependencies:

```bash
npm install
```

Run the desktop app:

```bash
npm run dev
```

Run checks:

```bash
npm run check
```

Build everything:

```bash
npm run build
```

Run Go tests only:

```bash
go test ./...
```

## Project Layout

```text
apps/desktop/   Electron and React desktop UI
daemon/         Local sidecar, agent runtimes, SSH bridge, event store
proxy-agent/    Remote SSH helper binary
protocol/       Shared TypeScript contracts
fixtures/       Real observed Claude/Codex event samples
docs/           Implementation notes and SSH behavior audit
AGENTS.md       Project-level event contract and agent guidance
```

## Event Contract

AstralOps preserves raw Claude/Codex events in `AstralEvent.raw` and drives UI/business logic from normalized event families such as:

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

When adding new mappings, capture real Claude Code or Codex samples first, add fixtures, then implement the specific observed shape. Unknown events should remain raw/debug-only until their semantics are proven.

## Security Notes

- The daemon only binds to localhost.
- Runtime auth uses a random bearer token written under `~/.AstralOps/runtime`.
- SSH helper binaries are copied to the remote host under `/tmp/.astralops`.
- Hidden hook output is scrubbed before normalized UI display, but raw event logs intentionally preserve original runtime payloads for replay and debugging.

## License

No license has been declared yet.
