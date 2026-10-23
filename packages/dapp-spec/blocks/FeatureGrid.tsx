import { parseList } from './parseList';

export type FeatureGridProps = {
  title?: string;
  /** `Title|Description` items, comma-separated (or one per line when the value contains a newline). */
  items?: string;
};

function parseItems(items: string): { title: string; text: string }[] {
  return parseList(items).map((s) => {
    const [title, ...rest] = s.split('|');
    return { title: (title ?? '').trim(), text: rest.join('|').trim() };
  });
}

/** Three-up feature cards from a compact items string. */
export function FeatureGrid({ title = 'Features', items = 'Fast|Ship quickly,Secure|Post-quantum,Open|Export code' }: FeatureGridProps) {
  const list = parseItems(items);
  return (
    <section className="sqc-widget dapp-feature-grid" data-sqc-widget="feature-grid">
      {title ? <h3 className="sqc-widget__title">{title}</h3> : null}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: '1rem' }}>
        {list.map((f, i) => (
          <div key={`${i}-${f.title}`} className="sqc-widget" style={{ padding: '1rem' }}>
            <strong>{f.title}</strong>
            {f.text ? <p className="sqc-widget__msg" style={{ margin: '0.4rem 0 0' }}>{f.text}</p> : null}
          </div>
        ))}
      </div>
    </section>
  );
}
