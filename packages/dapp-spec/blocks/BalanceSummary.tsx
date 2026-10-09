import { WidgetShell, useAccount, useBalance, useNetwork } from '@splendor/connect-react';

export type BalanceSummaryProps = {
  title?: string;
};

/** Network, address and SPLD balance of the connected wallet. */
export function BalanceSummary({ title = 'Overview' }: BalanceSummaryProps) {
  const { address } = useAccount();
  const { network } = useNetwork();
  const { balanceSPLD, loading, refresh } = useBalance();
  return (
    <WidgetShell kind="balance-summary" title={title}>
      <dl className="dapp-stats">
        <div>
          <dt>Network</dt>
          <dd>{network?.name ?? '—'}</dd>
        </div>
        <div>
          <dt>Balance</dt>
          <dd data-testid="dapp-balance">{balanceSPLD == null ? (loading ? 'Loading…' : '—') : `${balanceSPLD} SPLD`}</dd>
        </div>
        <div>
          <dt>Address</dt>
          <dd className="dapp-mono">{address ? `${address.slice(0, 10)}…${address.slice(-6)}` : '—'}</dd>
        </div>
      </dl>
      <button type="button" className="sqc-btn sqc-btn--block" onClick={() => void refresh()}>
        Refresh
      </button>
    </WidgetShell>
  );
}
