import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Download, Minus, Plus, X } from "lucide-react";
import type { MediaItem } from "./mediaTypes";

type MediaPreviewProps = {
  downloadUrl: string;
  media: MediaItem;
  onClose: () => void;
  url: string;
};

export function MediaPreview({ downloadUrl, media, onClose, url }: MediaPreviewProps): React.ReactPortal {
  const minZoom = 1;
  const maxZoom = 5;
  const imageRef = useRef<HTMLImageElement | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);
  const [baseScale, setBaseScale] = useState(1);
  const [dragging, setDragging] = useState(false);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [zoom, setZoom] = useState(minZoom);
  const panRef = useRef(pan);
  const zoomRef = useRef(zoom);

  const setPanValue = useCallback((next: { x: number; y: number }) => {
    panRef.current = next;
    setPan(next);
  }, []);

  const setZoomAt = useCallback(
    (nextZoom: number, origin?: { clientX: number; clientY: number }) => {
      const currentZoom = zoomRef.current;
      const clampedZoom = clamp(nextZoom, minZoom, maxZoom);
      if (Math.abs(clampedZoom - currentZoom) < 0.001) return;
      let nextPan = panRef.current;
      const viewport = viewportRef.current;
      if (origin && viewport) {
        const rect = viewport.getBoundingClientRect();
        const originX = origin.clientX - rect.left - rect.width / 2;
        const originY = origin.clientY - rect.top - rect.height / 2;
        const ratio = clampedZoom / currentZoom;
        nextPan = {
          x: originX - (originX - panRef.current.x) * ratio,
          y: originY - (originY - panRef.current.y) * ratio,
        };
      }
      if (clampedZoom === minZoom) nextPan = { x: 0, y: 0 };
      zoomRef.current = clampedZoom;
      panRef.current = nextPan;
      setZoom(clampedZoom);
      setPan(nextPan);
    },
    [maxZoom, minZoom],
  );

  const resetView = useCallback(() => {
    zoomRef.current = minZoom;
    panRef.current = { x: 0, y: 0 };
    setZoom(minZoom);
    setPan({ x: 0, y: 0 });
  }, [minZoom]);

  const measureBaseScale = useCallback(() => {
    const image = imageRef.current;
    if (!image || image.naturalWidth <= 0) return;
    setBaseScale(image.clientWidth / image.naturalWidth);
  }, []);

  useEffect(() => {
    function close(event: KeyboardEvent): void {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [onClose]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return undefined;
    function handleWheel(event: WheelEvent): void {
      if (event.ctrlKey || event.metaKey) {
        event.preventDefault();
        const factor = Math.exp(-event.deltaY * 0.01);
        setZoomAt(zoomRef.current * factor, { clientX: event.clientX, clientY: event.clientY });
        return;
      }
      if (zoomRef.current > minZoom) {
        event.preventDefault();
        setPanValue({
          x: panRef.current.x - event.deltaX,
          y: panRef.current.y - event.deltaY,
        });
      }
    }
    viewport.addEventListener("wheel", handleWheel, { passive: false });
    return () => viewport.removeEventListener("wheel", handleWheel);
  }, [minZoom, setPanValue, setZoomAt]);

  useEffect(() => {
    const image = imageRef.current;
    if (!image) return undefined;
    measureBaseScale();
    const observer = new ResizeObserver(measureBaseScale);
    observer.observe(image);
    window.addEventListener("resize", measureBaseScale);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measureBaseScale);
    };
  }, [measureBaseScale]);

  function handleViewportPointerDown(event: React.PointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) return;
    if (event.target === event.currentTarget) {
      event.preventDefault();
      onClose();
      return;
    }
    if (zoomRef.current <= minZoom) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: panRef.current.x,
      originY: panRef.current.y,
    };
    setDragging(true);
  }

  function handleViewportPointerMove(event: React.PointerEvent<HTMLDivElement>): void {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setPanValue({
      x: drag.originX + event.clientX - drag.startX,
      y: drag.originY + event.clientY - drag.startY,
    });
  }

  function handleViewportPointerEnd(event: React.PointerEvent<HTMLDivElement>): void {
    if (dragRef.current?.pointerId === event.pointerId) {
      dragRef.current = null;
      setDragging(false);
    }
  }

  function handleViewportPointerCancel(event: React.PointerEvent<HTMLDivElement>): void {
    if (dragRef.current?.pointerId === event.pointerId) {
      dragRef.current = null;
      setDragging(false);
    }
  }

  function handleDoubleClick(event: React.MouseEvent<HTMLDivElement>): void {
    if (zoomRef.current > minZoom) resetView();
    else setZoomAt(2, { clientX: event.clientX, clientY: event.clientY });
  }

  const zoomPercent = `${Math.max(1, Math.round(baseScale * zoom * 100))}%`;
  const imageCursor = zoom > minZoom ? (dragging ? "grabbing" : "grab") : "zoom-in";

  return createPortal(
    <div className="[-webkit-app-region:no-drag] fixed inset-0 z-[var(--ao-z-modal)] overflow-hidden bg-black/78 backdrop-blur-[2px]" role="dialog" aria-modal="true">
      <div className="[-webkit-app-region:no-drag] absolute right-3 top-3 z-30 flex items-center gap-1.5" onPointerDown={(event) => event.stopPropagation()}>
        <a className="[-webkit-app-region:no-drag] grid size-8 place-items-center rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] text-[var(--ao-text)] shadow-[var(--ao-shadow-panel)] transition-colors hover:bg-[var(--ao-panel)]" href={downloadUrl} aria-label="下载" title="下载" onClick={(event) => event.stopPropagation()}>
          <Download size={16} strokeWidth={1.9} />
        </a>
        <button
          className="[-webkit-app-region:no-drag] grid size-8 place-items-center rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] text-[var(--ao-text)] shadow-[var(--ao-shadow-panel)] transition-colors hover:bg-[var(--ao-panel)]"
          type="button"
          aria-label="关闭"
          title="关闭"
          onPointerDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
            onClose();
          }}
        >
          <X size={17} strokeWidth={1.9} />
        </button>
      </div>
      <div
        className="[-webkit-app-region:no-drag] absolute inset-0 z-20 grid place-items-center overflow-hidden px-8 py-16"
        ref={viewportRef}
        style={{ touchAction: "none" }}
        onDoubleClick={handleDoubleClick}
        onPointerCancel={handleViewportPointerCancel}
        onPointerDown={handleViewportPointerDown}
        onPointerMove={handleViewportPointerMove}
        onPointerUp={handleViewportPointerEnd}
      >
        <img
          alt={media.name}
          className="max-h-[calc(100vh-128px)] max-w-[calc(100vw-64px)] select-none rounded-lg bg-[var(--ao-bg)] object-contain shadow-[0_18px_56px_rgba(0,0,0,0.36)] will-change-transform"
          draggable={false}
          ref={imageRef}
          src={url}
          onLoad={measureBaseScale}
          style={{
            cursor: imageCursor,
            transform: `translate3d(${pan.x}px, ${pan.y}px, 0) scale(${zoom})`,
            transition: dragging ? "none" : "transform 120ms ease-out",
          }}
        />
      </div>
      <div className="[-webkit-app-region:no-drag] absolute bottom-5 left-1/2 z-30 flex -translate-x-1/2 items-center gap-1 rounded-lg border border-[var(--ao-border)] bg-[var(--ao-bg)] px-1 py-1 text-[var(--ao-text)] shadow-[var(--ao-shadow-panel)]" onPointerDown={(event) => event.stopPropagation()}>
        <button
          className="grid size-8 place-items-center rounded-lg bg-[var(--ao-panel)] text-[var(--ao-text)] transition-colors hover:bg-[var(--ao-panel-strong)] disabled:text-[var(--ao-subtle)]"
          type="button"
          aria-label="缩小"
          title="缩小"
          disabled={zoom <= minZoom}
          onClick={() => setZoomAt(zoomRef.current - 0.25)}
        >
          <Minus size={16} strokeWidth={1.9} />
        </button>
        <div className="min-w-14 text-center text-[13px] font-semibold leading-8">{zoomPercent}</div>
        <button
          className="grid size-8 place-items-center rounded-lg bg-[var(--ao-panel)] text-[var(--ao-text)] transition-colors hover:bg-[var(--ao-panel-strong)] disabled:text-[var(--ao-subtle)]"
          type="button"
          aria-label="放大"
          title="放大"
          disabled={zoom >= maxZoom}
          onClick={() => setZoomAt(zoomRef.current + 0.25)}
        >
          <Plus size={16} strokeWidth={1.9} />
        </button>
      </div>
    </div>,
    document.body,
  );
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min;
  return Math.min(max, Math.max(min, value));
}
