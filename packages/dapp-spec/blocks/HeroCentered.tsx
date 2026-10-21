export type HeroCenteredProps = {
  heading?: string;
  text?: string;
  ctaLabel?: string;
  ctaPath?: string;
};

/** Centered marketing hero with optional CTA. */
export function HeroCentered({
  heading = 'Build on Splendor',
  text = '',
  ctaLabel = '',
  ctaPath = '',
}: HeroCenteredProps) {
  const href = ctaPath ? (ctaPath.startsWith('http') ? ctaPath : `#${ctaPath.startsWith('/') ? ctaPath : `/${ctaPath}`}`) : '';
  return (
    <section className="sqc-widget dapp-hero-centered" data-sqc-widget="hero-centered" style={{ textAlign: 'center', padding: '2.5rem 1rem' }}>
      <h1 style={{ fontSize: '2.2rem', margin: '0 0 0.75rem', fontFamily: 'var(--sqc-font-display, inherit)' }}>{heading}</h1>
      {text ? <p className="sqc-widget__msg" style={{ maxWidth: 520, margin: '0 auto 1.25rem' }}>{text}</p> : null}
      {ctaLabel ? (
        href ? (
          <a className="sqc-btn" href={href}>
            {ctaLabel}
          </a>
        ) : (
          <button type="button" className="sqc-btn" disabled>
            {ctaLabel}
          </button>
        )
      ) : null}
    </section>
  );
}
