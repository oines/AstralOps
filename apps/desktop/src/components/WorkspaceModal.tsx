import { Bot, Check, FolderGit2, Server, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { CreateWorkspaceRequest } from "@astralops/protocol";
import type { HealthResponse, WorkspaceDraft } from "../types";

type WorkspaceModalProps = {
  defaultAgent: "claude" | "codex";
  health: HealthResponse | null;
  open: boolean;
  onChooseDirectory: () => Promise<string | null>;
  onClose: () => void;
  onCreate: (request: CreateWorkspaceRequest) => Promise<void>;
};

const initialDraft: WorkspaceDraft = {
  name: "",
  target: "local",
  agent: "claude",
  local_cwd: "",
  ssh_endpoint: "",
  ssh_port: 22,
  ssh_remote_cwd: "~",
};

export function WorkspaceModal({
  defaultAgent,
  health,
  open,
  onChooseDirectory,
  onClose,
  onCreate,
}: WorkspaceModalProps): React.JSX.Element | null {
  const [draft, setDraft] = useState<WorkspaceDraft>({ ...initialDraft, agent: defaultAgent });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setDraft({ ...initialDraft, agent: defaultAgent });
      setError("");
      setBusy(false);
    }
  }, [defaultAgent, open]);

  if (!open) return null;

  const agents = health?.agents;
  const claudeAvailable = Boolean(agents?.claude.available);
  const codexAvailable = Boolean(agents?.codex.available);
  const selectedAvailable = draft.agent === "claude" ? claudeAvailable : codexAvailable;
  const canCreate = draft.target === "local" && selectedAvailable && draft.local_cwd.trim() !== "";

  async function chooseFolder(): Promise<void> {
    const folder = await onChooseDirectory();
    if (!folder) return;
    setDraft((current) => ({
      ...current,
      local_cwd: folder,
      name: current.name || folder.split("/").filter(Boolean).at(-1) || "Local",
    }));
  }

  async function submit(): Promise<void> {
    if (!canCreate) {
      setError(draft.local_cwd.trim() === "" ? "先选择本机文件夹" : "当前 agent 不可用");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const request: CreateWorkspaceRequest = {
        name: draft.name || draft.local_cwd.split("/").at(-1) || "Local",
        target: "local",
        agent: draft.agent,
        local_cwd: draft.local_cwd,
      };
      await onCreate(request);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : String(createError));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-[rgba(20,20,22,0.36)] p-6 backdrop-blur-md">
      <section className="w-[min(580px,100%)] overflow-hidden rounded-[18px] border border-[#e9e7e1] bg-white shadow-[0_18px_48px_rgba(29,29,31,0.16)]">
        <header className="flex items-start justify-between gap-5 border-b border-[#e7e5e0] px-5 pb-4 pt-5">
          <div>
            <h2 className="m-0 text-[18px] font-bold text-[#1d1d1f]">创建工作区</h2>
            <p className="m-0 mt-1.5 max-w-[430px] text-[13px] leading-5 text-[#6b6b70]">
              选择本机目录后进入会话。SSH 远程路径保留到下一阶段。
            </p>
          </div>
          <button className="grid size-8 place-items-center rounded-[10px] text-[#98979c] hover:bg-black/[0.035]" type="button" title="Close" onClick={onClose}>
            <X size={16} />
          </button>
        </header>

        <div className="grid gap-4 px-5 py-5">
          <Field label="位置">
            <div className="grid grid-cols-2 gap-2">
              <TargetChoice
                active={draft.target === "local"}
                description="选择本机文件夹"
                icon={<FolderGit2 size={17} strokeWidth={1.8} />}
                label="本地"
                onClick={() => setDraft((current) => ({ ...current, target: "local" }))}
              />
              <TargetChoice
                active={false}
                description="即将支持"
                disabled
                icon={<Server size={17} strokeWidth={1.8} />}
                label="SSH 远程"
                onClick={() => undefined}
              />
            </div>
          </Field>

          <Field label="本机文件夹">
            <button
              className="flex h-11 w-full items-center gap-2 rounded-xl border border-[#e7e5e0] bg-[#f7f6f3] px-3 text-left text-[14px] font-semibold text-[#343438] hover:bg-[#f1f0ec]"
              type="button"
              onClick={() => void chooseFolder()}
            >
              <FolderGit2 size={17} strokeWidth={1.8} />
              <span className={draft.local_cwd ? "min-w-0 flex-1 truncate font-mono text-[13px]" : "min-w-0 flex-1 truncate text-[#96949a]"}>
                {draft.local_cwd || "选择文件夹..."}
              </span>
            </button>
          </Field>

          <Field label="Agent">
            <div className="grid grid-cols-2 gap-2">
              <AgentChoice
                active={draft.agent === "claude"}
                available={claudeAvailable}
                label="Claude Code"
                meta={agents?.claude.version || agents?.claude.path || "未找到"}
                onClick={() => setDraft((current) => ({ ...current, agent: "claude" }))}
              />
              <AgentChoice
                active={draft.agent === "codex"}
                available={codexAvailable}
                label="Codex"
                meta={agents?.codex.version || agents?.codex.path || "未找到"}
                onClick={() => setDraft((current) => ({ ...current, agent: "codex" }))}
              />
            </div>
          </Field>

          <Field label="名称">
            <input
              className="h-10 w-full rounded-xl border border-[#e7e5e0] bg-[#f7f6f3] px-3 text-[14px] outline-none focus:border-[#2563eb]"
              placeholder="默认使用文件夹名"
              value={draft.name}
              onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))}
            />
          </Field>

          {error ? <div className="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-[13px] text-red-600">{error}</div> : null}
        </div>

        <footer className="flex justify-end gap-2 border-t border-[#e7e5e0] px-5 pb-5 pt-4">
          <button className="rounded-full border border-[#e7e5e0] px-4 py-2 text-[14px] font-semibold text-[#1d1d1f] hover:bg-black/[0.035]" type="button" onClick={onClose}>
            取消
          </button>
          <button className="rounded-full bg-[#2563eb] px-4 py-2 text-[14px] font-semibold text-white disabled:opacity-50" type="button" disabled={busy || !canCreate} onClick={() => void submit()}>
            创建并开始
          </button>
        </footer>
      </section>
    </div>
  );
}

type FieldProps = {
  children: React.ReactNode;
  label: string;
};

function Field({ children, label }: FieldProps): React.JSX.Element {
  return (
    <label className="grid gap-1.5">
      <span className="text-[12px] font-semibold text-[#6b6b70]">{label}</span>
      {children}
    </label>
  );
}

type TargetChoiceProps = {
  active: boolean;
  description: string;
  disabled?: boolean;
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
};

function TargetChoice({ active, description, disabled = false, icon, label, onClick }: TargetChoiceProps): React.JSX.Element {
  return (
    <button
      className={`flex min-w-0 items-center gap-2 rounded-xl border px-3 py-3 text-left transition ${
        active
          ? "border-[#2563eb] bg-[#eff4ff] text-[#1d1d1f]"
          : "border-[#e7e5e0] bg-[#f7f6f3] text-[#343438] hover:bg-[#f1f0ec]"
      } ${disabled ? "cursor-not-allowed opacity-50 hover:bg-[#f7f6f3]" : ""}`}
      type="button"
      disabled={disabled}
      onClick={onClick}
    >
      {icon}
      <span className="min-w-0">
        <span className="flex items-center gap-1.5 text-[14px] font-semibold">
          {label}
          {active ? <Check size={14} strokeWidth={2} /> : null}
        </span>
        <span className="block truncate text-[12px] font-medium text-[#8b8a90]">{description}</span>
      </span>
    </button>
  );
}

type AgentChoiceProps = {
  active: boolean;
  available: boolean;
  label: string;
  meta: string;
  onClick: () => void;
};

function AgentChoice({ active, available, label, meta, onClick }: AgentChoiceProps): React.JSX.Element {
  return (
    <button
      className={`flex min-w-0 items-center gap-2 rounded-xl border px-3 py-2.5 text-left transition ${
        active
          ? "border-[#2563eb] bg-[#eff4ff] text-[#1d1d1f]"
          : "border-[#e7e5e0] bg-[#f7f6f3] text-[#343438] hover:bg-[#f1f0ec]"
      } ${available ? "" : "opacity-55"}`}
      type="button"
      onClick={onClick}
    >
      <Bot size={16} strokeWidth={1.8} />
      <span className="min-w-0">
        <span className="block truncate text-[14px] font-semibold">{label}</span>
        <span className="block truncate text-[12px] font-medium text-[#8b8a90]">{available ? meta : "PATH 中未找到"}</span>
      </span>
    </button>
  );
}
