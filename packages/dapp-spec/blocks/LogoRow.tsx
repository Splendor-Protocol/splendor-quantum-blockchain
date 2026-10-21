export type LogoRowProps = {
  title?: string;
  /** Partner names, comma-separated. Shown as text marks. */
  items?: string;
};

/** "Trusted by" strip of partner names. */
export function LogoRow({ title = 'Trusted by', items = 'Acme,Globex,Initech,Umbrella' }: LogoRowProps) {
  const list = items
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  return (
    <section className="sqc-widget dapp-logo-row" data-sqc-widget="logo-row" style={{ textAlign: 'center' }}>
      {title ? <p className="sqc-widget__msg" style={{ margin: '0 0 0.75rem' }}>{title}</p> : null}
      <div style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'center', gap: '0.75rem 2rem' }}>
        {list.map((name) => (
          <span key={name} style={{ fontWeight: 700, fontSize: '1.1rem', letterSpacing: '0.04em', opacity: 0.75 }}>
            {name}
          </span>
        ))}
      </div>
    </section>
  );
}
