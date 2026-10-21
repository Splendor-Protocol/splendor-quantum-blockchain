import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { TxResult, WidgetShell, useNetwork, usePendingTransactions } from '@splendor/connect-react';

export type TxStatusCardProps = {
  title?: string;
  /** Transaction hash to follow. */
  hash?: string;
};

/** Shows whether a transaction sent from this browser is still pending. */
export function TxStatusCard({ title = 'Transaction status', hash = '' }: TxStatusCardProps) {
  const { network } = useNetwork();
  const { all } = usePendingTransactions({ toast: false });
  const clean = hash.trim();
  const isPending = clean ? all.some((p) => p.hash.toLowerCase() === clean.toLowerCase()) : false;
  const href = network && clean ? txExplorerUrl(network, clean) : null;

  return (
    <WidgetShell kind="tx-status" title={title} gate={false} status={isPending ? 'pending' : 'idle'}>
      {!clean ? (
        <p className="sqc-widget__msg">Set a transaction hash to follow.</p>
      ) : (
        <>
          <p style={{ fontSize: '1.25rem', fontWeight: 700, margin: '0.25rem 0' }}>{isPending ? 'Pending…' : 'Not pending'}</p>
          <p className="sqc-widget__msg" style={{ margin: 0 }}>
            {isPending ? 'Waiting for confirmation.' : 'Confirmed, or not sent from this browser.'}
          </p>
          <TxResult hash={clean} confirmed={!isPending} explorerUrl={href} />
        </>
      )}
    </WidgetShell>
  );
}
