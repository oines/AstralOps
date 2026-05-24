# AstralOps

AstralOps is a macOS desktop agent workstation for running Claude Code or Codex locally while targeting either a local workspace or a sparse SSH projection.

The first implementation focuses on the V1 control plane:

- Electron + React desktop shell.
- Go `astralopsd` sidecar on `127.0.0.1` with a runtime bearer token.
- JSON/JSONL persistence under `~/.AstralOps`.
- HTTP and WebSocket event bus.
- Workspace/session APIs.
- Stdio JSON-RPC `astral-proxy-agent` foundation.

AstralOps does not modify `~/.claude/settings.json` or `~/.codex/config.toml`.

## Development

```bash
npm install
npm run dev
```

Useful checks:

```bash
npm run check
npm run build
```
