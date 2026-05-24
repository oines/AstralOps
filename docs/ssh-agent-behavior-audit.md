# AstralOps Agent Behavior and SSH Audit Log

Last updated: 2026-05-24

This log records concrete behavior mismatches found while testing Claude Code and Codex against AstralOps. Each resolved issue should add an entry with the observed failure, the implementation change, and the verification evidence.

## Resolved Issues

### 1. Approval and Ask dialogs had no real cancel/interrupt path

- Observed: permission and Ask surfaces could decline or submit, but closing/cancelling was not modeled as an agent task interruption. This did not match the visible "cancel task" semantics in Claude Code/Codex style approval flows.
- Fix: `daemon/interactions.go`, `apps/desktop/src/components/PendingInteractionPanel.tsx`, and `apps/desktop/src/interactionPayloads.ts` now distinguish secondary refusal/skip responses from task cancellation. ESC and the cancel action call runtime interrupt where appropriate. If the runtime is already idle, cancellation records `turn.cancelled` instead of a misleading `control.error`.
- Verification: added daemon tests for cancel handling and idle cancel behavior; desktop type checks pass. A real temporary-daemon Claude local Ask task emitted one `ask.requested`; cancelling it emitted `ask.resolved` and `turn.cancelled`, did not start a follow-up turn, and returned the session to `idle`. A real temporary-daemon Codex local command approval for `sleep 10; echo SHOULD_NOT_EXIST > codex_cancel_marker.txt` was cancelled with the native `cancel` decision; Codex resolved the approval, completed the tool with status `declined`, completed the turn, and did not create the marker file.

### 2. Approval payloads lost concrete Codex decision semantics

- Observed: Codex command and permissions approvals were simplified too aggressively. Complex command decisions and requested permission scopes could be dropped when responding.
- Fix: `daemon/codex_approval.go`, `daemon/codex_enrichment.go`, and `daemon/codex_normalizer.go` preserve complex decision objects, requested permission scopes, concrete command/file targets, and available native decisions.
- Verification: `TestCodexApprovalResponsePayloads` covers command accept, command amendment, permissions session scope, Ask answers, MCP elicitation, and unsupported methods.

### 3. Session titles used local fallback text over native agent titles

- Observed: session title projection could stick to the first user sentence and miss later native title/name updates from Codex or Claude.
- Fix: `apps/desktop/src/eventStore.ts` and `daemon/store.go` now prefer native session/thread title updates over local fallback text, while preserving the first fallback when no native title exists.
- Verification: title preference tests cover native title updates, Codex `thread/name/updated`, Codex `thread/started`, and Claude `post_turn_summary`.

### 4. SSH transcript hid the real `exec-server transport disconnected` failure

- Observed: the UI filtered `exec-server transport disconnected`, masking the exact failure users needed to debug SSH.
- Fix: removed that filter from `apps/desktop/src/transcriptModel.ts`.
- Verification: transcript model check passes; later real SSH E2E exposed protocol failures clearly enough to diagnose them.

### 5. Claude SSH remote hook generated a Python bridge file

- Observed: Claude SSH used a generated `.py` hook bridge, which was confusing and made the remote implementation look like stray project code.
- Fix: removed Python hook generation and added `daemon/claude_remote_helper.go`, a daemon subcommand `claude-remote-hook` used by hook settings and bridge commands.
- Verification: deleted stale local `hook_bridge.py`; daemon tests and full `go test ./...` pass.

### 6. Claude SSH approval allowed commands locally instead of remotely

- Observed: accepting a Claude SSH command approval could pass local `--allowedTools` semantics rather than authorizing the remote hook command once.
- Fix: `daemon/interactions.go` records a one-shot remote command allow for Claude SSH; local Claude `--allowedTools` remains for local sessions.
- Verification: tests cover exact local Claude allowed tool behavior and one-shot Claude remote command approval.

### 7. Codex exec-server responded to `initialized` notifications

- Observed: Codex exec-server protocol treats `initialized` as a notification. Responding to it is wrong JSON-RPC behavior.
- Fix: `daemon/codex_exec_server.go` ignores `initialized` notifications and returns an error only for unexpected notifications.
- Verification: `TestCodexExecServerInitializedNotificationDoesNotRespond`.

### 8. Codex exec-server sent `failure: ""` on successful reads

- Observed: real Codex interpreted empty-string failure as `ProcessFailed { message: "" }`, so successful remote commands were marked failed with exit code `-1`.
- Fix: `daemon/codex_exec_server.go` now emits `failure: null` unless there is a real failure.
- Verification: `TestCodexExecServerReadNextSeqMatchesCodexCursorContract` checks successful reads return nil failure.

### 9. Codex exec-server output cursor and notifications were incomplete

- Observed: process reads could duplicate output, and remote processes did not send the notification sequence Codex expects.
- Fix: process chunks start at seq 1, `nextSeq` advances to the next unread seq, and `process/output`, `process/exited`, and `process/closed` notifications are emitted with distinct sequence values. WebSocket writes are serialized.
- Verification: `TestCodexExecServerProcessSendsWakeNotifications`, `TestCodexExecServerReadNextSeqMatchesCodexCursorContract`, and `TestCodexExecServerWebSocketE2E`.

### 10. Codex exec-server file operations were not binary-safe

- Observed: remote file read/write paths used text content, which corrupts binary files and does not match current Codex `dataBase64` protocol.
- Fix: `daemon/codex_exec_server.go` and `proxy-agent/main.go` use `dataBase64` for reads/writes, with content fallback only for compatibility.
- Verification: `TestCodexExecServerFileSystemUsesBase64ForBinary` and proxy-agent binary read/write tests.

### 11. Codex exec-server fs options did not match protocol semantics

- Observed: create directory, remove, and copy ignored recursive/force/source/destination option semantics.
- Fix: daemon forwards `recursive`, `force`, `sourcePath`, and `destinationPath`; proxy-agent implements mkdir/remove/copy semantics including recursive directory copy.
- Verification: proxy-agent filesystem option tests pass.

### 12. Codex remote exec lost exact argv/arg0/env semantics

- Observed: remote exec collapsed argv into a shell string, so commands such as `/usr/bin/printf '%s' '$HOME; echo bad'` could be semantically changed.
- Fix: daemon forwards exact `argv`, `arg0`, and `env`; proxy-agent executes exact argv and only falls back to `/bin/sh -lc command` when no argv is provided.
- Verification: `TestCodexExecServerPassesExactArgvToRemoteExec` and proxy-agent exact argv test.

### 13. Codex SSH used the local shell path on the remote host

- Observed: on `root@10.0.1.33`, Codex app-server selected local `/bin/zsh`; the remote host did not have zsh, so read-only commands failed.
- Source evidence: Codex core builds the user shell from the app-server process user shell, not from `SHELL`. The public `thread/start` params do not expose a user-shell override.
- Fix: AstralOps now records the probed remote shell and normalizes Codex shell argv in `daemon/codex_exec_server.go` for SSH exec-server calls. POSIX shell wrappers (`sh`, `bash`, `zsh`, `dash`, `ksh`) are translated to the remote shell path; non-shell argv remain unchanged.
- Verification: `TestCodexExecServerTranslatesLocalShellWrapperToRemoteShell`; real SSH E2E on `root@10.0.1.33` ran `pwd` and `cat a.txt` in `/tmp/astralops-e2e-shelltranslated` and returned `/tmp/astralops-e2e-shelltranslated` plus `alpha`.

### 14. Codex SSH forwarded macOS local sandbox wrappers to Linux remote hosts

- Observed: real Codex sometimes sent `/usr/bin/sandbox-exec ... -- /bin/zsh -lc ...` through the remote exec-server. This is a local macOS sandbox wrapper and cannot run on Linux remote hosts.
- Fix: SSH exec-server strips local `sandbox-exec ... --` wrappers before remote execution, then applies the remote shell normalization above.
- Verification: `TestCodexExecServerStripsLocalSandboxWrapperForRemoteExec`; real SSH E2E with clean code completed successfully with exit code 0 and correct outputs.

### 15. Codex SSH environment override appended duplicate `SHELL`

- Observed: appending `SHELL=/remote/shell` after `os.Environ()` left duplicate env keys, which makes behavior dependent on environment readers.
- Fix: `daemon/codex_runtime.go` uses `withEnvValue` to replace existing env keys for `CODEX_EXEC_SERVER_URL` and `SHELL`.
- Verification: `TestWithEnvValueReplacesExistingValue` and `TestCodexSSHRuntimeUsesRemoteShellEnvironment`.

### 16. Proxy-agent PTY exit events dropped the real exit code

- Observed: proxy-agent emitted an empty PTY `exit` event, and the Codex exec-server treated every PTY exit as code 0. A failing remote interactive process could therefore look successful.
- Fix: `proxy-agent/main.go` now waits for the PTY child once, records the real exit code, and emits it in the `exit` event. `daemon/codex_exec_server.go` consumes `exit_code` when completing PTY processes.
- Verification: `TestPTYExitEventIncludesExitCode` covers a PTY process exiting with code 7; `go test ./daemon -run TestCodexExecServer -count=1` still passes.

### 17. Workspace PTY WebSocket dropped the remote PTY exit code

- Observed: after issue 16 was fixed, a real SSH PTY session still sent only `{"type":"exit"}` to the desktop WebSocket. The daemon handler was discarding `exit_code` while forwarding proxy-agent events.
- Fix: `daemon/handlers.go` now forwards `exit_code` on remote workspace PTY exit events.
- Verification: real SSH PTY E2E against `root@10.0.1.33` reproduced the missing code with `exit 7`; after restarting with the fix, the WebSocket emitted `{"type":"exit","exit_code":7}`. Daemon targeted tests still pass.

### 18. Codex SSH command labels showed local native wrappers instead of the effective remote command

- Observed: after remote execution was fixed, transcript command rows still displayed native Codex command strings such as `/bin/zsh -lc pwd`, even though AstralOps executed `/bin/bash -lc pwd` on the remote host. This made successful remote runs look like they had used a missing local shell.
- Fix: `daemon/codex_exec_server.go` records native argv and effective remote argv for each Codex SSH process. `daemon/codex_runtime.go` enriches Codex command lifecycle events with `native_command`, `effective_command`, and `remote_command`, and sets normalized `command` to the effective remote command. `apps/desktop/src/transcriptModel.ts` prefers the remote/effective command when rendering command rows.
- Verification: `TestCodexRuntimeEnrichesRemoteCommandEventsWithEffectiveCommand` covers normalized event enrichment. Real SSH Codex E2E against `root@10.0.1.33` showed command events with `command: /bin/bash -lc pwd`, `native_command: /bin/zsh -lc pwd`, correct outputs, and exit code 0.

### 19. Codex SSH started local shell snapshotting before remote execution

- Observed: even after SSH command execution was translated to the remote shell, Codex app-server could still start local shell snapshotting for the thread. On hosts where the local default shell path was invalid for the remote workflow, this produced noisy startup warnings unrelated to the actual remote command execution.
- Source evidence: Codex app-server `ThreadStartParams.config` is a dotted-path override map, and Codex upstream tests use `features.shell_snapshot = false` to disable shell snapshotting.
- Fix: `daemon/codex_runtime.go` now sends `config: {"features.shell_snapshot": false}` on SSH `thread/start` and `thread/resume` requests. Local Codex sessions do not receive this override.
- Verification: `TestCodexSSHRuntimeDisablesLocalShellSnapshot` records the fake app-server JSON-RPC request and asserts the SSH `thread/start` params include the remote cwd plus `features.shell_snapshot=false`. Targeted daemon Codex runtime tests pass.

### 20. Codex SSH metadata probes returned generic exec-server errors

- Observed: Codex can probe parent directories while discovering project roots such as `.git`. AstralOps correctly confines SSH file access to the remote cwd, but metadata/list probes that crossed the boundary returned generic JSON-RPC `-32000` errors with messages like `path ... escapes remote cwd ...`.
- Source evidence: Codex upstream exec-server maps filesystem `NotFound` to JSON-RPC code `-32004`; the remote filesystem client specifically treats `-32004` as `io::ErrorKind::NotFound`.
- Fix: `daemon/codex_exec_server.go` now returns `-32004` for SSH `fs/getMetadata` and `fs/readDirectory` boundary/not-found failures while preserving transport errors as real failures.
- Verification: `TestCodexExecServerMetadataBoundaryUsesNotFoundError` covers boundary errors for both metadata and list requests. Targeted exec-server tests pass. A real SSH Codex E2E against `root@10.0.1.33` completed without `path escapes remote cwd` warnings.

### 21. Codex SSH non-PTY process termination did not kill the remote process

- Observed: `process/terminate` closed the AstralOps process state for normal non-PTY Codex exec-server processes, but the proxy-agent `exec` request was synchronous and could not receive a kill request until the remote command had already finished.
- Fix: `proxy-agent/main.go` now supports asynchronous `exec_start` plus `exec_kill`, tracks running exec sessions, and kills the remote OS process on request. `daemon/ssh_proxy.go` and `daemon/codex_exec_server.go` use that evented path for non-PTY Codex SSH processes and send `exec_kill` from `process/terminate` and exec-server shutdown.
- Verification: `TestStartExecCanBeKilled` starts a long-running proxy exec and kills it before completion. `TestCodexExecServerTerminateKillsNonTTYRemoteExec` verifies Codex SSH process termination sends `exec_kill`. Targeted daemon and proxy-agent tests pass. A real SSH Codex E2E against `root@10.0.1.33` confirmed the new async exec path still streams normal `pwd` and `cat a.txt` command results.

### 22. Approval dialogs collapsed native cancel and task cancel semantics

- Observed: when an approval surface included a native `cancel` or `abort` decision, the desktop UI hid the footer `取消任务 ESC` affordance and made ESC submit the native cancel response. This collapsed two distinct semantics: responding to the agent request with "cancel this round" versus interrupting the whole task.
- Fix: `apps/desktop/src/components/PendingInteractionPanel.tsx` now always shows the footer cancel action and always sends the explicit interruption payload from `cancelResponse`. Native `cancel` or `abort` remains available only as a selectable decision inside the approval options.
- Verification: desktop type checking passes. Backend cancel handling is already covered by cancel/interrupt tests for Claude and Codex interactions.

### 23. Claude SSH hidden hook output leaked bridge secrets

- Observed: real Claude SSH E2E completed successfully, but hidden `hook.completed` normalized payloads still contained `ASTRALOPS_TOKEN`, local helper paths, and `claude-remote-hook` command strings inside hook stdout/output. Hidden UI visibility is not enough; normalized event projection should not carry those bridge internals.
- Fix: `daemon/claude_runtime.go` now recursively scrubs Claude remote bridge commands before marking hook lifecycle events hidden/debug. Hook stdout/output preserves the decoded remote command, but removes daemon URL/token/workspace env assignments and local helper paths.
- Verification: `TestScrubClaudeRemoteBridgeEventHidesHooksAndDecodesCommand` now covers hidden hook stdout/output. Targeted Claude remote tests pass. A real Claude SSH E2E against `root@10.0.1.33` completed with no normalized `ASTRALOPS_TOKEN`, `claude-remote-hook`, helper path, or old `hook_bridge.py` leaks.

### 24. Codex SSH exec cancellation could leave remote child processes running

- Observed: the async proxy-agent `exec_kill` path cancelled and killed only the direct command process. Shell-wrapped commands with background children could report as cancelled while the child process kept running on the SSH host.
- Fix: `proxy-agent` now starts non-Windows exec commands in their own process group and kills descendants plus the process group for context cancellation, explicit `exec_kill`, and timeout cancellation. Windows keeps the direct-process fallback.
- Verification: `TestStartExecKillTerminatesProcessGroup` starts a shell command whose background child writes a marker file if it survives cancellation; after `exec_kill`, no marker is written. Targeted proxy-agent kill tests pass.

### 25. Workspace PTY close could leave terminal child processes running

- Observed: PTY close paths killed only the shell process. In interactive shells, background jobs can be placed into a different process group within the same PTY session; a child that ignores SIGHUP survived terminal close and continued running.
- Fix: `proxy-agent` PTY kill and local workspace PTY close now kill discovered descendant processes before killing the command process group. This preserves normal terminal close semantics for the whole session rather than only the shell.
- Verification: `TestPTYKillTerminatesProcessGroup` and `TestLocalWorkspacePTYCloseTerminatesProcessGroup` reproduce the old leak with a background child that writes a survived marker after close; both now pass. A real SSH PTY E2E against `root@10.0.1.33` started a temporary Linux proxy-agent, issued `pty_start`, waited for the child ready marker, sent `pty_kill`, and confirmed no survived marker was written.

### 26. Workspace SSH exec used the old synchronous proxy command path

- Observed: the workspace `/exec` API still called proxy-agent `exec`, which blocks the proxy request loop until the remote command exits. If the desktop request was cancelled or timed out, AstralOps had no request ID it could kill, and the remote command could continue occupying the proxy.
- Fix: `daemon/handlers.go` now runs SSH workspace exec through `exec_start`, waits for its `exit` event, and sends `exec_kill` when the HTTP/request context is cancelled.
- Verification: `TestRemoteWorkspaceExecCancellationKillsProxyExec` uses a fake proxy protocol process to prove cancellation after `exec_start` sends `exec_kill`. The existing real SSH proxy-agent cancellation E2E on `root@10.0.1.33` verifies `exec_kill` terminates remote child processes rather than only resolving UI state.

### 27. Workspace file browser rejected valid `..`-prefixed names

- Observed: local and SSH workspace file path guards used a raw `strings.HasPrefix(rel, "..")` check. A valid file or directory named `..inside` under the workspace root was treated as if it escaped the workspace.
- Fix: `daemon/handlers.go` now rejects only the exact parent marker `..` or paths beginning with `../`, and uses the same helper when normalizing remote file-list entries.
- Verification: `TestWorkspacePathAllowsDotDotPrefixedNames` covers both local and remote workspace path resolution, allowing `..inside` while still rejecting `../outside`.

### 28. Cancelled SSH requests marked healthy workspaces as degraded

- Observed: `sshManager.call` and event-process startup treated `context.Canceled` or deadline expiry from the caller as proxy transport failure. A user closing a file-list, exec, or terminal startup request could drop the live proxy, mark the workspace `degraded`, and stop sessions even though SSH was healthy.
- Fix: `daemon/ssh_proxy.go` now returns the caller context error directly before reconnect/drop/degrade logic. Real transport failures still go through reconnect and degraded handling.
- Verification: `TestSSHManagerContextCancellationDoesNotDegradeWorkspace` and `TestSSHManagerStartEventContextCancellationDoesNotDegradeWorkspace` use a live fake proxy and prove caller cancellation returns `context.Canceled` while preserving the connected state and proxy instance.

### 29. SSH projection and Claude remote writes were not binary-safe

- Observed: proxy-agent and Codex exec-server supported `dataBase64`, but projection push/hydrate/rollback and Claude remote Write/Edit/MultiEdit still moved file bodies through text `content`. Binary or non-UTF-8 files could be corrupted while syncing between the remote workspace and local projection.
- Fix: `daemon/projection.go` now centralizes remote read/write body conversion with `dataBase64`, and `daemon/claude_remote.go` uses that path for hydrate, rollback, and post-tool writeback.
- Verification: `TestProjectionRemoteIOUsesBase64ForBinary` covers binary write params, base64-preferring reads, and invalid base64 rejection. Existing Codex exec-server binary filesystem coverage still passes.

### 30. SSH proxy helper did not advertise or validate core execution protocol methods

- Observed: the failure class shown by `exec-server rejected request (-32000): unknown method "exec_start"` means the daemon and remote proxy-agent are speaking different protocol versions. Before this fix, proxy-agent `hello` did not advertise supported methods, so daemon could accept an incompatible helper and only discover the break after the agent started running basic commands.
- Fix: `proxy-agent` now advertises supported protocol methods in `hello.capabilities.methods`, and `daemon/ssh_proxy.go` validates required P0 methods (`read`, `write`, `list`, `stat`, `exec_start`, `exec_kill`, `pty_start`, `pty_kill`) during SSH connection setup. Incompatible helpers fail fast with an explicit `ssh proxy helper is incompatible` message instead of letting agent turns degrade into repeated tool failures.
- Verification: `TestHelloAdvertisesCoreExecutionMethods`, `TestValidateProxyHelloRequiresCoreExecutionMethods`, and `TestProxyAgentProtocolSmokeE2E` cover the advertised method set and a stdin/stdout JSON protocol smoke with binary read/write, async exec, and PTY exit. A real SSH proxy-agent smoke against `root@10.0.1.33` verified `hello`, `read`, `write`, `list`, `exec_start`, and `pty_start` on the current Linux helper.

### 31. Codex SSH tried to start local node_repl MCP on the remote host

- Observed: a real Codex SSH session completed the basic command/read/write task, but its JSONL contained `MCP client for node_repl failed to start: MCP startup failed: No such file or directory (os error 2)`. Local Codex sessions started `node_repl` successfully, so the failure was specific to SSH. The root cause was Codex app-server inheriting the user's local `mcp_servers.node_repl` config while `CODEX_EXEC_SERVER_URL` redirected process creation to the remote Linux host, where the macOS `/Applications/Codex.app/.../node_repl` binary does not exist.
- Fix: Codex SSH app-server startup now passes the real Codex config override `mcp_servers.node_repl.enabled=false`. This disables only the observed host-local `node_repl` MCP server for SSH sessions. Local Codex sessions do not receive this override.
- Verification: `codex mcp list --json -c 'mcp_servers.node_repl.enabled=false'` reports `node_repl` as disabled, and a direct app-server initialize probe with the same override emitted no `node_repl` startup events. `TestCodexSSHRuntimeDisablesLocalNodeREPLMCPServer` and `TestCodexAppServerArgsKeepLocalNodeREPLMCPServer` cover the app-server args. A real Codex SSH task against `root@10.0.1.33` completed with the requested result file and only `codex_apps` MCP startup events; a real Codex local control task completed with `node_repl` and `codex_apps` both ready.

### 32. Codex approval ids collided across concurrent sessions

- Observed: real Codex local and Codex SSH sessions can both emit `item/commandExecution/requestApproval` with native JSON-RPC request id `0`. Before this fix, AstralOps exposed both approvals as `/v1/approvals/0/respond`; responding once approved only the newest matching session, leaving the other still pending. This made the approval target ambiguous whenever more than one Codex session waited on the same native request id.
- Fix: Codex `approval_id` and `ask_id` are now scoped with the Astral session id, for example `sess_a:0`, while the raw Codex request id remains preserved as `request_id` and in `AstralEvent.raw`. The backend and desktop pending-interaction identity now use only `approval_id` / `ask_id` as actionable ids; Codex raw `request_id` is metadata, not a response route.
- Verification: `TestCodexApprovalIDIsSessionScoped` and `TestFindInteractionEventDoesNotMatchCodexNativeRequestID` cover the id contract. A real E2E created simultaneous Codex local and Codex SSH command approvals, both with native `request_id: 0`; approving `sess_local:0` completed only local and left the SSH output file absent, then approving `sess_ssh:0` completed SSH and wrote its file.

### 33. SSH workspaces could stay disconnected after proxy-agent protocol upgrades

- Observed: the live `root@10.0.1.33:/root` workspace failed with `ssh proxy helper is incompatible: version 0.1.0 missing methods hello, read, write, list, stat, exec_start, exec_kill, pty_start, pty_kill`. The remote `/tmp/.astralops/<workspace>/astral-proxy-agent` was an older helper, and the running desktop daemon also had an older local helper cache, so reconnect kept uploading the incompatible binary.
- Fix: `daemon/ssh_proxy.go` now treats an incompatible helper hello as an upgrade trigger when the helper was not freshly uploaded: it emits `helper_status: upgrading`, force-uploads the current helper, and retries once. In dev/source checkouts, `localHelperBinary` now rebuilds `./proxy-agent` instead of trusting a stale cached helper binary.
- Verification: `TestSSHConnectUpgradesIncompatibleRemoteHelper` reproduces a same-SHA stale helper that omits execution methods until a forced upload occurs. The live workspace was unblocked by replacing the current app helper cache and reconnecting; `/v1/workspaces/ws_cb885d84059c/connect` returned `connected`, and `/v1/workspaces/ws_cb885d84059c/exec` ran `pwd/whoami/hostname` on `FRP-Server` with exit code 0.

### 34. Claude WebSearch permission denials were not actionable

- Observed: a real Claude SSH WebSearch task emitted `tool.started WebSearch`, then `tool.completed` with `Claude requested permissions to use WebSearch`, and the final Claude `result.permission_denials` contained `tool_name: WebSearch`. AstralOps preserved the raw result but did not create an `approval.requested`, so the user saw Claude telling them to authorize WebSearch without any actionable approval surface.
- Fix: `daemon/claude_normalizer.go` now maps the observed Claude `result.permission_denials` shape for `WebSearch` to `approval.requested(kind=permission)`, preserving `tool_name`, `tool_use_id`, and params. Bash result denials remain suppressed because Bash command approvals are produced earlier from live tool-result errors, and AskUserQuestion denials remain de-duplicated against `ask.requested`. Unobserved tool denials remain raw-only until a fixture proves their shape and semantics.
- Verification: `fixtures/claude-stream-json/real-ssh-websearch-permission.jsonl` captures the observed Claude SSH WebSearch permission shape. `TestClaudeResultWebSearchPermissionDenialRequestsApproval`, `TestNormalizeClaudeRealLocalFixtures`, and `TestClaudeLocalRuntimeMarksResultPermissionDenialRequiresAction` cover the normalization and session status. A real temporary-daemon E2E against `root@10.0.1.33` produced `approval.requested` for WebSearch with query `latest OpenAI news`.

### 35. Claude non-Bash permission accept did not authorize the retry

- Observed: even if a WebSearch approval surface existed, accepting it would only start a follow-up turn saying the user approved. `claudeAllowedToolsForInteraction` only generated `--allowedTools` for Bash commands, so Claude could still deny WebSearch on retry.
- Fix: Claude permission follow-ups now pass `AllowedTools: ["WebSearch"]` for the observed WebSearch approval shape. SSH Bash remains handled by the remote hook approval path; local Bash still uses the exact command rule.
- Verification: `TestClaudePermissionAcceptPassesAllowedNonBashTool` covers accepting a WebSearch approval on an SSH workspace. The same real temporary-daemon E2E accepted the WebSearch approval and the follow-up emitted a second `tool.started WebSearch` plus `tool.completed` with `is_error: false`, proving the retry was no longer blocked by the permission denial.

### 36. Completed Claude turns with pending Ask/Approval looked finished

- Observed: in real Claude plan/ask output, `ask.requested` remained pending, but the turn header still showed `已处理` because the turn had a `turn.completed` event. This is expected for current `claude -p --output-format stream-json` non-interactive behavior, where Ask/plan responses become follow-up turns, but the UI label was misleading.
- Fix: the desktop transcript now treats any unresolved `ask.requested` or `approval.requested` within a turn as `等待确认` even when the raw turn has completed. Sidebar session projection also derives `requires_action` from unresolved interactions, so non-active sessions do not appear idle while waiting for user action.
- Verification: the fix is covered by TypeScript checks and validated against the real `sess_679e737afb83` AskUserQuestion event shape: unresolved `ask_id: call_7c5c97e99bc146d1ac54620c` is now considered a pending interaction rather than a completed-only turn.

### 37. Claude AskUserQuestion could loop and require repeated cancellation

- Observed: a real Claude SSH plan/ask task (`sess_3b9e07fd75cd`) emitted five `AskUserQuestion` attempts in one stream-json turn. Claude `-p` treated each Ask as a non-interactive tool denial and continued the same turn, while the UI exposed stale earlier asks after the user cancelled the latest one.
- Fix: `daemon/claude_runtime.go` now pauses the running Claude process as soon as an observed Claude `AskUserQuestion` is emitted, leaving the session in `requires_action` for a follow-up answer instead of letting the same turn continue into repeated asks. The desktop interaction model also marks earlier Claude AskUserQuestion attempts in the same turn as superseded, so historical/replayed events cannot reveal stale asks after the latest one is resolved.
- Verification: `TestClaudeLocalRuntimePausesOnAskUserQuestion` proves a fake Claude process that would emit a second stale Ask is stopped after the first Ask. A real temporary-daemon E2E with Claude local produced exactly one `ask.requested`, no `turn.completed` or `turn.failed` before the answer, and session status `requires_action`; responding with answer `A` emitted `ask.resolved`, started a second turn, completed it, and returned the session to `idle`.

### 38. Claude SSH Bash bridge encoded local projection paths

- Observed: a real Claude SSH read-only scan (`sess_305329773311`) showed `ls -la /root` failing with `No such file or directory`. The raw event proved the actual tool input was `ls -la /Users/oines/.AstralOps/projections/ws_cb885d84059c/root`; the PreToolUse hook wrapped that exact command in `claude-remote-hook exec`, so the remote host executed a local projection path. UI path scrubbing then displayed the failure as `/root`, hiding the real cause.
- Fix: `daemon/claude_remote.go` now remaps `Workspace.LocalProjectionRoot` and its resolved symlink form to `ws.SSH.RemoteCWD` inside Bash command text before encoding it for the remote bridge. This keeps Claude's projected working-directory paths from leaking into remote shell execution, including macOS `/tmp` -> `/private/tmp` path resolution.
- Verification: `TestClaudeRemoteReadOnlyBashHookAllowsSafeCommandViaRemoteBridge` covers the observed shape by passing a Bash command containing the local projection root and asserting the decoded bridge command uses `/root`. `TestClaudeRemoteBashCommandRemapsResolvedProjectionRoot` covers the resolved symlink path form. A real temporary-daemon Claude SSH task against `root@10.0.1.33:/root` ran `ls -la` successfully, completed the turn, had no normalized projection path leak, and produced no `No such file or directory` output.

## Current Real SSH Evidence

- Host: `root@10.0.1.33`
- Remote test directory: `/tmp/astralops-e2e-ssh-async-1779623738`
- Remote file: `a.txt` containing `alpha`
- Clean E2E result: Codex SSH session completed through the async exec path, command events displayed `/bin/bash -lc pwd` and `/bin/bash -lc 'cat a.txt'`, and outputs were `/tmp/astralops-e2e-ssh-async-1779623738` plus `alpha`.
- Claude SSH E2E result: Claude SSH session completed against `/tmp/astralops-e2e-claude-ssh-scrub-1779624434`, ran remote `pwd` and `cat a.txt`, returned the remote directory plus `alpha`, and normalized event payloads did not leak hook helper internals.
- Claude SSH write E2E result: Claude created remote `/tmp/astralops-e2e-claude-write-1779624628/out.txt`; direct SSH verification read `beta` with the requested trailing newline.
- Proxy-agent cancellation E2E result: a temporary Linux proxy-agent binary on `root@10.0.1.33` started an async shell command with a background child, received `exec_kill` after the child signaled ready, and did not leave the child alive long enough to write its survived marker.
- Proxy-agent PTY cancellation E2E result: a temporary Linux proxy-agent binary on `root@10.0.1.33` started a PTY command with a background child that ignored SIGHUP, received `pty_kill` after the child signaled ready, and did not leave the child alive long enough to write its survived marker.
- P0 proxy protocol smoke result: a temporary Linux proxy-agent binary on `root@10.0.1.33` advertised 18 protocol methods and completed binary write/read, directory list, `exec_start` with shell output, and `pty_start` with exit code 7.
- P0 real agent task E2E result: a temporary daemon submitted harmless tasks to real Codex SSH, Codex local, Claude SSH, and Claude local sessions. All four completed, and each created the requested result file. The Codex SSH session JSONL was inspected after a `No such file or directory` match; the match was a `node_repl` MCP startup warning, while the core SSH command/read/write events completed with exit code 0 and effective `/bin/bash` remote commands.
- Codex SSH node_repl fix E2E result: after disabling only `mcp_servers.node_repl` for SSH app-server startup, a real Codex SSH task completed on `root@10.0.1.33`, wrote `result.txt`, emitted no warnings, and had only `codex_apps` MCP startup events. A real Codex local control task still emitted `node_repl` starting/ready plus `codex_apps` starting/ready with no warnings.
- Codex concurrent approval E2E result: real Codex local and Codex SSH turns both requested command approval with native `request_id: 0`. AstralOps exposed them as session-scoped ids and routed each response to the intended session; the SSH result file remained absent until the SSH-scoped approval id was submitted.
- Codex command cancel E2E result: real Codex local and Codex SSH command approvals were cancelled through their session-scoped approval ids. Both tools completed with Codex status `declined`, both turns completed, and neither local nor remote output file was created.
- Current cancel regression result: a fresh temporary-daemon Claude local Ask cancel produced `ask.resolved` plus `turn.cancelled` with session `idle`; a fresh temporary-daemon Codex local command cancel produced `approval.resolved`, `tool.completed(status=declined)`, `turn.completed`, and no marker file.
- Codex Ask E2E result: real Codex local and Codex SSH turns in Plan mode emitted `item/tool/requestUserInput` with native `request_id: 0` and session-scoped `ask_id`s. Responding to the local ask completed only local with answer `A`; the SSH ask stayed pending until its own scoped ask id was answered with `B`.
- Codex file-change approval E2E result: real Codex local and Codex SSH turns both emitted `item/fileChange/requestApproval` with native `request_id: 0` and session-scoped approval ids. Accepting the local file change changed only the local `a.txt` from `alpha` to `beta`; the SSH file remained `alpha` until its own response. Cancelling the SSH file change left the remote file unchanged and the turn completed.
- SSH helper upgrade recovery result: the live desktop workspace `ws_cb885d84059c` against `root@10.0.1.33:/root` recovered from an incompatible cached helper after the helper cache was updated. The connection state is `connected`, `helper_status` is `running`, advertised methods include async exec and PTY methods, and a remote exec returned `/root`, `root`, and `FRP-Server`.
- Claude SSH WebSearch permission E2E result: a temporary daemon submitted a real Claude SSH task asking for WebSearch on `root@10.0.1.33`. The first turn emitted `approval.requested(kind=permission, tool_name=WebSearch, params.query=latest OpenAI news)` after the real Claude permission denial. Accepting that approval started a follow-up with WebSearch allowed; the follow-up emitted `tool.started WebSearch`, `tool.completed(is_error=false)`, and a final `turn.completed`.
- Current WebSearch regression result: a fresh temporary-daemon Claude SSH task against `root@10.0.1.33:/root` searched `OpenAI Codex May 2026`. The first turn produced one WebSearch approval from the real permission denial; accepting it produced a second `tool.started WebSearch`, `tool.completed(is_error=false)`, a second `turn.completed`, no `turn.failed`, and session `idle`.
- Claude Ask loop E2E result: a temporary daemon submitted a real Claude local task that explicitly requested one `AskUserQuestion`. The daemon paused the turn at the first Ask, emitted exactly one `ask.requested`, left the session `requires_action`, then completed a follow-up turn after answer `A` with no repeated asks.
- Check gates after the fixes:
  - `go test ./...`
  - `npm run check -w apps/desktop`
  - `npm run check -w protocol`
  - `npm run build -w apps/desktop`

## Open Issues and Risks

- No open SSH execution correctness issue remains from this audit pass: the real Codex/Claude, SSH/local task matrix completed basic command, read, and write work.
- SSH sessions intentionally do not expose the host-local `node_repl` MCP server. Remote MCP support should be designed explicitly instead of reusing local macOS MCP command paths through the SSH exec-server.
- Future work should still capture fresh Claude Code/Codex fixtures when either upstream agent changes its control protocol.
