import { blocksFromTree, type DappSpec } from '@splendor/dapp-spec';

export type FlowLink = {
  fromPath: string;
  fromTitle: string;
  /** Id of the button / CTA block the link comes from (select it to edit the link). */
  fromBlockId: string;
  to: string;
  label: string;
};

function pagePathOf(value: string): string {
  const v = value.replace(/^#/, '');
  return v.startsWith('/') ? v : `/${v}`;
}

/** Internal page links from buttons and CTAs. External URLs and links to missing pages are skipped. */
export function collectLinks(spec: DappSpec): FlowLink[] {
  const links: FlowLink[] = [];
  const paths = new Set(spec.pages.map((p) => p.path));
  const push = (page: DappSpec['pages'][number], blockId: string, raw: unknown, label: unknown, fallback: string) => {
    if (typeof raw !== 'string' || !raw.trim() || /^https?:/i.test(raw.trim())) return;
    const to = pagePathOf(raw.trim());
    if (!paths.has(to)) return;
    links.push({ fromPath: page.path, fromTitle: page.title, fromBlockId: blockId, to, label: typeof label === 'string' && label ? label : fallback });
  };
  for (const page of spec.pages) {
    const blocks = page.blocks ?? blocksFromTree(page.root);
    for (const b of blocks) {
      const props = b.props;
      if (b.widget === 'hero-centered') push(page, b.id, props.ctaPath, props.ctaLabel, 'CTA');
      else if (b.widget === 'cta-banner') push(page, b.id, props.buttonPath, props.buttonLabel, 'Button');
      else if (b.widget === 'action-button') push(page, b.id, props.href, props.label, 'Link');
    }
  }
  return links;
}

export function FlowsPanel({
  spec,
  onSelectBlock,
  onSelectPage,
}: {
  spec: DappSpec;
  /** Called when a link is clicked: jump to the page and select the button that holds the link. */
  onSelectBlock?: (pagePath: string, blockId: string) => void;
  /** Called when a page box is clicked. */
  onSelectPage?: (path: string) => void;
}) {
  const links = collectLinks(spec);
  const pages = spec.pages;
  const w = 280;
  const h = Math.max(120, pages.length * 56 + 40);
  const pageY = new Map(pages.map((p, i) => [p.path, 30 + i * 56]));
  const activate = (l: FlowLink) => onSelectBlock?.(l.fromPath, l.fromBlockId);

  return (
    <div className="pg-stack" data-testid="flows-panel">
      <p className="pg-toybox-lead">Page flows</p>
      <p className="pg-muted" style={{ margin: 0 }}>
        Links from buttons and CTAs between your pages. Click a link to select its button.
      </p>
      <svg className="pg-flows-svg" viewBox={`0 0 ${w} ${h}`} width="100%" aria-label="Page flow diagram">
        {links.map((l, i) => {
          const y1 = pageY.get(l.fromPath) ?? 0;
          const y2 = pageY.get(l.to) ?? 0;
          const d = `M 40 ${y1} C 120 ${y1}, 120 ${y2}, 200 ${y2}`;
          return (
            <g
              key={`${l.fromBlockId}-${l.to}-${i}`}
              className="pg-flows-edge"
              role="button"
              tabIndex={0}
              aria-label={`${l.label}: ${l.fromTitle} to ${l.to}. Select the button`}
              data-testid="flow-edge"
              onClick={() => activate(l)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  activate(l);
                }
              }}
            >
              {/* Wide invisible stroke: a forgiving click target. */}
              <path d={d} fill="none" stroke="transparent" strokeWidth="12" />
              <path className="pg-flows-edge-line" d={d} fill="none" stroke="var(--pg-accent)" strokeWidth="1.5" opacity="0.7" />
              <text x="210" y={y2 + 4} fill="var(--pg-muted)" fontSize="10">
                {l.label}
              </text>
            </g>
          );
        })}
        {pages.map((p) => {
          const y = pageY.get(p.path)!;
          return (
            <g
              key={p.path}
              className={onSelectPage ? 'pg-flows-node is-clickable' : 'pg-flows-node'}
              onClick={() => onSelectPage?.(p.path)}
            >
              <rect x="8" y={y - 16} width="120" height="32" rx="8" fill="var(--pg-surface-2)" stroke="var(--pg-border)" />
              <text x="16" y={y + 4} fill="var(--pg-text)" fontSize="11">
                {p.title}
              </text>
            </g>
          );
        })}
      </svg>
      {links.length === 0 ? <p className="pg-muted">No internal page links yet.</p> : null}
      <ul className="pg-flows-list">
        {links.map((l, i) => (
          <li key={`${l.fromBlockId}-${i}`}>
            <button type="button" className="pg-link-btn" data-testid="flow-link" onClick={() => activate(l)}>
              <strong>{l.label}</strong>: {l.fromTitle} ({l.fromPath}) {'\u2192'} {l.to}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
