import { useEffect, useRef, useState, type DragEvent } from 'react';
import { createPortal } from 'react-dom';
import { defaultProps, type CatalogEntry } from '@splendor/dapp-spec';
import { groupMeta, groupOrder } from '../registry/groups';
import { PieceArt, TONE_COLORS } from './PieceArt';

export type PaletteDragPayload = { kind: 'widget'; id: string };

const RECENT_KEY = 'sqc-studio-recent-pieces';
const RECENT_MAX = 4;
/** Shelves a dApp builder reaches for first: the page itself, then tokens and contracts. */
const SHELVES_FIRST = ['layout', 'tokens', 'modules'];

function shelfOrder(categories: readonly string[]): string[] {
  const ordered = groupOrder(categories);
  return [...SHELVES_FIRST.filter((c) => ordered.includes(c)), ...ordered.filter((c) => !SHELVES_FIRST.includes(c))];
}
const HOVER_DELAY_MS = 280;

function readRecent(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]') as unknown;
    return Array.isArray(raw) ? raw.filter((x): x is string => typeof x === 'string').slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

function pushRecent(id: string): string[] {
  const next = [id, ...readRecent().filter((x) => x !== id)].slice(0, RECENT_MAX);
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    /* storage full or blocked: recents are a nicety */
  }
  return next;
}

/** `rect` is the hovered tile; `edge` the palette column it sits in (so the card never covers other tiles). */
type Hover = { entry: CatalogEntry; rect: DOMRect; edge: { left: number; right: number } };

/** Floating card beside the palette: big preview, what it does, what it needs. */
function PieceTooltip({ hover }: { hover: Hover }) {
  const { entry, rect, edge } = hover;
  const meta = groupMeta(entry.category);
  const accent = TONE_COLORS[meta.tone] ?? TONE_COLORS.violet!;
  const width = 280;
  const gap = 10;
  const fitsRight = edge.right + gap + width < window.innerWidth;
  const left = fitsRight ? edge.right + gap : Math.max(8, edge.left - gap - width);
  const top = Math.max(8, Math.min(rect.top - 12, window.innerHeight - 330));
  const needsWallet = entry.requires?.wallet;
  const modules = entry.requires?.modules ?? [];
  return createPortal(
    <div
      className="pg-piece-tip"
      role="tooltip"
      id={`piece-tip-${entry.id}`}
      style={{ left, top, width, ['--tip-accent' as string]: accent }}
      data-side={fitsRight ? 'right' : 'left'}
    >
      <div className="pg-piece-tip-art">
        <PieceArt id={entry.id} category={entry.category} tone={meta.tone} />
      </div>
      <div className="pg-piece-tip-body">
        <div className="pg-piece-tip-head">
          <strong>{entry.title}</strong>
          <span className="pg-piece-tip-shelf">{meta.label}</span>
        </div>
        <p>{entry.description}</p>
        <div className="pg-piece-tip-tags">
          <span className={needsWallet ? 'is-wallet' : 'is-free'}>{needsWallet ? 'Needs a wallet' : 'No wallet needed'}</span>
          {entry.container ? <span>Holds other components</span> : null}
          {modules.map((m) => (
            <span key={m}>{m.toUpperCase()}</span>
          ))}
          {entry.status !== 'stable' ? <span className="is-status">{entry.status}</span> : null}
        </div>
        <p className="pg-piece-tip-hint">
          <kbd>Drag</kbd> onto the page or <kbd>Click</kbd> / <kbd>Enter</kbd> to add (inside the selected container, else at the end)
        </p>
      </div>
    </div>,
    document.body,
  );
}

export function Palette({
  catalog,
  query,
  onQueryChange,
  full,
  onAdd,
  startDrag,
  endDrag,
  skipClick,
  insertTarget = null,
}: {
  catalog: CatalogEntry[];
  query: string;
  onQueryChange: (q: string) => void;
  full: boolean;
  onAdd: (widget: string, props: ReturnType<typeof defaultProps>) => void;
  startDrag: (event: DragEvent, payload: PaletteDragPayload) => void;
  endDrag: () => void;
  skipClick: { current: boolean };
  /** Name of the selected container: clicking a component adds it there instead of at the end of the page. */
  insertTarget?: string | null;
}) {
  const [shelf, setShelf] = useState<string>('all');
  const [recent, setRecent] = useState<string[]>(() => readRecent());
  const [hover, setHover] = useState<Hover | null>(null);
  const timer = useRef<number | null>(null);
  const dragging = useRef(false);

  useEffect(() => () => {
    if (timer.current) window.clearTimeout(timer.current);
  }, []);

  const q = query.trim().toLowerCase();
  // `hidden` entries still render in old specs but are not offered for new pages.
  const placeable = catalog.filter((e) => e.placeable && !e.hidden);
  const shelves = shelfOrder([...new Set(placeable.map((e) => e.category))]);
  const matches = placeable.filter(
    (e) =>
      (shelf === 'all' || e.category === shelf) &&
      (!q || e.title.toLowerCase().includes(q) || e.description.toLowerCase().includes(q) || e.category.includes(q)),
  );
  const categories = shelfOrder([...new Set(matches.map((e) => e.category))]);
  const recentEntries = !q && shelf === 'all' ? recent.map((id) => placeable.find((e) => e.id === id)).filter((e): e is CatalogEntry => !!e) : [];

  const showTip = (entry: CatalogEntry, el: HTMLElement) => {
    if (timer.current) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      if (dragging.current) return;
      const rect = el.getBoundingClientRect();
      const column = el.closest('aside')?.getBoundingClientRect();
      // In the narrow single-column layout the aside spans the screen: fall back to the tile itself.
      const edge = column && column.width < window.innerWidth * 0.5 ? column : rect;
      setHover({ entry, rect, edge: { left: edge.left, right: edge.right } });
    }, HOVER_DELAY_MS);
  };
  const hideTip = () => {
    if (timer.current) window.clearTimeout(timer.current);
    setHover(null);
  };

  const add = (e: CatalogEntry) => {
    if (skipClick.current) return;
    setRecent(pushRecent(e.id));
    hideTip();
    onAdd(e.id, defaultProps(e));
  };

  const tile = (e: CatalogEntry, keyPrefix = '') => {
    const meta = groupMeta(e.category);
    return (
      <button
        key={keyPrefix + e.id}
        type="button"
        className="pg-piece"
        data-tone={meta.tone}
        style={{ ['--piece-accent' as string]: TONE_COLORS[meta.tone] ?? TONE_COLORS.violet }}
        draggable={!full}
        disabled={full}
        aria-label={`${e.title}: ${e.description}`}
        aria-describedby={hover?.entry.id === e.id ? `piece-tip-${e.id}` : undefined}
        data-testid={keyPrefix ? undefined : `add-${e.id}`}
        onMouseEnter={(ev) => showTip(e, ev.currentTarget)}
        onMouseLeave={hideTip}
        onFocus={(ev) => showTip(e, ev.currentTarget)}
        onBlur={hideTip}
        onDragStart={(event) => {
          dragging.current = true;
          hideTip();
          const art = event.currentTarget.querySelector('.pg-piece-art');
          if (art instanceof HTMLElement) event.dataTransfer.setDragImage(art, art.offsetWidth / 2, art.offsetHeight / 2);
          startDrag(event, { kind: 'widget', id: e.id });
        }}
        onDragEnd={() => {
          dragging.current = false;
          setRecent(pushRecent(e.id));
          endDrag();
        }}
        onClick={() => add(e)}
      >
        <span className="pg-piece-art">
          <PieceArt id={e.id} category={e.category} tone={meta.tone} />
          {e.status !== 'stable' ? <span className="pg-piece-status">{e.status}</span> : null}
          {e.requires?.wallet ? (
            <span className="pg-piece-wallet" title="Needs a wallet">
              <svg viewBox="0 0 16 16" aria-hidden="true">
                <path d="M2.5 4.5h10a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-10a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1Zm0 0V4a1 1 0 0 1 1-1h7" fill="none" stroke="currentColor" strokeWidth="1.4" />
                <circle cx="11" cy="8.5" r="1" fill="currentColor" />
              </svg>
            </span>
          ) : null}
        </span>
        <span className="pg-piece-title">{e.title}</span>
      </button>
    );
  };

  return (
    <div className="pg-palette">
      <div className="pg-palette-head">
        <p className="pg-toybox-lead">Components</p>
        <div className="pg-palette-search">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
            <path d="M10.5 10.5 14 14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
          <input placeholder="Search components" aria-label="Find a component" value={query} onChange={(e) => onQueryChange(e.target.value)} />
        </div>
        <div className="pg-shelf-chips" role="group" aria-label="Filter by shelf">
          {['all', ...shelves].map((s) => {
            const meta = s === 'all' ? { label: 'All', tone: 'violet' } : groupMeta(s);
            return (
              <button
                key={s}
                type="button"
                aria-pressed={shelf === s}
                className={`pg-shelf-chip${shelf === s ? ' is-active' : ''}`}
                style={{ ['--piece-accent' as string]: TONE_COLORS[meta.tone] }}
                onClick={() => setShelf(s)}
              >
                {s !== 'all' ? <i aria-hidden="true" /> : null}
                {meta.label}
              </button>
            );
          })}
        </div>
      </div>

      {full ? <p className="pg-palette-full">This page is full. Remove a component to add more.</p> : null}
      {insertTarget && !full ? (
        <p className="pg-muted" role="status" data-testid="palette-insert-target" style={{ margin: '0 0 0.5rem' }}>
          New components go inside the selected {insertTarget.toLowerCase()}.
        </p>
      ) : null}

      {recentEntries.length ? (
        <section className="pg-shelf">
          <p className="pg-shelf-title">Recently used</p>
          <div className="pg-piece-grid">{recentEntries.map((e) => tile(e, 'recent-'))}</div>
        </section>
      ) : null}

      {categories.map((c) => {
        const meta = groupMeta(c);
        const items = matches.filter((e) => e.category === c);
        return (
          <section key={c} className="pg-shelf">
            <p className="pg-shelf-title" style={{ ['--piece-accent' as string]: TONE_COLORS[meta.tone] }}>
              <i aria-hidden="true" />
              {meta.label}
              <span>{items.length}</span>
            </p>
            <div className="pg-piece-grid">{items.map((e) => tile(e))}</div>
          </section>
        );
      })}
      {categories.length === 0 ? <p className="pg-muted">No components match “{query}”.</p> : null}
      {hover ? <PieceTooltip hover={hover} /> : null}
    </div>
  );
}
