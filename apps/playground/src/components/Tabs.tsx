import { useRef, type KeyboardEvent, type ReactNode } from 'react';

export type TabItem<T extends string> = { id: T; label: string; testId?: string };

/**
 * WAI-ARIA tabs: one tab stop for the list, arrow keys / Home / End move between tabs, and the
 * panel is labelled by its tab.
 */
export function Tabs<T extends string>({
  idPrefix,
  label,
  tabs,
  value,
  onChange,
  children,
}: {
  idPrefix: string;
  label: string;
  tabs: readonly TabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  children: ReactNode;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = tabs.findIndex((t) => t.id === value);
    const next =
      e.key === 'ArrowRight' ? (i + 1) % tabs.length
      : e.key === 'ArrowLeft' ? (i - 1 + tabs.length) % tabs.length
      : e.key === 'Home' ? 0
      : e.key === 'End' ? tabs.length - 1
      : -1;
    if (next < 0) return;
    e.preventDefault();
    onChange(tabs[next]!.id);
    refs.current[next]?.focus();
  };
  return (
    <>
      <div className="pg-tabs-inline" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
        {tabs.map((t, i) => (
          <button
            key={t.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={`${idPrefix}-tab-${t.id}`}
            aria-selected={value === t.id}
            aria-controls={`${idPrefix}-panel`}
            tabIndex={value === t.id ? 0 : -1}
            data-testid={t.testId}
            onClick={() => onChange(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${idPrefix}-panel`} aria-labelledby={`${idPrefix}-tab-${value}`} className="pg-stack">
        {children}
      </div>
    </>
  );
}
