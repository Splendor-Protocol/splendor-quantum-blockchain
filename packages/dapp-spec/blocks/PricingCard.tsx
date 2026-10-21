export type PricingCardProps = {
  name?: string;
  price?: string;
  period?: string;
  /** Feature lines, comma-separated. */
  features?: string;
  ctaLabel?: string;
  ctaPath?: string;
  highlighted?: boolean;
};

/** Single pricing tier card. */
export function PricingCard({
  name = 'Pro',
  price = '10 SPLD',
  period = '/ month',
  features = 'Unlimited pages,Custom theme,Priority support',
  ctaLabel = 'Choose plan',
  ctaPath = '',
  highlighted = false,
}: PricingCardProps) {
  const list = features
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  const href = ctaPath ? (ctaPath.startsWith('http') ? ctaPath : `#${ctaPath.startsWith('/') ? ctaPath : `/${ctaPath}`}`) : '';
  return (
    <div
      className="sqc-widget dapp-pricing-card"
      data-sqc-widget="pricing"
      style={{ padding: '1.5rem', borderColor: highlighted ? 'var(--sqc-accent, #7c5cff)' : undefined, borderWidth: highlighted ? 2 : undefined }}
    >
      <h3 className="sqc-widget__title">{name}</h3>
      <p style={{ margin: '0.25rem 0 1rem' }}>
        <span style={{ fontSize: '2rem', fontWeight: 700 }}>{price}</span>
        {period ? <span className="sqc-widget__msg"> {period}</span> : null}
      </p>
      <ul style={{ listStyle: 'none', margin: '0 0 1.25rem', padding: 0, display: 'grid', gap: '0.4rem' }}>
        {list.map((f) => (
          <li key={f}>
            <span aria-hidden="true" style={{ color: 'var(--sqc-success, #3ecf8e)', marginRight: '0.5rem' }}>
              ✓
            </span>
            {f}
          </li>
        ))}
      </ul>
      {ctaLabel ? (
        href ? (
          <a className="sqc-btn sqc-btn--block" href={href}>
            {ctaLabel}
          </a>
        ) : (
          <button type="button" className="sqc-btn sqc-btn--block" disabled>
            {ctaLabel}
          </button>
        )
      ) : null}
    </div>
  );
}
