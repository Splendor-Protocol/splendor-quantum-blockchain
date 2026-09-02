import { useAccount, useBalance } from '@splendor/connect-react';

export function BalancePanel() {
  const { isConnected } = useAccount();
  const { balanceSPLD, loading, error, refresh } = useBalance(10_000);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Native balance</h2>
        <p className="msg">Connect a wallet to see SPLD.</p>
      </div>
    );
  }

  return (
    <div className="panel">
      <h2>Native balance (SPLD)</h2>
      <p className="balance-big mono">
        {loading && balanceSPLD == null ? '…' : (balanceSPLD ?? '0')}
      </p>
      <div className="btn-row">
        <button type="button" className="btn ghost" disabled={loading} onClick={() => void refresh()}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}
