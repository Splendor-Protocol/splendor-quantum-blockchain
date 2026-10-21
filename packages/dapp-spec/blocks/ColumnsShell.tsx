import type { ReactNode } from 'react';

export type ColumnsShellProps = {
  /** Number of columns: 2, 3 or 4. */
  cols?: string | number;
  children?: ReactNode;
};

/** Equal-width columns wrapper. As a leaf block it renders `cols` empty placeholder cells. */
export function ColumnsShell({ cols = '3', children }: ColumnsShellProps) {
  const n = Math.min(4, Math.max(2, Number(cols) || 3));
  return (
    <div
      className="sqc-widget dapp-columns-shell"
      data-sqc-widget="columns"
      style={{ display: 'grid', gridTemplateColumns: `repeat(${n}, minmax(0, 1fr))`, gap: '1rem' }}
    >
      {children ??
        Array.from({ length: n }, (_, i) => (
          <div
            key={i}
            className="sqc-widget__msg"
            style={{
              border: '1px dashed var(--sqc-border, #262b38)',
              borderRadius: 'var(--sqc-radius, 12px)',
              minHeight: 72,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            Column {i + 1}
          </div>
        ))}
    </div>
  );
}
