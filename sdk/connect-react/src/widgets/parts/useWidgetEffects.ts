import { useCallback, useEffect, useRef } from 'react';
import { useOptionalTxToast } from '../../ui/TxToastHost.js';
import { connectErrorMessage } from '../../core/controller.js';
import type { WidgetStatus } from '../types.js';

/** Calls `onStatusChange` whenever `status` changes after mount. */
export function useStatusChange(status: WidgetStatus, onStatusChange?: (s: WidgetStatus) => void): void {
  const cb = useRef(onStatusChange);
  cb.current = onStatusChange;
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    cb.current?.(status);
  }, [status]);
}

/** Pending / success / error toasts for one widget action; no-ops without a `<TxToastHost>` or when disabled. */
export function useWidgetToast(enabled: boolean) {
  const api = useOptionalTxToast();
  const pendingId = useRef<string | null>(null);
  const active = enabled && api ? api : null;

  const clearPending = useCallback(() => {
    if (pendingId.current && active) active.dismiss(pendingId.current);
    pendingId.current = null;
  }, [active]);

  const pending = useCallback(
    (title: string) => {
      if (!active) return;
      clearPending();
      pendingId.current = active.push({
        title,
        status: 'pending',
        hash: null,
        explorerUrl: null,
        errorMessage: null,
      });
    },
    [active, clearPending],
  );

  const success = useCallback(
    (title: string, tx: { hash?: string | null; explorerUrl?: string | null; confirmed?: boolean } = {}) => {
      if (!active) return;
      clearPending();
      active.push({
        title,
        status: 'success',
        hash: tx.hash ?? null,
        explorerUrl: tx.explorerUrl ?? null,
        confirmed: tx.confirmed,
        errorMessage: null,
      });
    },
    [active, clearPending],
  );

  const error = useCallback(
    (title: string, err: unknown) => {
      if (!active) return;
      clearPending();
      active.push({
        title,
        status: 'error',
        hash: null,
        explorerUrl: null,
        errorMessage: connectErrorMessage(err),
      });
    },
    [active, clearPending],
  );

  return { pending, success, error };
}

/**
 * Clear the last result/error when the account or network changes, so a hash or error from
 * the previous context is never shown under the new one. Skipped while a send is pending.
 */
export function useResetOnContextChange(reset: () => void, pending: boolean, ...context: unknown[]): void {
  const resetRef = useRef(reset);
  resetRef.current = reset;
  const pendingRef = useRef(pending);
  pendingRef.current = pending;
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    if (!pendingRef.current) resetRef.current();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, context);
}

export function toError(e: unknown): Error {
  return e instanceof Error ? e : new Error(String(e));
}
