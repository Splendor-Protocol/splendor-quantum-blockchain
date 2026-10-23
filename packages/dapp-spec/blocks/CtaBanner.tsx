import { pageHref } from './pageHref';

export type CtaBannerProps = {
  heading?: string;
  text?: string;
  buttonLabel?: string;
  buttonPath?: string;
};

/** Full-width call-to-action strip. The button renders only when both a label and a path are set. */
export function CtaBanner({
  heading = 'Ready?',
  text = '',
  buttonLabel = 'Get started',
  buttonPath = '',
}: CtaBannerProps) {
  const href = pageHref(buttonPath);
  return (
    <section
      className="sqc-widget dapp-cta-banner"
      data-sqc-widget="cta-banner"
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '1rem',
        padding: '1.25rem 1.5rem',
        background: 'var(--sqc-hero-gradient, var(--sqc-surface, #12151d))',
        borderRadius: 'var(--sqc-radius, 12px)',
      }}
    >
      <div>
        <h3 style={{ margin: 0 }}>{heading}</h3>
        {text ? <p className="sqc-widget__msg" style={{ margin: '0.35rem 0 0' }}>{text}</p> : null}
      </div>
      {buttonLabel && href ? (
        <a className="sqc-btn" href={href}>
          {buttonLabel}
        </a>
      ) : null}
    </section>
  );
}
