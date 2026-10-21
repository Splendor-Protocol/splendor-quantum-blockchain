import type { ReactNode } from 'react';

export type RowShellProps = {
  label?: string;
  children?: ReactNode;
};

/** Horizontal layout wrapper. As a leaf block it renders a labelled placeholder row. */
export function RowShell({ label = 'Row', children }: RowShellProps) {
  return (
    <div
      className="sqc-widget dapp-row-shell"
      data-sqc-widget="row"
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1rem',
        border: '1px dashed var(--sqc-border, #262b38)',
        borderRadius: 'var(--sqc-radius, 12px)',
        padding: '1rem',
        minHeight: 72,
      }}
    >
      {children ?? <p className="sqc-widget__msg" style={{ margin: 0 }}>{label}: items sit side by side</p>}
    </div>
  );
}
