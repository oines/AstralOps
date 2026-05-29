import { Check, ChevronLeft, Folder, FolderGit2, HardDrive, LoaderCircle, RefreshCw, Server, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { CreateWorkspaceRequest } from "@astralops/protocol";
import type { HostFileSystemBrowseParams, HostFileSystemBrowseResult, HostFileSystemEntry, HostFileSystemRoot, WorkspaceDraft } from "../types";

type WorkspaceModalProps = {
  hostName: string;
  open: boolean;
  onBrowseFileSystem: (input: HostFileSystemBrowseParams) => Promise<HostFileSystemBrowseResult>;
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
  hostName,
  open,
  onBrowseFileSystem,
  onClose,
  onCreate,
}: WorkspaceModalProps): React.JSX.Element | null {
  const [draft, setDraft] = useState<WorkspaceDraft>(initialDraft);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [browseResult, setBrowseResult] = useState<HostFileSystemBrowseResult | null>(null);
  const [browseLoading, setBrowseLoading] = useState(false);
  const [browseError, setBrowseError] = useState("");

  const selectedPath = draft.target === "ssh" ? draft.ssh_remote_cwd : draft.local_cwd;
  const canBrowseSSH = draft.ssh_endpoint.trim() !== "";
  const canCreate =
    draft.target === "local"
      ? draft.local_cwd.trim() !== ""
      : draft.ssh_endpoint.trim() !== "" && draft.ssh_remote_cwd.trim().startsWith("/");

  const sshConfig = useMemo(
    () => ({
      endpoint: draft.ssh_endpoint.trim(),
      port: draft.ssh_port || 22,
      remote_cwd: draft.ssh_remote_cwd.trim() || "/",
    }),
    [draft.ssh_endpoint, draft.ssh_port, draft.ssh_remote_cwd],
  );

  useEffect(() => {
    if (!open) return;
    setDraft(initialDraft);
    setError("");
    setBusy(false);
    setBrowseResult(null);
    setBrowseLoading(false);
    setBrowseError("");
    void browse({ target: "local" });
  }, [open]);

  if (!open) return null;

  async function browse(input: HostFileSystemBrowseParams): Promise<void> {
    setBrowseLoading(true);
    setBrowseError("");
    try {
      const result = await onBrowseFileSystem(input);
      setBrowseResult(result);
      if (result.target === "ssh") {
        setDraft((current) => ({ ...current, ssh_remote_cwd: result.path }));
      }
    } catch (browseFailure) {
      setBrowseError(browseFailure instanceof Error ? browseFailure.message : String(browseFailure));
    } finally {
      setBrowseLoading(false);
    }
  }

  function setTarget(target: WorkspaceDraft["target"]): void {
    setDraft((current) => ({ ...current, target }));
    setBrowseResult(null);
    setBrowseError("");
    if (target === "local") {
      void browse({ target: "local" });
    }
  }

  function chooseCurrentDirectory(): void {
    if (!browseResult) return;
    const name = pathBase(browseResult.path, browseResult.separator);
    if (browseResult.target === "ssh") {
      setDraft((current) => ({
        ...current,
        ssh_remote_cwd: browseResult.path,
        name: current.name || name || "SSH",
      }));
      return;
    }
    setDraft((current) => ({
      ...current,
      local_cwd: browseResult.path,
      name: current.name || name || "Local",
    }));
  }

  async function submit(): Promise<void> {
    if (!canCreate) {
      setError(draft.target === "local" ? "先选择目录" : "填写 SSH 地址并选择目录");
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
              name: draft.name || pathBase(draft.local_cwd, browseResult?.separator || "/") || "Local",
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
    <div className="fixed inset-0 z-[var(--ao-z-modal)] grid place-items-center bg-[rgba(20,20,22,0.36)] p-4 backdrop-blur-md">
      <section className="grid max-h-[min(760px,calc(100vh-32px))] w-[min(720px,100%)] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden rounded-lg border border-[#e9e7e1] bg-white shadow-[0_18px_48px_rgba(29,29,31,0.16)]">
        <header className="flex items-start justify-between gap-4 border-b border-[#e7e5e0] px-4 pb-3 pt-4">
          <div className="min-w-0">
            <h2 className="m-0 text-[16px] font-bold text-[#1d1d1f]">创建工作区</h2>
            <p className="m-0 mt-0.5 truncate text-[12px] font-semibold text-[var(--ao-muted)]">{hostName}</p>
          </div>
          <button className="grid size-8 place-items-center rounded-lg text-[#98979c] hover:bg-black/[0.035]" type="button" title="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </header>

        <div className="grid min-h-0 gap-4 overflow-auto px-4 py-4">
          <Field label="位置">
            <div className="grid grid-cols-2 gap-2">
              <TargetChoice
                active={draft.target === "local"}
                description="Host 本地目录"
                icon={<FolderGit2 size={17} strokeWidth={1.8} />}
                label="本地"
                onClick={() => setTarget("local")}
              />
              <TargetChoice
                active={draft.target === "ssh"}
                description="Host 连接 SSH"
                icon={<Server size={17} strokeWidth={1.8} />}
                label="SSH"
                onClick={() => setTarget("ssh")}
              />
            </div>
          </Field>

          {draft.target === "ssh" ? (
            <div className="grid gap-3">
              <Field label="SSH 地址">
                <input
                  className="h-10 w-full rounded-lg border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                  placeholder="root@example.com"
                  value={draft.ssh_endpoint}
                  onChange={(event) => setDraft((current) => ({ ...current, ssh_endpoint: event.target.value }))}
                />
              </Field>
              <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-3">
                <Field label="端口">
                  <input
                    className="h-10 w-full rounded-lg border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                    min={1}
                    type="number"
                    value={draft.ssh_port}
                    onChange={(event) => setDraft((current) => ({ ...current, ssh_port: Number(event.target.value) || 22 }))}
                  />
                </Field>
                <Field label="目录">
                  <div className="flex min-w-0 gap-2">
                    <input
                      className="h-10 min-w-0 flex-1 rounded-lg border border-[#e7e5e0] bg-[#f7f6f3] px-3 font-mono text-[13px] outline-none focus:border-[#2563eb]"
                      placeholder="/home/user/project"
                      value={draft.ssh_remote_cwd}
                      onChange={(event) => setDraft((current) => ({ ...current, ssh_remote_cwd: event.target.value }))}
                    />
                    <button
                      className="flex h-10 shrink-0 items-center gap-2 rounded-lg bg-black/[0.055] px-3 text-[13px] font-semibold text-[var(--ao-text)] hover:bg-black/[0.08] disabled:cursor-default disabled:opacity-50"
                      type="button"
                      disabled={!canBrowseSSH || browseLoading}
                      onClick={() => void browse({ target: "ssh", path: draft.ssh_remote_cwd || "/", ssh: sshConfig })}
                    >
                      {browseLoading && draft.target === "ssh" ? <LoaderCircle className="animate-spin" size={15} /> : <RefreshCw size={15} />}
                      浏览
                    </button>
                  </div>
                </Field>
              </div>
            </div>
          ) : null}

          <DirectoryBrowser
            loading={browseLoading}
            result={browseResult}
            selectedPath={selectedPath}
            target={draft.target}
            error={browseError}
            onBrowse={(path) => void browse(draft.target === "ssh" ? { target: "ssh", path, ssh: sshConfig } : { target: "local", path })}
            onChoose={chooseCurrentDirectory}
            canBrowse={draft.target === "local" || canBrowseSSH}
          />

          <Field label="名称">
            <input
              className="h-10 w-full rounded-lg border border-[#e7e5e0] bg-[#f7f6f3] px-3 text-[14px] outline-none focus:border-[#2563eb]"
              placeholder="默认使用目录名"
              value={draft.name}
              onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))}
            />
          </Field>

          {error ? <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-[13px] text-red-600">{error}</div> : null}
        </div>

        <footer className="flex justify-end gap-2 border-t border-[#e7e5e0] px-4 pb-4 pt-3">
          <button className="rounded-lg border border-[#e7e5e0] px-3 py-1.5 text-[13px] font-semibold text-[#1d1d1f] hover:bg-black/[0.035]" type="button" onClick={onClose}>
            取消
          </button>
          <button className="rounded-lg bg-[#2563eb] px-3 py-1.5 text-[13px] font-semibold text-white disabled:opacity-50" type="button" disabled={busy || !canCreate} onClick={() => void submit()}>
            {busy ? "创建中" : "创建工作区"}
          </button>
        </footer>
      </section>
    </div>
  );
}

type DirectoryBrowserProps = {
  canBrowse: boolean;
  error: string;
  loading: boolean;
  result: HostFileSystemBrowseResult | null;
  selectedPath: string;
  target: WorkspaceDraft["target"];
  onBrowse: (path: string) => void;
  onChoose: () => void;
};

function DirectoryBrowser({ canBrowse, error, loading, result, selectedPath, target, onBrowse, onChoose }: DirectoryBrowserProps): React.JSX.Element {
  const roots = result?.roots ?? [];
  return (
    <div className="grid overflow-hidden rounded-lg border border-[#e7e5e0]">
      <div className="flex min-h-10 items-center gap-2 border-b border-[#e7e5e0] bg-[#faf9f6] px-3">
        <button
          className="grid size-7 shrink-0 place-items-center rounded-md text-[var(--ao-muted-strong)] hover:bg-black/[0.055] disabled:cursor-default disabled:opacity-35"
          type="button"
          title="上一级"
          disabled={!canBrowse || loading || !result?.parent_path}
          onClick={() => result?.parent_path && onBrowse(result.parent_path)}
        >
          <ChevronLeft size={16} strokeWidth={1.9} />
        </button>
        <div className="min-w-0 flex-1 truncate font-mono text-[12px] font-semibold text-[#343438]">{result?.path || (target === "ssh" ? "填写 SSH 后浏览" : "加载中")}</div>
        <button
          className="flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-black/[0.055] px-2 text-[12px] font-semibold text-[var(--ao-text)] hover:bg-black/[0.08] disabled:cursor-default disabled:opacity-50"
          type="button"
          disabled={!result || loading}
          onClick={onChoose}
        >
          <Check size={14} strokeWidth={2} />
          选择
        </button>
      </div>
      {roots.length > 0 ? (
        <div className="flex gap-1.5 overflow-x-auto border-b border-[#e7e5e0] px-3 py-2">
          {roots.map((root) => (
            <RootButton key={`${root.id}:${root.path}`} root={root} onClick={() => onBrowse(root.path)} />
          ))}
        </div>
      ) : null}
      <div className="max-h-[260px] min-h-[188px] overflow-auto bg-white">
        {loading ? (
          <div className="grid h-[188px] place-items-center text-[13px] font-semibold text-[var(--ao-muted)]">
            <span className="flex items-center gap-2">
              <LoaderCircle className="animate-spin" size={15} />
              加载中
            </span>
          </div>
        ) : error ? (
          <div className="px-3 py-3 text-[13px] font-semibold text-red-600">{error}</div>
        ) : result ? (
          result.entries.length > 0 ? (
            <div className="grid py-1">
              {result.entries.map((entry) => (
                <DirectoryEntryRow
                  active={entry.path === selectedPath}
                  entry={entry}
                  key={entry.path}
                  onBrowse={onBrowse}
                />
              ))}
            </div>
          ) : (
            <div className="px-3 py-3 text-[13px] font-semibold text-[var(--ao-muted)]">空目录</div>
          )
        ) : (
          <div className="px-3 py-3 text-[13px] font-semibold text-[var(--ao-muted)]">{canBrowse ? "选择一个根目录" : "填写 SSH 地址后浏览"}</div>
        )}
      </div>
    </div>
  );
}

function RootButton({ root, onClick }: { root: HostFileSystemRoot; onClick: () => void }): React.JSX.Element {
  return (
    <button
      className="flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-black/[0.045] px-2 text-[12px] font-semibold text-[#343438] hover:bg-black/[0.075]"
      type="button"
      title={root.path}
      onClick={onClick}
    >
      <HardDrive size={13} strokeWidth={1.9} />
      <span className="max-w-[140px] truncate">{root.label}</span>
    </button>
  );
}

function DirectoryEntryRow({ active, entry, onBrowse }: { active: boolean; entry: HostFileSystemEntry; onBrowse: (path: string) => void }): React.JSX.Element {
  const directory = entry.kind === "dir";
  return (
    <button
      className={`grid min-h-8 grid-cols-[18px_minmax(0,1fr)_80px] items-center gap-2 px-3 text-left text-[13px] transition-colors ${
        directory ? "hover:bg-black/[0.035]" : "cursor-default text-[var(--ao-muted)]"
      } ${active ? "bg-black/[0.055]" : ""}`}
      type="button"
      disabled={!directory}
      title={entry.path}
      onClick={() => directory && onBrowse(entry.path)}
    >
      <Folder className={directory ? "text-[#5f6368]" : "text-[#b5b2ad]"} size={15} strokeWidth={1.8} />
      <span className="min-w-0 truncate font-semibold text-[#343438]">{entry.name}</span>
      <span className="truncate text-right text-[11px] font-medium text-[var(--ao-muted)]">{entry.kind}</span>
    </button>
  );
}

type FieldProps = {
  children: React.ReactNode;
  label: string;
};

function Field({ children, label }: FieldProps): React.JSX.Element {
  return (
    <div className="grid gap-1.5">
      <span className="text-[12px] font-semibold text-[#6b6b70]">{label}</span>
      {children}
    </div>
  );
}

type TargetChoiceProps = {
  active: boolean;
  description: string;
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
};

function TargetChoice({ active, description, icon, label, onClick }: TargetChoiceProps): React.JSX.Element {
  return (
    <button
      className={`flex min-h-[64px] items-center gap-3 rounded-lg border px-3 text-left transition-colors ${
        active ? "border-[#2563eb] bg-[#eff6ff] text-[#1d4ed8]" : "border-[#e7e5e0] bg-[#f7f6f3] text-[#343438] hover:bg-[#f1f0ec]"
      }`}
      type="button"
      onClick={onClick}
    >
      <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-white/70">{icon}</span>
      <span className="min-w-0">
        <span className="block text-[13px] font-bold">{label}</span>
        <span className="block truncate text-[12px] font-semibold opacity-70">{description}</span>
      </span>
    </button>
  );
}

function pathBase(path: string, separator: string): string {
  const trimmed = path.trim();
  if (!trimmed) return "";
  const normalizedSeparator = separator || "/";
  const withoutTrailing = trimmed.endsWith(normalizedSeparator) && trimmed.length > normalizedSeparator.length
    ? trimmed.slice(0, -normalizedSeparator.length)
    : trimmed;
  const parts = withoutTrailing.split(normalizedSeparator).filter(Boolean);
  return parts.at(-1) || withoutTrailing;
}
