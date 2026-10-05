/**
 * Shared mutation state for connect-react tx hooks.
 */
import { useCallback, useContext, useRef, useState } from 'react';
import { BridgeError } from '@splendor/sqc-sdk';
import { txExplorerUrl, type NetworkConfig } from '@splendor/sqc-sdk/networks';
import { connectErrorMessage } from '../core/controller.js';
import { addPendingTx, isConfirmedResult } from '../core/pendingTx.js';
import { ControllerContext } from './context.js';

export type TxHookStatus = 'idle' | 'pending' | 'success' | 'error';

/** Describes a send for the pending-transaction list. */
export type TxMeta = { kind: string; summary: string };

/**
 * Ref-based single-flight lock. A second call while one is running rejects with BUSY
 * instead of asking the wallet to sign twice (double click, Enter + click, …).
 */
export function useSingleFlight(): <T>(fn: () => Promise<T>) => Promise<T> {
  const busy = useRef(false);
  return useCallback(async <T,>(fn: () => Promise<T>): Promise<T> => {
    if (busy.current) {
      throw BridgeError.busy('A transaction is already being signed — wait for it to finish');
    }
    busy.current = true;
    try {
      return await fn();
    } finally {
      busy.current = false;
    }
  }, []);
}

/**
 * Returns a recorder that stores an unconfirmed send in the pending list
 * (`sqc.connect.pendingTx`) so `usePendingTransactions` can finish watching it after a reload.
 */
export function useRecordPendingTx(): (res: unknown, meta: TxMeta) => void {
  const ctrl = useContext(ControllerContext);
  return useCallback(
    (res: unknown, meta: TxMeta) => {
      if (!ctrl || ctrl.isStub || isConfirmedResult(res)) return;
      const hash = res && typeof res === 'object' ? (res as { hash?: unknown }).hash : undefined;
      if (typeof hash !== 'string' || !hash) return;
      const state = ctrl.getState();
      const network = state.network?.id;
      if (!network) return;
      addPendingTx({
        hash,
        kind: meta.kind,
        summary: meta.summary.slice(0, 200),
        from: state.account?.address,
        network,
      });
    },
    [ctrl],
  );
}

export function useTxAction(network?: NetworkConfig | null): {
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  /** Safe UI copy — never raw RPC bodies. */
  errorMessage: string | null;
  reset: () => void;
  run: <T extends { hash?: string }>(fn: () => Promise<T>, meta?: TxMeta) => Promise<T>;
} {
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [explorerUrl, setExplorerUrl] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setExplorerUrl(null);
    setError(null);
  }, []);

  const run = useCallback(
    <T extends { hash?: string }>(fn: () => Promise<T>, meta?: TxMeta): Promise<T> =>
      singleFlight(async () => {
        setStatus('pending');
        setError(null);
        setHash(null);
        setExplorerUrl(null);
        try {
          const res = await fn();
          const h = res.hash ?? null;
          setHash(h);
          setExplorerUrl(network && h ? txExplorerUrl(network, h) : null);
          setStatus('success');
          recordPending(res, meta ?? { kind: 'tx', summary: 'Transaction' });
          return res;
        } catch (e) {
          const err = e instanceof Error ? e : new Error(String(e));
          setError(err);
          setStatus('error');
          throw err;
        }
      }),
    [network, singleFlight, recordPending],
  );

  return {
    status,
    hash,
    explorerUrl,
    error,
    errorMessage: error ? connectErrorMessage(error) : null,
    reset,
    run,
  };
}
