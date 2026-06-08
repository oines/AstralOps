import { useState } from "react";
import { Download, File, Loader2, Maximize2 } from "lucide-react";
import { normalizedRecord, type AstralEvent } from "../../types";
import { textValue } from "../../transcriptModel";
import { MediaPreview } from "./MediaPreview";
import type { MediaItem, MediaUrlResolver } from "./mediaTypes";

export function TranscriptMediaBlock({
  align,
  event,
  media,
  mediaUrl,
}: {
  align: "left" | "right";
  event: AstralEvent;
  media: MediaItem;
  mediaUrl?: MediaUrlResolver;
}): React.JSX.Element {
  const [previewOpen, setPreviewOpen] = useState(false);
  const url = mediaUrl?.(event.session_id, event.seq, media.id) || "";
  const downloadUrl = mediaUrl?.(event.session_id, event.seq, media.id, true) || url;
  const pending = media.status === "in_progress" || media.status === "generating";
  const image = media.kind === "image";

  if (!image) {
    return (
      <a
        className="flex max-w-[520px] items-center gap-2 rounded-lg border border-black/10 bg-white px-3 py-2 text-[14px] font-semibold leading-5 text-[#343438] shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition-colors hover:bg-black/[0.03]"
        href={downloadUrl}
      >
        <File size={18} strokeWidth={1.8} />
        <span className="min-w-0 flex-1 truncate">{media.name}</span>
        {media.size ? <span className="shrink-0 text-[12px] font-medium text-[#a0a3a7]">{formatBytes(media.size)}</span> : null}
        <Download size={16} strokeWidth={1.8} />
      </a>
    );
  }

  if (pending || !url) {
    return (
      <div className="flex h-64 w-[min(100%,640px)] items-center justify-center rounded-[8px] border border-black/10 bg-black/[0.035] text-[#73777c]">
        <div className="flex items-center gap-2 text-[14px] font-semibold">
          <Loader2 className="animate-spin" size={18} strokeWidth={1.9} />
          <span>正在生成图片</span>
        </div>
      </div>
    );
  }

  return (
    <div className={`group/media grid max-w-[min(100%,640px)] gap-2 ${align === "right" ? "justify-items-end" : "justify-items-start"}`}>
      {media.revisedPrompt ? <div className="max-w-[640px] text-[13px] font-medium leading-5 text-[#8a8d91]">{media.revisedPrompt}</div> : null}
      <button
        className="overflow-hidden rounded-[8px] border border-black/5 bg-black/[0.03] text-left shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition-[transform,box-shadow] hover:-translate-y-px hover:shadow-[0_8px_22px_rgba(0,0,0,0.08)]"
        type="button"
        aria-label="预览图片"
        onClick={() => setPreviewOpen(true)}
      >
        <img alt={media.name} className="block max-h-[520px] w-full max-w-[640px] object-contain" src={url} />
      </button>
      <div className="flex items-center gap-2 text-[#8a8d91] opacity-0 transition-opacity group-hover/media:opacity-100">
        <button className="grid size-7 place-items-center rounded-md hover:bg-black/[0.05]" type="button" aria-label="预览" title="预览" onClick={() => setPreviewOpen(true)}>
          <Maximize2 size={16} strokeWidth={1.8} />
        </button>
        <a className="grid size-7 place-items-center rounded-md hover:bg-black/[0.05]" href={downloadUrl} aria-label="下载" title="下载">
          <Download size={16} strokeWidth={1.8} />
        </a>
      </div>
      {previewOpen ? <MediaPreview media={media} url={url} downloadUrl={downloadUrl} onClose={() => setPreviewOpen(false)} /> : null}
    </div>
  );
}

export function attachmentsFromEvent(event: AstralEvent): MediaItem[] {
  const value = normalizedRecord(event);
  const raw = Array.isArray(value.attachments) ? value.attachments : [];
  return raw.map((item) => mediaFromRecord(item as Record<string, unknown>)).filter((item): item is MediaItem => Boolean(item));
}

export function mediaFromEvent(event: AstralEvent): MediaItem | null {
  return mediaFromRecord(normalizedRecord(event));
}

function mediaFromRecord(value: Record<string, unknown>): MediaItem | null {
  const id = textValue(value, "media_id") || textValue(value, "id") || textValue(value, "item_id");
  const path = textValue(value, "path") || textValue(value, "saved_path");
  if (!id) return null;
  const name = textValue(value, "name") || fileName(path) || id;
  return {
    id,
    kind: textValue(value, "kind") || (textValue(value, "mime_type").startsWith("image/") ? "image" : "file"),
    path,
    name,
    mimeType: textValue(value, "mime_type"),
    size: numberValue(value.size),
    status: textValue(value, "status"),
    revisedPrompt: textValue(value, "revised_prompt"),
  };
}

function fileName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path;
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let value = size;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}
