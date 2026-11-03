import type { ReactNode } from 'react';

export type NoticeTone = 'info' | 'warning' | 'error' | 'success';

/**
 * One look for every inline message. Errors and warnings are announced (`alert`), info and success
 * politely (`status`), so a screen reader hears a failure at once and a confirmation when idle.
 */
export function Notice({
  tone = 'info',
  title,
  children,
  action,
  testId,
}: {
  tone?: NoticeTone;
  title?: ReactNode;
  children?: ReactNode;
  /** Buttons or links on the right (Retry, Dismiss…). */
  action?: ReactNode;
  testId?: string;
}) {
  return (
    <div className={`pg-notice pg-notice--${tone}`} role={tone === 'error' || tone === 'warning' ? 'alert' : 'status'} data-testid={testId}>
      <div className="pg-notice-body">
        {title ? <strong>{title}</strong> : null}
        {children ? <div>{children}</div> : null}
      </div>
      {action ? <div className="pg-notice-actions">{action}</div> : null}
    </div>
  );
}

/** Nothing here yet: what this place is for, and the one thing to do next. */
export function EmptyState({ title, children, action, testId }: { title: ReactNode; children?: ReactNode; action?: ReactNode; testId?: string }) {
  return (
    <div className="pg-empty-state" data-testid={testId}>
      <strong>{title}</strong>
      {children ? <p className="pg-muted">{children}</p> : null}
      {action ? <div className="pg-row">{action}</div> : null}
    </div>
  );
}

/** Placeholder lines while something loads; announced once as "Loading …". */
export function Skeleton({ lines = 3, label = 'Loading' }: { lines?: number; label?: string }) {
  return (
    <div className="pg-skeleton" role="status" aria-label={label}>
      {Array.from({ length: lines }, (_, i) => (
        <span key={i} className="pg-skeleton-line" style={{ width: `${92 - ((i * 17) % 40)}%` }} />
      ))}
    </div>
  );
}
