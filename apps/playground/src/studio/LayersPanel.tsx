import { memo, useEffect, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react';
import { isContainer, nodeId, type CatalogEntry, type ContainerNode, type DappPage, type LayoutNode } from '@splendor/dapp-spec';
import { groupMeta } from '../registry/groups';
import { CONTRACT_FN_MIME } from './CanvasOverlay';
import { TONE_COLORS } from './PieceArt';

export type LayerDrop = {
  parentId: string;
  /** Insertion slot among the parent's children, before any move is applied. */
  index: number;
  moveId: null;
  contractFn?: string;
};

export type LayersPanelProps = {
  page: DappPage;
  /** Every catalog entry (titles for components). */
  catalog: readonly CatalogEntry[];
  selectedId: string | null;
  /** The page is at its component limit: duplicating is disabled. */
  full?: boolean;
  onSelect: (id: string) => void;
  onDuplicate: (id: string) => void;
  onRemove: (id: string) => void;
  /** Keyboard move (Alt+Arrow): put `id` at slot `index` of `parentId` (slots count before the move). */
  onMove: (move: { id: string; parentId: string; index: number }) => void;
  /** A drag (component from the palette, or a layer) landed here; the studio knows what is being dragged. */
  onDrop: (drop: LayerDrop) => void;
  /** Decides whether a drag that started in the studio can be dropped here. */
  acceptsDrag: (dataTransfer: DataTransfer | null) => boolean;
  /** Starts dragging a layer (shares the studio's drag state, so the canvas accepts it too). */
  startDrag: (event: DragEvent, payload: { kind: 'block'; id: string }) => void;
  endDrag: () => void;
};

type DropMark = { id: string; pos: 'before' | 'after' | 'inside' };

const KIND_TITLE: Record<string, string> = { section: 'Section', row: 'Row', column: 'Column', card: 'Card', stack: 'Stack' };

const stroke = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round', strokeLinejoin: 'round' } as const;

/** Small glyph per kind: boxes for layout containers, a dot in the shelf colour for components. */
function KindIcon({ kind, color }: { kind: string; color?: string }) {
  let shape: ReactNode;
  switch (kind) {
    case 'section':
      shape = <rect x="2.5" y="3.5" width="11" height="9" rx="1.5" {...stroke} />;
      break;
    case 'row':
      shape = (
        <>
          <rect x="2.5" y="4" width="11" height="8" rx="1.5" {...stroke} />
          <path d="M6.2 4v8M9.8 4v8" {...stroke} />
        </>
      );
      break;
    case 'column':
      shape = <rect x="5" y="2.5" width="6" height="11" rx="1.5" {...stroke} />;
      break;
    case 'card':
      shape = (
        <>
          <rect x="2.5" y="3.5" width="11" height="9" rx="2.5" {...stroke} />
          <path d="M5 6.8h6" {...stroke} />
        </>
      );
      break;
    case 'stack':
      shape = <path d="M3 4.5h10M3 8h10M3 11.5h10" {...stroke} />;
      break;
    default:
      shape = <circle cx="8" cy="8" r="3.4" fill={color ?? 'currentColor'} />;
  }
  return (
    <svg className="pg-layer-icon" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false" style={color && kind === 'block' ? { color } : undefined}>
      {shape}
    </svg>
  );
}

/** Where `id` sits: its parent container and slot. Null for the page root and unknown ids. */
function whereIs(root: LayoutNode, id: string): { parent: ContainerNode; index: number } | null {
  if (!isContainer(root)) return null;
  const index = root.children.findIndex((c) => nodeId(c) === id);
  if (index >= 0) return { parent: root, index };
  for (const c of root.children) {
    const hit = whereIs(c, id);
    if (hit) return hit;
  }
  return null;
}

/**
 * The page as a tree: containers (section, row, column, card, stack) as group headers and every
 * component as a list item under its container. Click to select, drag to reorder or nest,
 * Alt+Arrow to move within the parent, Delete to remove.
 */
export const LayersPanel = memo(function LayersPanel({ page, catalog, selectedId, full = false, onSelect, onDuplicate, onRemove, onMove, onDrop, acceptsDrag, startDrag, endDrag }: LayersPanelProps) {
  const root = useRef<HTMLDivElement>(null);
  const focusAfter = useRef<string | null>(null);
  const [mark, setMark] = useState<DropMark | null>(null);

  // After a keyboard move the row is re-created in a new place: give it focus back.
  useEffect(() => {
    const id = focusAfter.current;
    if (!id) return;
    focusAfter.current = null;
    const row = [...(root.current?.querySelectorAll<HTMLElement>('[data-layer-id]') ?? [])].find((el) => el.dataset.layerId === id);
    row?.querySelector<HTMLElement>('[data-layer-select]')?.focus();
  });

  const titleOf = (node: LayoutNode): string => {
    if (node.kind === 'block') return catalog.find((e) => e.id === node.block.widget)?.title ?? node.block.widget;
    if (page.root.kind !== 'block' && page.root.id === node.id) return 'Page';
    return KIND_TITLE[node.kind] ?? node.kind.charAt(0).toUpperCase() + node.kind.slice(1);
  };

  const dropPosition = (event: DragEvent<HTMLElement>, node: LayoutNode, isRoot: boolean): DropMark['pos'] => {
    if (isRoot) return 'inside';
    const box = event.currentTarget.getBoundingClientRect();
    const y = box.height ? (event.clientY - box.top) / box.height : 0.5;
    if (node.kind === 'block') return y < 0.5 ? 'before' : 'after';
    return y < 0.3 ? 'before' : 'inside';
  };

  const onRowDragOver = (event: DragEvent<HTMLElement>, node: LayoutNode, isRoot: boolean) => {
    if (!acceptsDrag(event.dataTransfer)) return;
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = event.dataTransfer.effectAllowed.startsWith('copy') ? 'copy' : 'move';
    const pos = dropPosition(event, node, isRoot);
    const id = nodeId(node);
    setMark((prev) => (prev?.id === id && prev.pos === pos ? prev : { id, pos }));
  };

  const onRowDrop = (event: DragEvent<HTMLElement>, node: LayoutNode, isRoot: boolean) => {
    if (!acceptsDrag(event.dataTransfer)) return;
    event.preventDefault();
    event.stopPropagation();
    const pos = dropPosition(event, node, isRoot);
    const contractFn = event.dataTransfer.getData(CONTRACT_FN_MIME);
    setMark(null);
    const extra = contractFn ? { contractFn } : {};
    if (isContainer(node) && pos === 'inside') {
      onDrop({ parentId: node.id, index: node.children.length, moveId: null, ...extra });
      return;
    }
    const at = whereIs(page.root, nodeId(node));
    if (at) onDrop({ parentId: at.parent.id, index: pos === 'after' ? at.index + 1 : at.index, moveId: null, ...extra });
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (!target.hasAttribute('data-layer-select')) return;
    const id = target.closest<HTMLElement>('[data-layer-id]')?.dataset.layerId;
    if (!id) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onSelect(id);
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'd' && whereIs(page.root, id)) {
      event.preventDefault();
      if (!full) onDuplicate(id);
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (event.altKey) {
        const at = whereIs(page.root, id);
        if (!at) return;
        const last = at.parent.children.length - 1;
        if (event.key === 'ArrowUp' && at.index > 0) {
          focusAfter.current = id;
          onMove({ id, parentId: at.parent.id, index: at.index - 1 });
        } else if (event.key === 'ArrowDown' && at.index < last) {
          focusAfter.current = id;
          onMove({ id, parentId: at.parent.id, index: at.index + 2 });
        }
        return;
      }
      const buttons = [...(root.current?.querySelectorAll<HTMLElement>('[data-layer-select]') ?? [])];
      const next = buttons[buttons.indexOf(target) + (event.key === 'ArrowDown' ? 1 : -1)];
      next?.focus();
      return;
    }
    if (event.key === 'Delete' && whereIs(page.root, id)) {
      event.preventDefault();
      // Focus lands on the neighbour so the keyboard keeps its place.
      const buttons = [...(root.current?.querySelectorAll<HTMLElement>('[data-layer-select]') ?? [])];
      const i = buttons.indexOf(target);
      const neighbour = buttons[i + 1] ?? buttons[i - 1];
      const neighbourId = neighbour?.closest<HTMLElement>('[data-layer-id]')?.dataset.layerId ?? null;
      focusAfter.current = neighbourId;
      onRemove(id);
    }
  };

  const row = (node: LayoutNode, depth: number, asItem: boolean): ReactNode => {
    const id = nodeId(node);
    const isRoot = page.root.kind !== 'block' && page.root.id === id;
    const title = titleOf(node);
    const entry = node.kind === 'block' ? catalog.find((e) => e.id === node.block.widget) : undefined;
    const color = entry ? (TONE_COLORS[groupMeta(entry.category).tone] ?? undefined) : undefined;
    const active = id === selectedId;
    const m = mark?.id === id ? mark.pos : null;
    const className = `pg-layer${node.kind === 'block' ? ' is-leaf' : ' is-container'}${active ? ' is-active' : ''}${m ? ` is-drop-${m}` : ''}`;
    // The row is presentational; its tree item is the focusable label, with the actions beside it
    // (a tree item must not contain buttons).
    const common = {
      className,
      'data-layer-id': id,
      'data-kind': node.kind,
      role: 'none',
      draggable: !isRoot,
      onDragStart: (e: DragEvent<HTMLElement>) => {
        if (isRoot) return;
        e.stopPropagation();
        startDrag(e, { kind: 'block', id });
      },
      onDragEnd: () => {
        setMark(null);
        endDrag();
      },
      onDragOver: (e: DragEvent<HTMLElement>) => onRowDragOver(e, node, isRoot),
      onDragLeave: (e: DragEvent<HTMLElement>) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setMark((prev) => (prev?.id === id ? null : prev));
      },
      onDrop: (e: DragEvent<HTMLElement>) => onRowDrop(e, node, isRoot),
    } as const;
    const body = (
      <>
        <div
          role="treeitem"
          className="pg-layer-main"
          data-layer-select=""
          tabIndex={id === tabStop ? 0 : -1}
          style={{ paddingLeft: `${0.35 + depth * 0.8}rem` }}
          aria-level={depth + 1}
          aria-selected={active}
          aria-expanded={isContainer(node) ? true : undefined}
          aria-keyshortcuts={isRoot ? undefined : 'Delete Control+D Alt+ArrowUp Alt+ArrowDown'}
          onClick={() => onSelect(id)}
        >
          <KindIcon kind={node.kind === 'block' ? 'block' : node.kind} color={color} />
          <span className="pg-layer-title">{title}</span>
        </div>
        {!isRoot ? (
          <>
            <button type="button" className="pg-icon-btn" aria-label={`Duplicate ${title}`} title="Duplicate" disabled={full} onClick={() => onDuplicate(id)}>
              ⧉
            </button>
            <button type="button" className="pg-icon-btn" aria-label={`Remove ${title}`} title="Remove" onClick={() => onRemove(id)}>
              ×
            </button>
          </>
        ) : null}
      </>
    );
    return asItem ? (
      <li key={id} {...common}>
        {body}
      </li>
    ) : (
      <div key={id} {...common}>
        {body}
      </div>
    );
  };

  const children = (container: ContainerNode, depth: number): ReactNode[] => {
    const out: ReactNode[] = [];
    let run: ReactNode[] = [];
    const flush = () => {
      if (!run.length) return;
      out.push(
        <ul key={`run-${nodeId(container)}-${out.length}`} role="group" className="pg-layer-run">
          {run}
        </ul>,
      );
      run = [];
    };
    for (const child of container.children) {
      if (child.kind === 'block') {
        run.push(row(child, depth, true));
        continue;
      }
      flush();
      out.push(row(child, depth, false));
      out.push(...children(child, depth + 1));
    }
    flush();
    if (!container.children.length) out.push(<p key={`empty-${container.id}`} className="pg-layer-empty" style={{ paddingLeft: `${0.35 + depth * 0.8}rem` }}>Empty</p>);
    return out;
  };

  const tree = page.root;
  // One tab stop for the whole tree (the selection, else the page); arrows move between items.
  const tabStop = selectedId && whereIs(tree, selectedId) ? selectedId : nodeId(tree);
  return (
    <div ref={root} className="pg-layers" role="tree" aria-label="Page layers" data-testid="block-list" onKeyDown={onKeyDown}>
      {isContainer(tree) ? (
        <>
          {row(tree, 0, false)}
          <div role="group">{children(tree, 1)}</div>
        </>
      ) : null}
    </div>
  );
});
