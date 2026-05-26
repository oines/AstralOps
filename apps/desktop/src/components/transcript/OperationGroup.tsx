import React, { useEffect, useMemo, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ChevronRight, Copy, FileCode2, TerminalSquare } from "lucide-react";
import type { AstralEvent } from "../../types";
import {
  buildCommandItems,
  type CommandItem,
  type FileDiff,
  type TranscriptOperationGroup,
  type TranscriptOperationStep,
  type TurnGroup,
} from "../../transcriptModel";

type OperationGroupProps = {
  group: TranscriptOperationGroup;
  renderDetail: (event: AstralEvent) => React.ReactNode;
  turnStatus: TurnGroup["status"];
};

export function OperationGroup({ group, renderDetail, turnStatus }: OperationGroupProps): React.JSX.Element | null {
  const [open, setOpen] = useState(turnStatus === "running");

  useEffect(() => {
    setOpen(turnStatus === "running");
  }, [turnStatus]);

  if (group.steps.length === 0 || group.summary === "") return null;

  return (
    <div className="min-w-0">
      <button
        className="flex min-w-0 max-w-full items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#a0a3a7] transition-colors duration-150 ease-out hover:text-[#777b80]"
        type="button"
        onClick={() => setOpen((current) => !current)}
      >
        <TerminalSquare className="shrink-0" size={15} strokeWidth={1.8} />
        <span className="min-w-0 truncate">{group.summary}</span>
        <ChevronRight className={`shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={15} strokeWidth={2} />
      </button>
      <AnimatePresence initial={false}>
        {open ? (
          <motion.div
            animate={{ height: "auto", opacity: 1 }}
            className="min-w-0 overflow-hidden"
            exit={{ height: 0, opacity: 0 }}
            initial={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
          >
            <div className="mt-1.5 grid min-w-0 gap-2">
              {group.steps.map((step) => renderOperationStep(step, turnStatus, renderDetail))}
            </div>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}

function renderOperationStep(
  step: TranscriptOperationStep,
  turnStatus: TurnGroup["status"],
  renderDetail: (event: AstralEvent) => React.ReactNode,
): React.ReactNode {
  if (step.type === "command") return <CommandList events={step.events} key={step.id} />;
  if (step.type === "fileChanges") return <FileChangesGroup files={step.files} key={step.id} />;
  return <React.Fragment key={step.id}>{renderDetail(step.event)}</React.Fragment>;
}

function FileChangesGroup({ files }: { files: FileDiff[] }): React.JSX.Element | null {
  const [open, setOpen] = useState(false);
  if (files.length === 0) return null;
  const summary = files.length === 1 ? files[0].name : `${files.length} 个文件`;

  return (
    <div className="min-w-0">
      <button
        className="flex min-w-0 max-w-full items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#a0a3a7] transition-colors duration-150 ease-out hover:text-[#777b80]"
        type="button"
        onClick={() => setOpen((current) => !current)}
      >
        <FileCode2 className="shrink-0" size={15} strokeWidth={1.8} />
        <span className="shrink-0">已编辑的文件</span>
        <span className="min-w-0 truncate">{summary}</span>
        <ChevronRight className={`shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={15} strokeWidth={2} />
      </button>
      {open ? (
        <div className="mt-2 grid min-w-0 gap-3">
          {files.map((file) => (
            <DiffCard file={file} key={`${file.path}:${file.diff}`} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function DiffCard({ file }: { file: FileDiff }): React.JSX.Element {
  const rows = useMemo(() => diffRows(file.diff), [file.diff]);
  return (
    <div className="group min-w-0 overflow-hidden rounded-[8px] border border-black/10 bg-white shadow-[0_1px_2px_rgba(0,0,0,0.04)]">
      <div className="flex min-w-0 items-center gap-2 border-b border-black/5 bg-[#fbfbfc] px-3 py-2.5 font-mono text-[13px] leading-5">
        <span className="min-w-0 truncate text-[#5f6368]">{file.name}</span>
        <span className="rounded bg-[#e9f8ed] px-1.5 text-[#008a2e]">+{file.additions}</span>
        <span className="rounded bg-[#fdecec] px-1.5 text-[#b91c1c]">-{file.deletions}</span>
        <button
          className="ml-auto grid size-7 shrink-0 place-items-center rounded-md text-[#8a8d91] opacity-0 transition hover:bg-black/[0.05] group-hover:opacity-100"
          type="button"
          aria-label="复制 diff"
          onClick={() => void navigator.clipboard?.writeText(file.diff)}
        >
          <Copy size={15} strokeWidth={1.8} />
        </button>
      </div>
      <div className="max-h-96 min-w-0 overflow-auto bg-white py-2 font-mono text-[12px] leading-5 text-[#343438]">
        {rows.map((row, index) => (
          <DiffRowView key={`${index}-${row.kind}-${row.text}`} row={row} />
        ))}
      </div>
    </div>
  );
}

type DiffRow = {
  kind: "added" | "removed" | "context" | "meta";
  lineNumber?: number;
  text: string;
};

function DiffRowView({ row }: { row: DiffRow }): React.JSX.Element {
  if (row.kind === "meta") {
    return (
      <div className="grid min-w-max grid-cols-[4px_68px_minmax(0,1fr)] text-[#a0a3a7]">
        <span />
        <span className="border-r border-black/[0.06]" />
        <span className="whitespace-pre px-3">{row.text || " "}</span>
      </div>
    );
  }

  const added = row.kind === "added";
  const removed = row.kind === "removed";
  const lineClass = added ? "bg-[#eaf8ee] text-[#006f2a]" : removed ? "bg-[#fff0f0] text-[#a61b1b]" : "text-[#5f6368]";
  const barClass = added ? "bg-[#0a9f42]" : removed ? "bg-[#dc2626]" : "bg-transparent";

  return (
    <div className={`grid min-w-max grid-cols-[4px_68px_minmax(0,1fr)] ${lineClass}`}>
      <span className={barClass} />
      <span className="select-none border-r border-black/[0.06] pr-3 text-right text-[#aeb1b7]">{row.lineNumber ?? ""}</span>
      <span className="whitespace-pre px-3">{row.text || " "}</span>
    </div>
  );
}

function diffRows(diff: string): DiffRow[] {
  const rows: DiffRow[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;

  for (const line of diff.split("\n")) {
    if (isHiddenDiffMetaLine(line)) continue;
    const hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[3]);
      inHunk = true;
      continue;
    }
    if (!inHunk) {
      rows.push({ kind: "meta", text: line });
      continue;
    }
    if (line.startsWith("+")) {
      rows.push({ kind: "added", lineNumber: newLine, text: line.slice(1) });
      newLine += 1;
      continue;
    }
    if (line.startsWith("-")) {
      rows.push({ kind: "removed", lineNumber: oldLine, text: line.slice(1) });
      oldLine += 1;
      continue;
    }
    if (line.startsWith("\\")) {
      rows.push({ kind: "meta", text: line });
      continue;
    }
    rows.push({ kind: "context", lineNumber: newLine, text: line.startsWith(" ") ? line.slice(1) : line });
    oldLine += 1;
    newLine += 1;
  }

  return rows.length > 0 ? rows : [{ kind: "meta", text: diff }];
}

function isHiddenDiffMetaLine(line: string): boolean {
  return (
    line.startsWith("diff --git ") ||
    line.startsWith("index ") ||
    line.startsWith("new file mode ") ||
    line.startsWith("deleted file mode ") ||
    line.startsWith("--- ") ||
    line.startsWith("+++ ")
  );
}

function CommandList({ events }: { events: AstralEvent[] }): React.JSX.Element | null {
  const items = useMemo(() => buildCommandItems(events), [events]);

  if (items.length === 0) return null;

  return (
    <div className="grid min-w-0 gap-1">
      {items.map((item) => (
        <CommandRow item={item} key={item.key} />
      ))}
    </div>
  );
}

function CommandRow({ item }: { item: CommandItem }): React.JSX.Element {
  const [open, setOpen] = useState(false);
  const hasOutput = item.output.trim() !== "";
  const outputPreview = item.output.length > 12000 ? item.output.slice(-12000) : item.output;
  const outputClipped = outputPreview.length !== item.output.length;
  return (
    <div className="grid min-w-0 gap-2">
      <button
        className="flex min-w-0 items-center gap-2 text-left text-[13px] font-medium leading-6 text-[#6f7378] transition-colors duration-150 ease-out hover:text-[#343438]"
        type="button"
        onClick={() => hasOutput && setOpen((current) => !current)}
      >
        <span className="shrink-0">{item.status === "running" ? "正在运行" : "已运行"}</span>
        <span className="min-w-0 truncate font-mono text-[13px]">{item.command}</span>
        {hasOutput ? <ChevronRight className={`ml-auto shrink-0 transition-transform duration-150 ease-out ${open ? "rotate-90" : ""}`} size={15} strokeWidth={2} /> : null}
      </button>
      {open && hasOutput ? (
        <div className="min-w-0 rounded-[12px] bg-black/5 px-3 py-2 text-[#5f6368]">
          <div className="mb-1.5 text-[13px] font-medium">Shell</div>
          {outputClipped ? <div className="mb-2 text-[12px] font-semibold text-[#a0a3a7]">已显示最新 12000 个字符</div> : null}
          <pre className="max-h-72 min-w-0 overflow-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-5 [overflow-wrap:anywhere]">{outputPreview}</pre>
          {item.status === "completed" ? <div className="mt-2 text-right text-[13px] font-semibold text-[#8a8d91]">成功</div> : null}
        </div>
      ) : null}
    </div>
  );
}
