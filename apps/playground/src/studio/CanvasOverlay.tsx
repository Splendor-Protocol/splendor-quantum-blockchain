import { useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type PointerEvent } from 'react';
import { isWithin, pickDropTarget } from '../preview/dropTarget';
import type { PreviewDropTarget, PreviewNodeRect, PreviewRect } from '../preview/protocol';
import { useCanvasHover, useCanvasRects, type CanvasStore } from './canvasStore';

export const CONTRACT_FN_MIME = 'application/x-splendor-contract-fn';

export type CanvasDrop = {
  parentId: string;
  /** Insertion slot among the parent's children, before any move is applied. */
  index: number;
  /** Set when an existing node is being moved with the selection handle. */
  moveId: string | null;
  /** Raw contract-function MIME payload, when a contract function was dragged in. */
  contractFn?: string;
};

export type CanvasOverlayProps = {
  /**
   * Rects (null until the frame has rendered) and the node under the pointer inside the frame,
   * as the preview frame reports them.
   */
  store: CanvasStore;
  selectedId: string | null;
  labelFor?: (id: string, kind: string) => string;
  /** Decides whether a drag that started in the studio (palette, block list…) can be dropped on the canvas. */
  acceptsDrag: (dataTransfer: DataTransfer | null) => boolean;
  /** Adding another block would exceed the page limit. */
  full?: boolean;
  onSelect: (id: string) => void;
  onDuplicate: (id: string) => void;
  onRemove: (id: string) => void;
  onDrop: (drop: CanvasDrop) => void;
};

const toStyle = (r: PreviewRect): CSSProperties => ({ top: r.top, left: r.left, width: r.width, height: r.height });

function defaultLabel(_id: string, kind: string): string {
  return kind === 'block' ? 'Block' : kind.charAt(0).toUpperCase() + kind.slice(1);
}

/**
 * Sits on top of the preview iframe. It never blocks the page (the frame keeps the pointer for
 * clicking widgets and reporting hover) except while a drag is in flight, when it catches the drag
 * itself and shows the insertion points the frame reported.
 */
export function CanvasOverlay({ store, selectedId, labelFor = defaultLabel, acceptsDrag, full = false, onSelect, onDuplicate, onRemove, onDrop }: CanvasOverlayProps) {
  const rects = useCanvasRects(store);
  const hoverId = useCanvasHover(store);
  const root = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);
  const [movingId, setMovingId] = useState<string | null>(null);
  const [active, setActive] = useState<PreviewDropTarget | null>(null);

  const nodes = rects?.nodes ?? [];
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);

  // A drag started anywhere in the studio: take over the pointer until it ends.
  useEffect(() => {
    const onStart = (e: globalThis.DragEvent) => {
      if (acceptsDrag(e.dataTransfer)) setDragging(true);
    };
    const onEnd = () => {
      setDragging(false);
      setMovingId(null);
      setActive(null);
    };
    document.addEventListener('dragstart', onStart);
    document.addEventListener('dragend', onEnd);
    document.addEventListener('drop', onEnd);
    return () => {
      document.removeEventListener('dragstart', onStart);
      document.removeEventListener('dragend', onEnd);
      document.removeEventListener('drop', onEnd);
    };
  }, [acceptsDrag]);

  const pick = (e: { clientX: number; clientY: number }): PreviewDropTarget | null => {
    if (!rects || !root.current) return null;
    const box = root.current.getBoundingClientRect();
    return pickDropTarget(rects, e.clientX - box.left, e.clientY - box.top, movingId);
  };

  // Touch and pen: HTML5 drag does not start from a finger, so the handle follows the pointer.
  const pointerMove = useRef<{ pointerId: number; id: string } | null>(null);
  /** The last insertion point under the finger; read at pointerup, before a re-render could catch up. */
  const activeRef = useRef<PreviewDropTarget | null>(null);
  const onHandlePointerDown = (e: PointerEvent<HTMLElement>, id: string) => {
    if (e.pointerType === 'mouse') return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    pointerMove.current = { pointerId: e.pointerId, id };
    activeRef.current = null;
    setMovingId(id);
    setDragging(true);
  };
  const onHandlePointerMove = (e: PointerEvent<HTMLElement>) => {
    if (pointerMove.current?.pointerId !== e.pointerId) return;
    const next = pick(e);
    activeRef.current = next;
    setActive((prev) => (prev?.parentId === next?.parentId && prev?.index === next?.index ? prev : next));
  };
  /** Pointer up drops; cancel or a lost capture (the handle went away) just ends the move. */
  const onHandlePointerEnd = (e: PointerEvent<HTMLElement>, drop: boolean) => {
    const move = pointerMove.current;
    if (move?.pointerId !== e.pointerId) return;
    pointerMove.current = null;
    const target = drop ? (activeRef.current ?? pick(e)) : null;
    activeRef.current = null;
    setDragging(false);
    setMovingId(null);
    setActive(null);
    if (target) onDrop({ parentId: target.parentId, index: target.index, moveId: move.id });
  };

  // If the handle goes away mid-gesture (the frame reloaded, the node moved), its own events never
  // arrive: end the move when the finger lifts anywhere, so the overlay stops catching the pointer.
  useEffect(() => {
    if (!dragging || !pointerMove.current) return;
    const end = () => {
      if (!pointerMove.current) return;
      pointerMove.current = null;
      activeRef.current = null;
      setDragging(false);
      setMovingId(null);
      setActive(null);
    };
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
    return () => {
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
    };
  }, [dragging]);

  const selected = selectedId ? byId.get(selectedId) : undefined;
  const hovered = hoverId && hoverId !== selectedId && !dragging ? byId.get(hoverId) : undefined;

  const chain = useMemo(() => {
    const out: PreviewNodeRect[] = [];
    let cur: PreviewNodeRect | undefined = selected;
    while (cur && out.length < 12) {
      out.unshift(cur);
      cur = cur.parentId ? byId.get(cur.parentId) : undefined;
    }
    return out;
  }, [selected, byId]);

  const canEdit = Boolean(selected && selected.parentId);
  const barBelow = selected ? selected.rect.top < 30 : false;

  const startMove = (e: DragEvent, id: string) => {
    // A long press during a finger move can also start a native drag; the pointer move owns it.
    if (pointerMove.current) {
      e.preventDefault();
      return;
    }
    e.dataTransfer.setData('text/plain', id);
    e.dataTransfer.effectAllowed = 'move';
    setMovingId(id);
    setDragging(true);
  };

  const zones = dragging && rects ? rects.dropTargets.filter((t) => !movingId || !isWithin(rects, t.parentId, movingId)) : [];
  const activeParent = active ? byId.get(active.parentId) : undefined;

  return (
    <div
      ref={root}
      className={`pg-canvas-overlay${dragging ? ' is-dragging' : ''}`}
      data-testid="canvas-overlay"
      onDragOver={(e) => {
        if (!dragging) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = e.dataTransfer.effectAllowed.startsWith('copy') ? 'copy' : 'move';
        const next = pick(e);
        setActive((prev) => (prev?.parentId === next?.parentId && prev?.index === next?.index ? prev : next));
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setActive(null);
      }}
      onDrop={(e) => {
        if (!dragging) return;
        e.preventDefault();
        const target = active ?? pick(e);
        const raw = e.dataTransfer.getData(CONTRACT_FN_MIME);
        const moveId = movingId;
        setDragging(false);
        setMovingId(null);
        setActive(null);
        if (target) onDrop({ parentId: target.parentId, index: target.index, moveId, ...(raw ? { contractFn: raw } : {}) });
      }}
    >
      {hovered ? (
        <div className="pg-hover-frame" style={toStyle(hovered.rect)} data-testid="canvas-hover">
          <span className="pg-hover-label">{labelFor(hovered.id, hovered.kind)}</span>
        </div>
      ) : null}

      {/* Stays mounted while dragging: removing the drag handle mid-drag would cancel the drag. */}
      {selected ? (
        <div className={`pg-selection-frame${dragging ? ' is-dimmed' : ''}`} style={toStyle(selected.rect)} data-testid="canvas-selection" data-node-id={selected.id}>
          <div className={`pg-selection-bar${barBelow ? ' is-below' : ''}`} role="toolbar"
            aria-label={`${labelFor(selected.id, selected.kind)} actions`}
            onKeyDown={(e) => {
              // Delete removes the selection from the keyboard; no confirmation needed, Undo restores it.
              if ((e.key === 'Delete' || e.key === 'Backspace') && canEdit && !e.defaultPrevented) {
                e.preventDefault();
                onRemove(selected.id);
              }
            }}
          >
            <span className="pg-selection-label">{labelFor(selected.id, selected.kind)}</span>
            {selected.parentId ? (
              <button type="button" className="pg-selection-btn" aria-label="Select parent" title="Select parent" onClick={() => onSelect(selected.parentId!)}>
                ↑
              </button>
            ) : null}
            {canEdit ? (
              <>
                <span
                  className="pg-selection-btn pg-selection-handle"
                  draggable
                  aria-hidden="true"
                  title="Drag to move"
                  data-testid="canvas-move-handle"
                  onDragStart={(e) => startMove(e, selected.id)}
                  onPointerDown={(e) => onHandlePointerDown(e, selected.id)}
                  onPointerMove={onHandlePointerMove}
                  onPointerUp={(e) => onHandlePointerEnd(e, true)}
                  onPointerCancel={(e) => onHandlePointerEnd(e, false)}
                  onLostPointerCapture={(e) => onHandlePointerEnd(e, false)}
                >
                  ⠿
                </span>
                <button type="button" className="pg-selection-btn" aria-label="Duplicate" title="Duplicate" disabled={full} onClick={() => onDuplicate(selected.id)} data-testid="canvas-duplicate">
                  ⧉
                </button>
                <button type="button" className="pg-selection-btn is-danger" aria-label="Delete" aria-keyshortcuts="Delete" title="Delete (Del)" onClick={() => onRemove(selected.id)} data-testid="canvas-delete">
                  ×
                </button>
              </>
            ) : null}
          </div>
        </div>
      ) : null}

      {dragging ? (
        <>
          {activeParent ? <div className="pg-drop-parent" style={toStyle(activeParent.rect)} /> : null}
          {zones.map((t) => {
            const on = active?.parentId === t.parentId && active.index === t.index;
            return <div key={`${t.parentId}:${t.index}`} className={`pg-drop-zone${on ? ' is-active' : ''}`} style={toStyle(t.rect)} data-testid={on ? 'canvas-drop-active' : undefined} />;
          })}
        </>
      ) : null}

      {chain.length > 0 && !dragging ? (
        <nav className="pg-breadcrumb" aria-label="Selection path" data-testid="canvas-breadcrumb">
          {chain.map((n, i) => (
            <span key={n.id} className="pg-breadcrumb-item">
              {i > 0 ? <span aria-hidden="true">›</span> : null}
              <button type="button" aria-current={i === chain.length - 1 ? 'true' : undefined} onClick={() => onSelect(n.id)}>
                {labelFor(n.id, n.kind)}
              </button>
            </span>
          ))}
        </nav>
      ) : null}
    </div>
  );
}
