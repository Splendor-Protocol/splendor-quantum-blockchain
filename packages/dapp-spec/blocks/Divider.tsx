export type DividerProps = { label?: string };

/** Horizontal rule, optionally labelled. */
export function Divider({ label = '' }: DividerProps) {
  if (!label) return <hr className="dapp-divider" data-sqc-widget="divider" style={{ border: 0, borderTop: '1px solid var(--sqc-border, #262b38)', margin: '1rem 0' }} />;
  return (
    <div data-sqc-widget="divider" style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', margin: '1rem 0', color: 'var(--sqc-text-muted, #9aa4b2)' }}>
      <span style={{ flex: 1, height: 1, background: 'var(--sqc-border, #262b38)' }} />
      <small>{label}</small>
      <span style={{ flex: 1, height: 1, background: 'var(--sqc-border, #262b38)' }} />
    </div>
  );
}
