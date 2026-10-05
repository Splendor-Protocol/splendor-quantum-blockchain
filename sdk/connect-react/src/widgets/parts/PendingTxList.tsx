import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { useConnectState } from '../../react/context.js';
import { usePendingTransactions } from '../../react/hooks/pending.js';

export type PendingTxListProps = {
  /** Poll interval for receipts (ms). Default 5000. */
  pollMs?: number;
  /** Text when nothing is pending; omit to render nothing. */
  emptyText?: string;
  className?: string;
};

/** Unconfirmed transactions on the current network, kept across reloads. */
export function PendingTxList({ pollMs, emptyText, className }: PendingTxListProps) {
  const { pending, remove } = usePendingTransactions({ pollMs });
  const network = useConnectState((s) => s.network);
  if (!pending.length) {
    return emptyText ? <p className={['sqc-pending-tx', className].filter(Boolean).join(' ')}>{emptyText}</p> : null;
  }
  return (
    <ul className={['sqc-pending-tx', className].filter(Boolean).join(' ')} aria-label="Pending transactions">
      {pending.map((p) => {
        const href = network ? txExplorerUrl(network, p.hash) : null;
        return (
          <li key={p.hash} className="sqc-pending-tx__item" data-testid="sqc-pending-tx">
            <span className="sqc-pending-tx__summary">{p.summary}</span>
            {href ? (
              <a className="sqc-pending-tx__hash" href={href} target="_blank" rel="noopener noreferrer">
                {p.hash.slice(0, 10)}…
              </a>
            ) : (
              <code className="sqc-pending-tx__hash">{p.hash.slice(0, 10)}…</code>
            )}
            <button
              type="button"
              className="sqc-copy"
              aria-label={`Stop watching ${p.hash.slice(0, 10)}`}
              onClick={() => remove(p.hash)}
            >
              ×
            </button>
          </li>
        );
      })}
    </ul>
  );
}
