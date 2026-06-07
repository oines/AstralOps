import assert from "node:assert/strict";
import test from "node:test";
import {
  ControllerRuntime,
  buildSessionInputOptions,
  buildWorkbenchFromSnapshot,
} from "../dist/runtime.js";

const now = "2026-01-01T00:00:00.000Z";

test("buildWorkbenchFromSnapshot indexes sessions, views, and connections", () => {
  const snapshot = hostSnapshot();
  const workbench = buildWorkbenchFromSnapshot(snapshot);
  assert.equal(workbench.workspaces.ws1.id, "ws1");
  assert.equal(workbench.sessions.s1.title, "View title");
  assert.equal(workbench.workspace_connections.ws1.status, "connected");
});

test("ControllerRuntime selects a session and loads its transcript events", async () => {
  const host = fakeHost();
  const runtime = new ControllerRuntime({ host, hostId: "host1" }, { restoreOnLaunch: true, refreshSessionViewDelayMs: 0 });
  await runtime.start();
  await runtime.selectSession("s2");
  const snapshot = runtime.snapshot();
  assert.equal(snapshot.selectedWorkspaceId, "ws1");
  assert.equal(snapshot.selectedSessionId, "s2");
  assert.deepEqual(snapshot.selectedSessionEvents.map((event) => event.seq), [10]);
  runtime.stop();
});

test("ControllerRuntime deletion reconciles selection to a remaining session", async () => {
  const host = fakeHost();
  const runtime = new ControllerRuntime({ host, hostId: "host1" }, { restoreOnLaunch: true });
  await runtime.start();
  await runtime.selectSession("s1");
  await runtime.deleteSession("s1");
  assert.equal(runtime.snapshot().selectedSessionId, "s2");
  runtime.stop();
});

test("buildSessionInputOptions preserves host-owned attachment handles", () => {
  const options = buildSessionInputOptions({
    model: "gpt-test",
    permission_mode: "default",
    attachments: [{
      id: "att1",
      kind: "image",
      name: "image.png",
      media_id: "media1",
      host_owned: true,
      mime_type: "image/png",
      size: 12,
      detail: "high",
    }],
  });
  assert.equal(options.model, "gpt-test");
  assert.equal(options.permission_mode, undefined);
  assert.equal(options.attachments?.[0].media_id, "media1");
  assert.equal(options.attachments?.[0].host_owned, true);
});

function hostSnapshot() {
  return {
    host: { identity: { device_id: "host1", public_key_fingerprint: "fp" }, started_at: now, version: "test", agents: {} },
    workspaces: [{ id: "ws1", name: "Workspace", target: "local", agent: "codex", created_at: now, updated_at: now }],
    sessions: [
      { id: "s1", workspace_id: "ws1", agent: "codex", title: "First", status: "idle", created_at: now, updated_at: "2026-01-01T00:00:01.000Z" },
      { id: "s2", workspace_id: "ws1", agent: "codex", title: "Second", status: "idle", created_at: now, updated_at: "2026-01-01T00:00:02.000Z" },
    ],
    workspace_connections: [{ workspace_id: "ws1", target: "local", status: "connected", updated_at: now }],
    session_views: [{
      session: { id: "s1", workspace_id: "ws1", agent: "codex", title: "First", status: "idle", created_at: now, updated_at: now },
      title: "View title",
      status: "idle",
    }],
    events: [],
    initial_session_events: [],
  };
}

function fakeHost() {
  let snapshot = hostSnapshot();
  return {
    hostDeviceId: "host1",
    terminal: {
      createWorkspaceTerminal: async () => ({ terminal_id: "term1", output_seq: 0 }),
      openWorkspaceTerminal: () => ({ input() {}, resize() {}, ackRendered() {}, close() {} }),
      closeWorkspaceTerminal: async () => ({ ok: true }),
    },
    state: async () => ({ host_device_id: "host1", state: "live" }),
    subscribeState: () => ({ close() {} }),
    snapshot: async () => snapshot,
    workbench: async () => buildWorkbenchFromSnapshot(snapshot),
    subscribeWorkbench: () => ({ close() {} }),
    events: async (query = {}) => {
      const sessionId = typeof query === "object" ? query.session_id : "";
      return sessionId === "s2"
        ? [{ seq: 10, ts: now, workspace_id: "ws1", session_id: "s2", agent: "codex", kind: "message.user", normalized: { text: "hello" } }]
        : [];
    },
    subscribeEvents: () => ({ close() {} }),
    createWorkspace: async (input) => ({ id: "ws2", name: input.name, target: input.target ?? "local", agent: "codex", created_at: now, updated_at: now }),
    connectWorkspace: async (workspaceId) => ({ workspace_id: workspaceId, target: "local", status: "connected", updated_at: now }),
    disconnectWorkspace: async (workspaceId) => ({ workspace_id: workspaceId, target: "local", status: "disconnected", updated_at: now }),
    listWorkspaceFiles: async () => ({ root: "", path: "", entries: [] }),
    runWorkspaceCommand: async (_workspaceId, command) => ({ command, cwd: "", exit_code: 0, stdout: "", stderr: "", duration_ms: 0 }),
    deleteWorkspace: async (workspaceId) => {
      const workbench = buildWorkbenchFromSnapshot(snapshot);
      delete workbench.workspaces[workspaceId];
      snapshot = { ...snapshot, workbench, workspaces: Object.values(workbench.workspaces), sessions: Object.values(workbench.sessions), session_views: Object.values(workbench.session_views), workspace_connections: Object.values(workbench.workspace_connections) };
      return { ok: true };
    },
    createSession: async (workspaceId) => ({ id: "s3", workspace_id: workspaceId, agent: "codex", title: "New", status: "idle", created_at: now, updated_at: now }),
    sessionView: async (sessionId) => ({
      session: { id: sessionId, workspace_id: "ws1", agent: "codex", title: sessionId, status: "idle", created_at: now, updated_at: now },
      title: sessionId,
      status: "idle",
    }),
    deleteSession: async (sessionId) => {
      const workbench = buildWorkbenchFromSnapshot(snapshot);
      delete workbench.sessions[sessionId];
      delete workbench.session_views[sessionId];
      snapshot = { ...snapshot, workbench, sessions: Object.values(workbench.sessions), session_views: Object.values(workbench.session_views) };
      return { ok: true };
    },
    forkSession: async () => ({ session: { id: "s4", workspace_id: "ws1", agent: "codex", title: "Fork", status: "idle", created_at: now, updated_at: now } }),
    sendInput: async () => ({ ok: true }),
    editLastUserMessage: async () => ({ ok: true }),
    interrupt: async () => ({ ok: true }),
    cancelQueuedInput: async () => ({ ok: true }),
    steerQueuedInput: async () => ({ ok: true }),
    respondApproval: async () => ({ ok: true }),
    mediaUrl: () => "",
  };
}
