import { pageHref } from './pageHref';

export type HeroGradientProps = {
  heading?: string;
  text?: string;
  ctaLabel?: string;
  ctaPath?: string;
};

/** Centered hero on the theme's hero gradient. The button renders only when both a label and a path are set. */
export function HeroGradient({ heading = 'Build on Splendor', text = '', ctaLabel = '', ctaPath = '' }: HeroGradientProps) {
  const href = pageHref(ctaPath);
  return (
    <section
      className="sqc-widget dapp-hero-gradient"
      data-sqc-widget="hero-gradient"
      style={{
        textAlign: 'center',
        padding: '4rem 1.5rem',
        background: 'var(--sqc-hero-gradient, var(--sqc-surface, #12151d))',
        borderRadius: 'var(--sqc-radius, 12px)',
      }}
    >
      <h1 style={{ fontSize: '2.6rem', margin: '0 0 0.75rem', fontFamily: 'var(--sqc-font-display, inherit)' }}>{heading}</h1>
      {text ? <p className="sqc-widget__msg" style={{ maxWidth: 560, margin: '0 auto 1.5rem', fontSize: '1.05rem' }}>{text}</p> : null}
      {ctaLabel && href ? (
        <a className="sqc-btn" href={href}>
          {ctaLabel}
        </a>
      ) : null}
    </section>
  );
}
