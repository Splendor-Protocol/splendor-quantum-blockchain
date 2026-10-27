import { useCallback, useRef, useState, type ReactNode } from 'react';
import { ModalDialog } from './ModalDialog';

export type ConfirmOptions = {
  title: string;
  message: ReactNode;
  /** Label of the button that goes ahead, e.g. "Delete project". */
  confirmLabel: string;
  /** Styles the confirm button as destructive. */
  danger?: boolean;
};

type Pending = ConfirmOptions & { resolve: (ok: boolean) => void };

/**
 * A focus-trapped replacement for `window.confirm`. Render `dialog` somewhere in the tree and
 * `await confirm({...})`; it resolves false on Cancel or Escape. Cancel has focus first, so Enter
 * does not destroy anything by accident.
 */
export function useConfirm(): [dialog: ReactNode, confirm: (options: ConfirmOptions) => Promise<boolean>] {
  const [pending, setPending] = useState<Pending | null>(null);
  const pendingRef = useRef<Pending | null>(null);
  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        // A second request while one is open answers the first with "no".
        pendingRef.current?.resolve(false);
        const next = { ...options, resolve };
        pendingRef.current = next;
        setPending(next);
      }),
    [],
  );
  const done = (ok: boolean) => {
    const p = pendingRef.current;
    pendingRef.current = null;
    setPending(null);
    p?.resolve(ok);
  };
  const dialog = pending ? (
    <ModalDialog label={pending.title} onClose={() => done(false)} className="pg-modal" testId="confirm-dialog">
      <div className="pg-modal-panel pg-confirm">
        <h2 style={{ margin: 0, fontSize: '1.1rem' }}>{pending.title}</h2>
        <div className="pg-muted">{pending.message}</div>
        <div className="pg-row" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" data-testid="confirm-cancel" onClick={() => done(false)}>
            Cancel
          </button>
          <button type="button" className={`pg-btn pg-btn-sm${pending.danger ? ' pg-btn-danger' : ''}`} data-testid="confirm-ok" onClick={() => done(true)}>
            {pending.confirmLabel}
          </button>
        </div>
      </div>
    </ModalDialog>
  ) : null;
  return [dialog, confirm];
}
