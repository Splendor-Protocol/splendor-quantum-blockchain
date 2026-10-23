import { pageHref } from './pageHref';
import { parseList } from './parseList';

export type NavbarBlockProps = {
  brand?: string;
  /** `Label|/path` items, comma-separated (or one per line when the value contains a newline). */
  links?: string;
  ctaLabel?: string;
  ctaPath?: string;
};

/** In-page navigation bar with brand, links and an optional button (shown only with a label and a path). */
export function NavbarBlock({ brand = 'My dApp', links = 'Home|/,About|/about', ctaLabel = '', ctaPath = '' }: NavbarBlockProps) {
  const items = parseList(links).map((s) => {
    const [label, ...rest] = s.split('|');
    return { label: (label ?? '').trim(), href: pageHref(rest.join('|')) };
  });
  const cta = pageHref(ctaPath);
  return (
    <nav
      className="sqc-widget dapp-navbar"
      data-sqc-widget="navbar"
      aria-label="Page sections"
      style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '1rem', padding: '0.75rem 1.25rem' }}
    >
      <strong style={{ marginRight: 'auto' }}>{brand}</strong>
      {items.map((l, i) => (
        <a key={`${i}-${l.label}-${l.href}`} href={l.href || undefined} style={{ color: 'inherit', textDecoration: 'none' }}>
          {l.label}
        </a>
      ))}
      {ctaLabel && cta ? (
        <a className="sqc-btn" href={cta}>
          {ctaLabel}
        </a>
      ) : null}
    </nav>
  );
}
