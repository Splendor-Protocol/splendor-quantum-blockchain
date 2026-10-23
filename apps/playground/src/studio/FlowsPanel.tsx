import { useId, useState } from 'react';
import { blocksFromTree, type DappSpec } from '@splendor/dapp-spec';
import './preview-app.css';

/** Props that can hold a page link or URL. Shared with the Inspector's Actions tab. */
export const ACTION_KEYS = ['href', 'ctaPath', 'buttonPath', 'link', 'action'] as const;

/** Label prop that usually sits next to an action prop (`ctaPath` -> `ctaLabel`). */
const LABEL_FOR: Record<string, string> = { href: 'label', ctaPath: 'ctaLabel', buttonPath: 'buttonLabel', link: 'label', action: 'label' };

export type FlowLink = {
  fromPath: string;
  fromTitle: string;
  /** Id of the button / CTA block the link comes from (select it to edit the link). */
  fromBlockId: string;
  to: string;
  label: string;
  /** Set when the target page does not exist (the link is broken). */
  missing?: true;
};

/** Normalises `#/x`, `/x` and `x` to `/x`. */
export function pagePathOf(value: string): string {
  const v = value.trim().replace(/^#/, '');
  return v.startsWith('/') ? v : `/${v}`;
}

/** True for values that point outside the dApp (http(s), mailto, tel and other schemes). */
export function isExternalHref(value: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(value.trim()) || value.trim().startsWith('//');
}

/** `Label|/path,Label|/path` (the navbar block's `links` prop). */
export function parseNavbarLinks(raw: unknown): { label: string; path: string }[] {
  if (typeof raw !== 'string') return [];
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const [label, ...rest] = s.split('|');
      return { label: (label ?? '').trim(), path: rest.join('|').trim() };
    })
    .filter((l) => l.path);
}

/**
 * Internal page links from every block (buttons, CTAs, navbars…). External URLs are skipped; links
 * to pages that do not exist are kept and flagged with `missing`.
 */
export function collectLinks(spec: DappSpec): FlowLink[] {
  const links: FlowLink[] = [];
  const paths = new Set(spec.pages.map((p) => p.path));
  const push = (page: DappSpec['pages'][number], blockId: string, raw: unknown, label: unknown, fallback: string) => {
    if (typeof raw !== 'string' || !raw.trim() || isExternalHref(raw)) return;
    const to = pagePathOf(raw);
    links.push({
      fromPath: page.path,
      fromTitle: page.title,
      fromBlockId: blockId,
      to,
      label: typeof label === 'string' && label ? label : fallback,
      ...(paths.has(to) ? {} : { missing: true as const }),
    });
  };
  for (const page of spec.pages) {
    const blocks = page.blocks ?? blocksFromTree(page.root);
    for (const b of blocks) {
      const props = b.props;
      for (const key of ACTION_KEYS) {
        const labelKey = LABEL_FOR[key]!;
        const fallback = key === 'ctaPath' ? 'CTA' : key === 'buttonPath' ? 'Button' : 'Link';
        push(page, b.id, props[key], props[labelKey], fallback);
      }
      if (b.widget === 'navbar') {
        for (const l of parseNavbarLinks(props.links)) push(page, b.id, l.path, l.label, 'Nav link');
      }
    }
  }
  return links;
}

export type FlowsPanelProps = {
  spec: DappSpec;
  /** Called when a link is clicked: jump to the page and select the button that holds the link. */
  onSelectBlock?: (pagePath: string, blockId: string) => void;
  /** Called when a page card is clicked, and by the "Open <page>" button of a selected link. */
  onSelectPage?: (path: string) => void;
  /** Shows a "Play from here" button on every card; called with that page's path. */
  onPlay?: (path: string) => void;
};

type PageAccessKind = 'public' | 'wallet' | 'signin';
const ACCESS_LABEL: Record<PageAccessKind, string> = { public: 'Public', wallet: 'Wallet', signin: 'Signed in' };

// Storyboard geometry (CSS px). Cards sit in one row, in page order; arrows live in the gaps and lanes.
const CARD_W = 176;
const CARD_H = 184;
const GAP = 124;
const PAD_X = 14;
const LANE_MIN = 40;
const ROW_STEP = 22;
const ROW_SLOTS = 6;

const truncate = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}\u2026` : s);

type Placed = { link: FlowLink; index: number; from: number; to: number; outRow: number; inRow: number; pair: number };

/** Kind of wireframe bar a widget draws. */
function wireKind(widget: string): 'hero' | 'cta' | 'tile' | 'bar' {
  if (widget.startsWith('hero')) return 'hero';
  if (/cta|button|action|form|transfer|deploy/.test(widget)) return 'cta';
  if (/stat|card|account|token|balance|tile|price|pricing/.test(widget)) return 'tile';
  return 'bar';
}

function Wireframe({ widgets }: { widgets: string[] }) {
  const shown = widgets.slice(0, 8);
  return (
    <span className="pg-wf" aria-hidden="true">
      <span className="pg-wf-nav" />
      {shown.map((w, i) => (
        <span key={`${w}-${i}`} className={`pg-wf-block pg-wf-${wireKind(w)}`} />
      ))}
      {widgets.length > shown.length ? <span className="pg-wf-more">+{widgets.length - shown.length}</span> : null}
    </span>
  );
}

/**
 * Storyboard of the app: a card per page in page order, an arrow per link between pages (labelled
 * with the button text), broken links in red. Cards open the page; arrows select the button that
 * holds the link and offer to open the page it leads to.
 */
export function FlowsPanel({ spec, onSelectBlock, onSelectPage, onPlay }: FlowsPanelProps) {
  const uid = useId().replace(/[^A-Za-z0-9_-]/g, '');
  const links = collectLinks(spec);
  const broken = links.filter((l) => l.missing);
  const pages = spec.pages;
  const [active, setActive] = useState<number | null>(null);
  const activate = (l: FlowLink, i: number) => {
    setActive(i);
    onSelectBlock?.(l.fromPath, l.fromBlockId);
  };

  const indexOfPath = new Map(pages.map((p, i) => [p.path, i]));
  const outCount = new Map<number, number>();
  const inCount = new Map<number, number>();
  const pairCount = new Map<string, number>();
  const placed: Placed[] = links.map((link, index) => {
    const from = indexOfPath.get(link.fromPath) ?? 0;
    const to = link.missing ? -1 : (indexOfPath.get(link.to) ?? -1);
    const outRow = outCount.get(from) ?? 0;
    outCount.set(from, outRow + 1);
    const inRow = to >= 0 ? (inCount.get(to) ?? 0) : 0;
    if (to >= 0) inCount.set(to, inRow + 1);
    const key = `${from}>${to}`;
    const pair = pairCount.get(key) ?? 0;
    pairCount.set(key, pair + 1);
    return { link, index, from, to, outRow, inRow, pair };
  });

  // Arcs over (forward, not adjacent) or under (backward) the cards need lanes tall enough for them.
  const laneDepth = (p: Placed) => LANE_MIN + 14 * Math.max(0, Math.abs(p.to - p.from) - 1) + (p.pair % 4) * 10;
  const topLane = Math.max(LANE_MIN + 18, ...placed.filter((p) => p.to === p.from || p.to > p.from + 1).map(laneDepth));
  const bottomLane = Math.max(LANE_MIN, ...placed.filter((p) => p.to >= 0 && p.to < p.from).map(laneDepth));
  const cardX = (i: number) => PAD_X + i * (CARD_W + GAP);
  const cardY = topLane;
  const width = PAD_X * 2 + pages.length * CARD_W + pages.length * GAP;
  const height = topLane + CARD_H + bottomLane + 12;
  const rowY = (row: number) => cardY + 38 + (row % ROW_SLOTS) * ROW_STEP;

  const edgeGeometry = (p: Placed): { d: string; lx: number; ly: number; anchor: 'middle' | 'start' } => {
    const x0 = cardX(p.from) + CARD_W;
    if (p.link.missing || p.to < 0) {
      const y = rowY(p.outRow);
      return { d: `M ${x0} ${y} L ${x0 + 52} ${y}`, lx: x0 + 4, ly: y - 5, anchor: 'start' };
    }
    if (p.to === p.from) {
      const xs = cardX(p.from) + CARD_W - 60 - (p.pair % 3) * 12;
      const xe = cardX(p.from) + CARD_W - 16;
      const ly = cardY - 34 - (p.pair % 3) * 8;
      return { d: `M ${xs} ${cardY} C ${xs} ${ly}, ${xe} ${ly}, ${xe} ${cardY}`, lx: (xs + xe) / 2, ly: ly - 3, anchor: 'middle' };
    }
    if (p.to === p.from + 1) {
      const ys = rowY(p.outRow);
      const ye = rowY(p.inRow);
      const xe = cardX(p.to);
      const mid = (x0 + xe) / 2;
      return { d: `M ${x0} ${ys} C ${mid} ${ys}, ${mid} ${ye}, ${xe} ${ye}`, lx: mid, ly: (ys + ye) / 2 - 5, anchor: 'middle' };
    }
    if (p.to > p.from) {
      const lane = laneDepth(p);
      const xs = cardX(p.from) + CARD_W - 30 - (p.pair % 4) * 14;
      const xe = cardX(p.to) + 30 + (p.pair % 4) * 14;
      const top = cardY - lane;
      return { d: `M ${xs} ${cardY} C ${xs} ${top}, ${xe} ${top}, ${xe} ${cardY}`, lx: (xs + xe) / 2, ly: top + lane * 0.25 - 4, anchor: 'middle' };
    }
    const lane = laneDepth(p);
    const bottom = cardY + CARD_H;
    const xs = cardX(p.from) + 30 + (p.pair % 4) * 14;
    const xe = cardX(p.to) + CARD_W - 30 - (p.pair % 4) * 14;
    const low = bottom + lane;
    return { d: `M ${xs} ${bottom} C ${xs} ${low}, ${xe} ${low}, ${xe} ${bottom}`, lx: (xs + xe) / 2, ly: bottom + lane * 0.75 + 12, anchor: 'middle' };
  };

  const activeLink = active != null ? links[active] : undefined;
  const target = activeLink && !activeLink.missing ? pages.find((p) => p.path === activeLink.to) : undefined;

  return (
    <div className="pg-stack" data-testid="flows-panel">
      <p className="pg-toybox-lead">Page flows</p>
      <p className="pg-muted" style={{ margin: 0 }}>
        Your pages in order, with an arrow for every link between them. Click a card to open the page, an arrow to select the button that holds the link.
      </p>
      {broken.length ? (
        <p className="pg-error" role="status" data-testid="flows-broken-summary" style={{ margin: 0 }}>
          {broken.length === 1 ? '1 link points to a page that does not exist.' : `${broken.length} links point to pages that do not exist.`}
        </p>
      ) : null}

      <div className="pg-story" data-testid="flows-storyboard">
        <div className="pg-story-canvas" style={{ width, height }}>
          {pages.map((p, i) => {
            const blocks = p.blocks ?? blocksFromTree(p.root);
            const access = (p.access ?? 'public') as PageAccessKind;
            const open = () => onSelectPage?.(p.path);
            return (
              <div key={p.path} className="pg-story-node" style={{ left: cardX(i), top: cardY, width: CARD_W, height: CARD_H }}>
                <div
                  className={`pg-story-card${onSelectPage ? ' is-clickable' : ''}`}
                  role="button"
                  tabIndex={0}
                  aria-label={`Open page ${p.title}`}
                  data-testid="flow-page"
                  data-path={p.path}
                  onClick={open}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      open();
                    }
                  }}
                >
                  <span className="pg-story-title">{p.title}</span>
                  <span className="pg-story-meta">
                    <code className="pg-mono">{p.path}</code>
                    <span className={`pg-badge pg-access-${access}`} data-testid="flow-access">
                      {ACCESS_LABEL[access] ?? 'Public'}
                    </span>
                  </span>
                  <Wireframe widgets={blocks.map((b) => b.widget)} />
                  <span className="pg-story-count">{blocks.length === 1 ? '1 component' : `${blocks.length} components`}</span>
                </div>
                {onPlay ? (
                  <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm pg-story-play" data-testid="flow-play" aria-label={`Play from here: ${p.title}`} onClick={() => onPlay(p.path)}>
                    {'\u25B6'} Play from here
                  </button>
                ) : null}
              </div>
            );
          })}

          <svg className="pg-story-svg" width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="group" aria-label="Page flow diagram">
            <defs>
              <marker id={`${uid}-arrow`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--pg-accent)" />
              </marker>
              <marker id={`${uid}-arrow-broken`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--pg-danger)" />
              </marker>
            </defs>
            {placed.map((p) => {
              const l = p.link;
              const g = edgeGeometry(p);
              const isActive = active === p.index;
              const name = l.missing ? `${l.label}: ${l.fromTitle} to ${l.to}, missing page. Select the block` : `${l.label}: ${l.fromTitle} to ${l.to}. Select the block`;
              return (
                <g
                  key={`${l.fromBlockId}-${l.to}-${p.index}`}
                  className={`pg-flows-edge${l.missing ? ' is-broken' : ''}${isActive ? ' is-active' : ''}`}
                  role="button"
                  tabIndex={0}
                  aria-label={name}
                  aria-pressed={isActive}
                  data-testid={l.missing ? 'flow-edge-broken' : 'flow-edge'}
                  onClick={() => activate(l, p.index)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      activate(l, p.index);
                    }
                  }}
                >
                  <title>{l.missing ? `${l.label}: missing page ${l.to}` : `${l.label}: ${l.fromTitle} \u2192 ${l.to}`}</title>
                  {/* Wide invisible stroke: a forgiving click target. */}
                  <path d={g.d} fill="none" stroke="transparent" strokeWidth="14" />
                  <path
                    className="pg-flows-edge-line"
                    d={g.d}
                    fill="none"
                    stroke={l.missing ? 'var(--pg-danger)' : 'var(--pg-accent)'}
                    strokeWidth={isActive ? 2.5 : 1.5}
                    strokeDasharray={l.missing ? '4 3' : undefined}
                    opacity={l.missing || isActive ? 1 : 0.75}
                    markerEnd={l.missing ? undefined : `url(#${uid}-arrow)`}
                  />
                  {l.missing ? (
                    <text className="pg-story-x" x={cardX(p.from) + CARD_W + 60} y={rowY(p.outRow) + 4} fill="var(--pg-danger)" fontSize="13" textAnchor="middle">
                      {'\u2715'}
                    </text>
                  ) : null}
                  <text x={g.lx} y={g.ly} fill={l.missing ? 'var(--pg-danger)' : 'var(--pg-text)'} fontSize="10.5" textAnchor={g.anchor}>
                    {truncate(l.label, 16)}
                  </text>
                  {l.missing ? (
                    <text x={cardX(p.from) + CARD_W + 4} y={rowY(p.outRow) + 15} fill="var(--pg-danger)" fontSize="9.5">
                      missing page
                    </text>
                  ) : null}
                </g>
              );
            })}
          </svg>
        </div>
      </div>

      {activeLink ? (
        <div className="pg-story-open" role="status" data-testid="flow-selected">
          <span>
            <strong>{activeLink.label}</strong>: {activeLink.fromTitle} {'\u2192'} {activeLink.missing ? `${activeLink.to} (missing page)` : (target?.title ?? activeLink.to)}
          </span>
          {target && onSelectPage ? (
            <button type="button" className="pg-btn pg-btn-sm" data-testid="flow-open-target" onClick={() => onSelectPage(target.path)}>
              Open {target.title}
            </button>
          ) : null}
        </div>
      ) : null}

      {links.length === 0 ? <p className="pg-muted">No internal page links yet.</p> : null}
      <ul className="pg-flows-list">
        {links.map((l, i) => (
          <li key={`${l.fromBlockId}-${i}`}>
            <button type="button" className="pg-link-btn" data-testid="flow-link" onClick={() => activate(l, i)}>
              <strong>{l.label}</strong>: {l.fromTitle} ({l.fromPath}) {'\u2192'} {l.to}
            </button>
            {l.missing ? (
              <span className="pg-error" data-testid="flow-link-missing">
                {' '}
                missing page
              </span>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}
