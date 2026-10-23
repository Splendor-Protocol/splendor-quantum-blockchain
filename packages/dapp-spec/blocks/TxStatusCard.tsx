import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { TxResult, WidgetShell, useNetwork, usePendingTransactions } from '@splendor/connect-react';

export type TxStatusCardProps = {
  title?: string;
  /** Transaction hash to follow: 64 hex digits, with or without a 0x prefix. */
  hash?: string;
};

const HASH_RE = /^(0x)?[0-9a-fA-F]{64}$/;

/**
 * Shows whether a transaction sent from this browser is still pending. A transaction that is not
 * tracked here may be confirmed, failed or sent elsewhere, so it is reported as unknown.
 */
export function TxStatusCard({ title = 'Transaction status', hash = '' }: TxStatusCardProps) {
  const { network } = useNetwork();
  const { all } = usePendingTransactions({ toast: false });
  const clean = hash.trim();
  const valid = HASH_RE.test(clean);
  const bare = (h: string) => h.toLowerCase().replace(/^0x/, '');
  const isPending = valid ? all.some((p) => bare(p.hash) === bare(clean)) : false;
  const href = network && valid ? txExplorerUrl(network, clean) : null;

  return (
    <WidgetShell kind="tx-status" title={title} gate={false} status={isPending ? 'pending' : 'idle'}>
      {!clean ? (
        <p className="sqc-widget__msg">Set a transaction hash to follow.</p>
      ) : !valid ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          Not a valid transaction hash (expected 64 hex digits, optionally starting with 0x).
        </p>
      ) : isPending ? (
        <>
          <p style={{ fontSize: '1.25rem', fontWeight: 700, margin: '0.25rem 0' }}>Pending…</p>
          <p className="sqc-widget__msg" style={{ margin: 0 }}>
            Waiting for confirmation.
          </p>
          <TxResult hash={clean} confirmed={false} explorerUrl={href} />
        </>
      ) : (
        <>
          <p style={{ fontSize: '1.25rem', fontWeight: 700, margin: '0.25rem 0' }}>Unknown</p>
          <p className="sqc-widget__msg" style={{ margin: 0 }}>
            Not pending in this browser. It may be confirmed, failed or sent elsewhere; check the explorer.
          </p>
          <p className="sqc-widget__msg dapp-mono" style={{ margin: '0.5rem 0 0' }}>
            {clean}
          </p>
          {href ? (
            <p className="sqc-widget__msg" style={{ margin: '0.25rem 0 0' }}>
              <a href={href} target="_blank" rel="noreferrer">
                View in explorer
              </a>
            </p>
          ) : null}
        </>
      )}
    </WidgetShell>
  );
}
