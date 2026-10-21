export type NavbarBlockProps = {
  brand?: string;
  /** `Label|/path` items, comma-separated. */
  links?: string;
  ctaLabel?: string;
  ctaPath?: string;
};

function toHref(path: string): string {
  if (!path) return '';
  if (path.startsWith('http')) return path;
  return `#${path.startsWith('/') ? path : `/${path}`}`;
}

/** In-page navigation bar with brand, links and an optional button. */
export function NavbarBlock({ brand = 'My dApp', links = 'Home|/,About|/about', ctaLabel = '', ctaPath = '' }: NavbarBlockProps) {
  const items = links
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const [label, ...rest] = s.split('|');
      return { label: (label ?? '').trim(), href: toHref(rest.join('|').trim()) };
    });
  const cta = toHref(ctaPath);
  return (
    <nav
      className="sqc-widget dapp-navbar"
      data-sqc-widget="navbar"
      style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '1rem', padding: '0.75rem 1.25rem' }}
    >
      <strong style={{ marginRight: 'auto' }}>{brand}</strong>
      {items.map((l) => (
        <a key={`${l.label}-${l.href}`} href={l.href || undefined} style={{ color: 'inherit', textDecoration: 'none' }}>
          {l.label}
        </a>
      ))}
      {ctaLabel ? (
        cta ? (
          <a className="sqc-btn" href={cta}>
            {ctaLabel}
          </a>
        ) : (
          <button type="button" className="sqc-btn" disabled>
            {ctaLabel}
          </button>
        )
      ) : null}
    </nav>
  );
}
