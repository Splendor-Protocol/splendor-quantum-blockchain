import { useState, type DragEvent } from 'react';
import { defaultProps, type CatalogEntry } from '@splendor/dapp-spec';
import { StatusBadge } from '../gallery/GalleryPage';
import { groupMeta, groupOrder } from '../registry/groups';

export type PaletteDragPayload = { kind: 'widget'; id: string };

/** SVG thumbnail from `/thumbs/{thumb}.svg` (copied from `@splendor/dapp-spec/thumbs`); falls back to the first letter. */
function ThumbMark({ entry, tone }: { entry: CatalogEntry; tone: string }) {
  const [failed, setFailed] = useState(false);
  if (entry.thumb && !failed) {
    return (
      <span className="pg-toy-thumb" aria-hidden="true">
        <img src={`/thumbs/${entry.thumb}.svg`} alt="" width={32} height={32} loading="lazy" onError={() => setFailed(true)} />
      </span>
    );
  }
  return (
    <span className="pg-toy-mark" data-tone={tone} aria-hidden="true">
      {entry.title.slice(0, 1)}
    </span>
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
}: {
  catalog: CatalogEntry[];
  query: string;
  onQueryChange: (q: string) => void;
  full: boolean;
  onAdd: (widget: string, props: ReturnType<typeof defaultProps>) => void;
  startDrag: (event: DragEvent, payload: PaletteDragPayload) => void;
  endDrag: () => void;
  skipClick: { current: boolean };
}) {
  const q = query.trim().toLowerCase();
  const placeable = catalog.filter(
    (e) => e.placeable && (!q || e.title.toLowerCase().includes(q) || e.description.toLowerCase().includes(q)),
  );
  const categories = groupOrder([...new Set(placeable.map((e) => e.category))]);

  return (
    <>
      <p className="pg-toybox-lead">Drag a piece onto the page.</p>
      <input
        className="pg-toy-search"
        placeholder="Find a piece"
        aria-label="Find a piece"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
      />
      {categories.map((c) => {
        const meta = groupMeta(c);
        const items = placeable.filter((e) => e.category === c);
        return (
          <section key={c}>
            <p className="pg-list-title">{meta.label}</p>
            <div className="pg-toy-grid">
              {items.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  className="pg-toy pg-palette-card"
                  draggable={!full}
                  disabled={full}
                  title={e.description}
                  data-testid={`add-${e.id}`}
                  onDragStart={(event) => startDrag(event, { kind: 'widget', id: e.id })}
                  onDragEnd={endDrag}
                  onClick={() => {
                    if (skipClick.current) return;
                    onAdd(e.id, defaultProps(e));
                  }}
                >
                  <ThumbMark entry={e} tone={meta.tone} />
                  <span className="pg-toy-copy">
                    <strong>{e.title}</strong>
                    <small>{e.description}</small>
                  </span>
                  <StatusBadge status={e.status} />
                </button>
              ))}
            </div>
          </section>
        );
      })}
      {categories.length === 0 ? <p className="pg-muted">No pieces match.</p> : null}
    </>
  );
}
