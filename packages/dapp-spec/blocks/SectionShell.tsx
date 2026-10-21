import type { ReactNode } from 'react';

export type SectionShellProps = {
  label?: string;
  children?: ReactNode;
};

/** Layout wrapper for a page section. As a leaf block it renders a labelled placeholder area. */
export function SectionShell({ label = 'Section', children }: SectionShellProps) {
  return (
    <section
      className="sqc-widget dapp-section-shell"
      data-sqc-widget="section"
      style={{ border: '1px dashed var(--sqc-border, #262b38)', borderRadius: 'var(--sqc-radius, 12px)', padding: '1.5rem', minHeight: 96 }}
    >
      {children ?? <p className="sqc-widget__msg" style={{ margin: 0, textAlign: 'center' }}>{label}: place content here</p>}
    </section>
  );
}
