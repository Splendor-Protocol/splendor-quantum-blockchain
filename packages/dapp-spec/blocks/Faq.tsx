import { parseList } from './parseList';

export type FaqProps = {
  title?: string;
  /** `Question|Answer` items, comma-separated (or one per line when the value contains a newline). */
  items?: string;
};

function parseItems(items: string): { q: string; a: string }[] {
  return parseList(items).map((s) => {
    const [q, ...rest] = s.split('|');
    return { q: (q ?? '').trim(), a: rest.join('|').trim() };
  });
}

/** Accordion-style FAQ list. */
export function Faq({ title = 'FAQ', items = 'What is this?|A Splendor dApp.' }: FaqProps) {
  const list = parseItems(items);
  return (
    <section className="sqc-widget dapp-faq" data-sqc-widget="faq">
      {title ? <h3 className="sqc-widget__title">{title}</h3> : null}
      <div style={{ display: 'grid', gap: '0.5rem' }}>
        {list.map((item, i) => (
          <details key={`${i}-${item.q}`} style={{ border: '1px solid var(--sqc-border, #262b38)', borderRadius: 8, padding: '0.5rem 0.75rem' }}>
            <summary style={{ cursor: 'pointer', fontWeight: 600 }}>{item.q}</summary>
            <p className="sqc-widget__msg" style={{ margin: '0.5rem 0 0' }}>{item.a}</p>
          </details>
        ))}
      </div>
    </section>
  );
}
