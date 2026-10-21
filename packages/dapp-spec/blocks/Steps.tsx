export type StepsProps = {
  title?: string;
  /** `Title|Description` items, comma-separated. */
  items?: string;
};

function parseItems(items: string): { title: string; text: string }[] {
  return items
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const [title, ...rest] = s.split('|');
      return { title: (title ?? '').trim(), text: rest.join('|').trim() };
    });
}

/** Numbered how-it-works list. */
export function Steps({ title = 'How it works', items = 'Connect|Link your wallet,Choose|Pick an option,Confirm|Sign the transaction' }: StepsProps) {
  const list = parseItems(items);
  return (
    <section className="sqc-widget dapp-steps" data-sqc-widget="steps">
      {title ? <h3 className="sqc-widget__title">{title}</h3> : null}
      <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.9rem' }}>
        {list.map((s, i) => (
          <li key={`${i}-${s.title}`} style={{ display: 'flex', gap: '0.8rem', alignItems: 'flex-start' }}>
            <span
              aria-hidden="true"
              style={{
                flex: '0 0 auto',
                width: 28,
                height: 28,
                borderRadius: '50%',
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontWeight: 700,
                fontSize: '0.85rem',
                border: '1px solid var(--sqc-accent, #7c5cff)',
                color: 'var(--sqc-accent, #7c5cff)',
              }}
            >
              {i + 1}
            </span>
            <div>
              <strong>{s.title}</strong>
              {s.text ? <p className="sqc-widget__msg" style={{ margin: '0.2rem 0 0' }}>{s.text}</p> : null}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}
