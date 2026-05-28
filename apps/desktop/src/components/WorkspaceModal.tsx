import { Check, FolderGit2, Server, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { CreateWorkspaceRequest } from "@astralops/protocol";
import type { WorkspaceDraft } from "../types";

type WorkspaceModalProps = {
  open: boolean;
  onChooseDirectory: () => Promise<string | null>;
  onClose: () => void;
  onCreate: (request: CreateWorkspaceRequest) => Promise<void>;
};

const initialDraft: WorkspaceDraft = {
  name: "",
  target: "local",
  local_cwd: "",
  ssh_endpoint: "",
  ssh_port: 22,
  ssh_remote_cwd: "",
};

export function WorkspaceModal({
  open,
  onChooseDirectory,
  onClose,
  onCreate,
}: WorkspaceModalProps): React.JSX.Element | null {
  const [draft, setDraft] = useState<WorkspaceDraft>(initialDraft);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setDraft(initialDraft);
      setError("");
      setBusy(false);
    }
  }, [open]);

  if (!open) return null;

  const canCreate =
    draft.target === "local"
      ? draft.local_cwd.trim() !== ""
      : draft.ssh_endpoint.trim() !== "" && draft.ssh_remote_cwd.trim().startsWith("/");

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
      if (draft.target === "local") {
        setError("先选择本机文件夹");
      } else {
        setError("填写 SSH endpoint，并使用绝对远端路径");
      }
      return;
    }
    setBusy(true);
    setError("");
    try {
      const request: CreateWorkspaceRequest =
        draft.target === "ssh"
          ? {
              name: draft.name || draft.ssh_endpoint || "SSH",
              target: "ssh",
              ssh: {
                endpoint: draft.ssh_endpoint.trim(),
                port: draft.ssh_port || 22,
                remote_cwd: draft.ssh_remote_cwd.trim(),
              },
            }
          : {
              name: draft.name || draft.local_cwd.split("/").at(-1) || "Local",
              target: "local",
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
    <div className="fixed inset-0 z-[var(--ao-z-modal)] grid place-items-center bg-[rgba(20,20,22,0.36)] p-6 backdrop-blur-md">
      <section className="w-[min(580px,100%)] overflow-hidden rounded-[18px] border border-[#e9e7e1] bg-white shadow-[0_18px_48px_rgba(29,29,31,0.16)]">
        <header className="flex items-start justify-between gap-5 border-b border-[#e7e5e0] px-5 pb-4 pt-5">
          <div>
            <h2 className="m-0 text-[18px] font-bold text-[#1d1d1f]">创建工作区</h2>
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
                description="本机文件夹"
                icon={<FolderGit2 size={17} strokeWidth={1.8} />}
                label="本地"
                onClick={() => setDraft((current) => ({ ...current, target: "local" }))}
              />
              <TargetChoice
                active={draft.target === "ssh"}
                description="远程目录"
                icon={<Server size={17} strokeWidth={1.8} />}
                label="SSH 远程"
                onClick={() => setDraft((current) => ({ ...current, target: "ssh" }))}
              />
            </div>
          </Field>

          {draft.target === "local" ? (
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
          ) : (
            <div className="grid gap-3">
              <Field label="SSH 地址">
                <input
                  className="h-10 w-full rounded-xl border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                  placeholder="root@example.com"
                  value={draft.ssh_endpoint}
                  onChange={(event) => setDraft((current) => ({ ...current, ssh_endpoint: event.target.value }))}
                />
              </Field>
              <div className="grid grid-cols-[110px_1fr] gap-3">
                <Field label="端口">
                  <input
                    className="h-10 w-full rounded-xl border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                    min={1}
                    type="number"
                    value={draft.ssh_port}
                    onChange={(event) => setDraft((current) => ({ ...current, ssh_port: Number(event.target.value) || 22 }))}
                  />
                </Field>
                <Field label="远程目录">
                  <input
                    className="h-10 w-full rounded-xl border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                    placeholder="/home/user/project"
                    value={draft.ssh_remote_cwd}
                    onChange={(event) => setDraft((current) => ({ ...current, ssh_remote_cwd: event.target.value }))}
                  />
                </Field>
              </div>
            </div>
          )}

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
            创建工作区
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
