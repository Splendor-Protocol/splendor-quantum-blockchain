import { useCallback, useEffect, useMemo, useSyncExternalStore } from 'react';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import {
  clearPendingTxs,
  loadPendingTxs,
  receiptOutcome,
  removePendingTx,
  subscribePendingTxs,
  type PendingTx,
} from '../../core/pendingTx.js';
import { useOptionalTxToast } from '../../ui/TxToastHost.js';
import { useConnectState } from '../context.js';
import { useReadConnection } from './core.js';

export type { PendingTx } from '../../core/pendingTx.js';

const EMPTY: PendingTx[] = [];

/**
 * Sent transactions that have not confirmed yet, persisted across reloads. While mounted,
 * entries for the current network are polled until the node reports a receipt; then they
 * are removed and (with a `<TxToastHost>`) a success or failure toast is shown.
 */
export function usePendingTransactions(opts: { pollMs?: number; toast?: boolean } = {}): {
  /** Pending entries on the current network, newest first. */
  pending: PendingTx[];
  /** Pending entries on every network. */
  all: PendingTx[];
  remove: (hash: string) => void;
  clear: () => void;
} {
  const pollMs = opts.pollMs ?? 5_000;
  const wantToast = opts.toast !== false;
  const all = useSyncExternalStore(subscribePendingTxs, loadPendingTxs, () => EMPTY);
  const network = useConnectState((s) => s.network);
  const conn = useReadConnection();
  const toast = useOptionalTxToast();
  const networkId = network?.id ?? null;
  const pending = useMemo(() => all.filter((p) => p.network === networkId), [all, networkId]);
  const hashesKey = pending.map((p) => p.hash).join(',');

  useEffect(() => {
    if (!conn || !network || !hashesKey || !pollMs) return;
    let cancelled = false;
    const check = async () => {
      for (const p of loadPendingTxs().filter((x) => x.network === network.id)) {
        if (cancelled) return;
        let receipt: unknown;
        try {
          receipt = await conn.getTxReceipt(p.hash);
        } catch {
          continue;
        }
        const outcome = receiptOutcome(receipt);
        if (!outcome || cancelled) continue;
        removePendingTx(p.hash);
        if (wantToast && toast) {
          const failed = outcome === 'failed';
          const err = failed ? (receipt as { error?: unknown }).error : undefined;
          toast.push({
            id: `pending-${p.hash}`,
            status: failed ? 'error' : 'success',
            title: `${p.summary} — ${failed ? 'failed' : 'confirmed'}`,
            hash: p.hash,
            explorerUrl: txExplorerUrl(network, p.hash),
            confirmed: !failed,
            errorMessage: failed ? (typeof err === 'string' && err.length < 200 ? err : 'Transaction failed') : null,
          });
        }
      }
    };
    void check();
    const id = setInterval(() => void check(), pollMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [conn, network, hashesKey, pollMs, wantToast, toast]);

  const remove = useCallback((hash: string) => removePendingTx(hash), []);
  const clear = useCallback(() => clearPendingTxs(), []);
  return { pending, all, remove, clear };
}
