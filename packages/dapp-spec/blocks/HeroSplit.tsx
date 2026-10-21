export type HeroSplitProps = {
  heading?: string;
  text?: string;
  ctaLabel?: string;
  ctaPath?: string;
  /** https image shown beside the text. */
  imageUrl?: string;
  /** Put the image on the left. */
  reverse?: boolean;
};

/** Two-column hero: text and call-to-action next to an image. */
export function HeroSplit({
  heading = 'Build on Splendor',
  text = '',
  ctaLabel = '',
  ctaPath = '',
  imageUrl = '',
  reverse = false,
}: HeroSplitProps) {
  const href = ctaPath ? (ctaPath.startsWith('http') ? ctaPath : `#${ctaPath.startsWith('/') ? ctaPath : `/${ctaPath}`}`) : '';
  const safe = /^https:\/\//i.test(imageUrl) ? imageUrl : '';
  return (
    <section
      className="sqc-widget dapp-hero-split"
      data-sqc-widget="hero-split"
      style={{
        display: 'flex',
        flexDirection: reverse ? 'row-reverse' : 'row',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: '2rem',
        padding: '2rem 1.5rem',
      }}
    >
      <div style={{ flex: '1 1 260px' }}>
        <h1 style={{ fontSize: '2.1rem', margin: '0 0 0.75rem', fontFamily: 'var(--sqc-font-display, inherit)' }}>{heading}</h1>
        {text ? <p className="sqc-widget__msg" style={{ margin: '0 0 1.25rem' }}>{text}</p> : null}
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
      </div>
      <div style={{ flex: '1 1 260px' }}>
        {safe ? (
          <img src={safe} alt="" style={{ width: '100%', display: 'block', borderRadius: 'var(--sqc-radius, 12px)' }} />
        ) : (
          <div
            className="sqc-widget__msg"
            style={{
              minHeight: 180,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              background: 'var(--sqc-surface, #12151d)',
              borderRadius: 'var(--sqc-radius, 12px)',
            }}
          >
            Set an https image URL
          </div>
        )}
      </div>
    </section>
  );
}
