import { useSyncExternalStore } from 'react';

export type AppToast = {
  id: number;
  kind: 'success' | 'error' | 'info';
  title: string;
  message?: string;
};

/** Toasts for app actions (saved, copied, deleted, wallet errors). Transaction toasts stay in connect-react. */
let toasts: AppToast[] = [];
let seq = 0;
const listeners = new Set<() => void>();
const MAX_TOASTS = 4;
const emit = () => listeners.forEach((l) => l());

export function pushToast(t: Omit<AppToast, 'id'>, ttlMs = t.kind === 'error' ? 8000 : 4000): number {
  const id = ++seq;
  // Same message twice in a row: keep one.
  toasts = [...toasts.filter((x) => !(x.title === t.title && x.message === t.message)), { ...t, id }].slice(-MAX_TOASTS);
  emit();
  if (ttlMs > 0 && typeof window !== 'undefined') window.setTimeout(() => dismissToast(id), ttlMs);
  return id;
}

export function dismissToast(id: number): void {
  const next = toasts.filter((t) => t.id !== id);
  if (next.length === toasts.length) return;
  toasts = next;
  emit();
}

/** Test helper. */
export function __clearToasts(): void {
  toasts = [];
  emit();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

/** Bottom-right stack; polite for info and success, assertive for errors. */
export function AppToasts() {
  const list = useSyncExternalStore(subscribe, () => toasts, () => toasts);
  return (
    <div className="pg-toasts" aria-live="polite" data-testid="app-toasts">
      {list.map((t) => (
        <div key={t.id} className={`pg-toast pg-toast--${t.kind}`} role={t.kind === 'error' ? 'alert' : 'status'}>
          <div>
            <strong>{t.title}</strong>
            {t.message ? <p>{t.message}</p> : null}
          </div>
          <button type="button" className="pg-icon-btn" aria-label="Dismiss" onClick={() => dismissToast(t.id)}>
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
