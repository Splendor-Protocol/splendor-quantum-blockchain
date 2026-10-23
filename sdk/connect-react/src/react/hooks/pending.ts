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
import { usePageVisible } from '../usePageVisible.js';
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
  // Hidden tabs do not poll; coming back checks right away.
  const visible = usePageVisible();

  useEffect(() => {
    if (!conn || !network || !hashesKey || !pollMs || !visible) return;
    let cancelled = false;
    const check = async () => {
      const list = loadPendingTxs().filter((x) => x.network === network.id);
      const receipts = await Promise.allSettled(list.map((p) => conn.getTxReceipt(p.hash)));
      for (let i = 0; i < list.length; i++) {
        if (cancelled) return;
        const p = list[i]!;
        const settled = receipts[i]!;
        if (settled.status !== 'fulfilled') continue;
        const receipt: unknown = settled.value;
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
  }, [conn, network, hashesKey, pollMs, wantToast, toast, visible]);

  const remove = useCallback((hash: string) => removePendingTx(hash), []);
  const clear = useCallback(() => clearPendingTxs(), []);
  return { pending, all, remove, clear };
}
