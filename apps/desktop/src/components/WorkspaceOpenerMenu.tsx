import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, Code2, Folder, TerminalSquare } from "lucide-react";
import type { Workspace } from "../types";

type WorkspaceOpenerMenuProps = {
  rightPanelOpen: boolean;
  workspace: Workspace | null;
  onError: (message: string) => void;
};

const DEFAULT_OPENERS: WorkspaceOpenerInfo[] = [
  { id: "vscode", label: "VS Code", available: true },
  { id: "finder", label: "Finder", available: true },
  { id: "terminal", label: "Terminal", available: true },
];

export function WorkspaceOpenerMenu({ rightPanelOpen, workspace, onError }: WorkspaceOpenerMenuProps): React.JSX.Element {
  const [openers, setOpeners] = useState<WorkspaceOpenerInfo[]>(DEFAULT_OPENERS);
  const [selectedId, setSelectedId] = useState<WorkspaceOpenerId>("vscode");
  const [menuOpen, setMenuOpen] = useState(false);
  const [openingId, setOpeningId] = useState<WorkspaceOpenerId | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    void window.astral.getWorkspaceOpeners()
      .then((items) => {
        if (!cancelled && items.length > 0) setOpeners(items);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!menuOpen) return;
    function handlePointerDown(event: PointerEvent): void {
      if (rootRef.current?.contains(event.target as Node)) return;
      setMenuOpen(false);
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") setMenuOpen(false);
    }
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [menuOpen]);

  const selected = useMemo(
    () => openers.find((opener) => opener.id === selectedId) ?? openers[0] ?? DEFAULT_OPENERS[0],
    [openers, selectedId],
  );
  const disabled = !workspace;
  const selectedDisabledReason = workspace ? disabledReasonForOpener(selected, workspace) : "No workspace selected";

  const openWorkspace = useCallback(
    async (opener: WorkspaceOpenerInfo) => {
      if (!workspace) return;
      const reason = disabledReasonForOpener(opener, workspace);
      if (reason) {
        onError(reason);
        return;
      }
      setOpeningId(opener.id);
      onError("");
      try {
        const result = await window.astral.openWorkspace(opener.id, workspace);
        if (!result.ok) {
          onError(result.error || `Could not open workspace in ${opener.label}`);
          return;
        }
        setSelectedId(opener.id);
        setMenuOpen(false);
      } catch (openError) {
        onError(openError instanceof Error ? openError.message : String(openError));
      } finally {
        setOpeningId(null);
      }
    },
    [onError, workspace],
  );

  return (
    <div
      ref={rootRef}
      className="workspace-opener-menu [-webkit-app-region:no-drag] absolute top-[10px] z-[var(--ao-z-chrome-menu)] transition-[right] duration-180 ease-out"
      style={{ right: rightPanelOpen ? "calc(var(--astral-right-panel-width, 420px) + 10px)" : 64 }}
    >
      <div className={`flex h-8 overflow-hidden rounded-xl border border-black/10 bg-white/90 shadow-[0_1px_2px_rgba(0,0,0,0.06)] backdrop-blur ${disabled ? "opacity-45" : ""}`}>
        <button
          aria-label={`Open workspace in ${selected.label}`}
          className="grid h-8 w-11 place-items-center transition-[background-color,transform] duration-150 ease-out hover:bg-black/[0.045] active:scale-95 disabled:pointer-events-none"
          disabled={disabled || Boolean(selectedDisabledReason) || openingId !== null}
          title={selectedDisabledReason || `Open workspace in ${selected.label}`}
          type="button"
          onClick={() => void openWorkspace(selected)}
          onMouseDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
        >
          <OpenerIcon opener={selected} size={20} />
        </button>
        <div className="h-8 w-px bg-black/5" />
        <button
          aria-expanded={menuOpen}
          aria-label="Choose workspace opener"
          className="grid h-8 w-9 place-items-center text-[#767a80] transition-[background-color,color,transform] duration-150 ease-out hover:bg-black/[0.045] hover:text-[#343438] active:scale-95 disabled:pointer-events-none"
          disabled={disabled}
          title={disabled ? "No workspace selected" : "Choose workspace opener"}
          type="button"
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            setMenuOpen((current) => !current);
          }}
          onMouseDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
        >
          <ChevronDown className={`transition-transform duration-150 ease-out ${menuOpen ? "rotate-180" : ""}`} size={16} strokeWidth={2} />
        </button>
      </div>

      {menuOpen ? (
        <div className="absolute right-0 top-[42px] w-[238px] rounded-[18px] border border-black/10 bg-white/95 p-2 shadow-[0_18px_55px_rgba(0,0,0,0.16)] backdrop-blur">
          {openers.map((opener) => {
            const reason = workspace ? disabledReasonForOpener(opener, workspace) : "No workspace selected";
            const rowDisabled = Boolean(reason) || openingId !== null;
            return (
              <button
                key={opener.id}
                className={`flex h-12 w-full items-center gap-3 rounded-xl px-3 text-left text-[15px] font-medium transition-colors duration-150 ${
                  rowDisabled ? "cursor-not-allowed text-[#b3b1ad]" : "text-[#1f2227] hover:bg-black/[0.045]"
                }`}
                disabled={rowDisabled}
                title={reason || `Open workspace in ${opener.label}`}
                type="button"
                onClick={() => void openWorkspace(opener)}
              >
                <OpenerIcon opener={opener} size={22} muted={rowDisabled} />
                <span className="truncate">{opener.label}</span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function disabledReasonForOpener(opener: WorkspaceOpenerInfo, workspace: Workspace): string {
  if (!opener.available) return opener.disabled_reason || `${opener.label} is unavailable`;
  if (opener.id === "finder" && workspace.target === "ssh") return "Finder cannot open SSH workspaces";
  return "";
}

function OpenerIcon({ muted = false, opener, size }: { muted?: boolean; opener: WorkspaceOpenerInfo; size: number }): React.JSX.Element {
  if (opener.icon_data_url) {
    return <img alt="" className={`shrink-0 ${muted ? "opacity-45 grayscale" : ""}`} height={size} src={opener.icon_data_url} width={size} />;
  }
  const className = `shrink-0 ${muted ? "text-[#b3b1ad]" : "text-[#5f6368]"}`;
  if (opener.id === "finder") return <Folder className={className} size={size} strokeWidth={1.8} />;
  if (opener.id === "terminal") return <TerminalSquare className={className} size={size} strokeWidth={1.8} />;
  return <Code2 className={className} size={size} strokeWidth={1.9} />;
}
