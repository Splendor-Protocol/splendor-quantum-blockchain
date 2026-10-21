import type { ReactNode } from 'react';

export type CardShellProps = {
  title?: string;
  children?: ReactNode;
};

/** Card wrapper. As a leaf block it renders a titled card with placeholder content. */
export function CardShell({ title = 'Card', children }: CardShellProps) {
  return (
    <div className="sqc-widget dapp-card-shell" data-sqc-widget="card" style={{ padding: '1.25rem' }}>
      {title ? <h3 className="sqc-widget__title">{title}</h3> : null}
      {children ?? <p className="sqc-widget__msg" style={{ margin: 0 }}>Card content goes here.</p>}
    </div>
  );
}
