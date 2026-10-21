import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { WidgetShell, useNetwork, usePendingTransactions } from '@splendor/connect-react';

export type TxHistoryTableProps = {
  title?: string;
  /** List transactions from every network instead of only the current one. */
  allNetworks?: boolean;
  /** Maximum rows. */
  limit?: number;
};

/** Table of transactions sent from this browser that are still unconfirmed. */
export function TxHistoryTable({ title = 'Recent transactions', allNetworks = false, limit = 10 }: TxHistoryTableProps) {
  const { network } = useNetwork();
  const { pending, all } = usePendingTransactions({ toast: false });
  const rows = (allNetworks ? all : pending).slice(0, Math.max(1, limit));

  return (
    <WidgetShell kind="tx-history" title={title} gate={false}>
      {rows.length === 0 ? (
        <p className="sqc-widget__msg">No pending transactions.</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={{ padding: '0.4rem 0.5rem' }}>When</th>
                <th style={{ padding: '0.4rem 0.5rem' }}>What</th>
                <th style={{ padding: '0.4rem 0.5rem' }}>Hash</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const href = network && p.network === network.id ? txExplorerUrl(network, p.hash) : null;
                return (
                  <tr key={p.hash} style={{ borderTop: '1px solid var(--sqc-border, #262b38)' }}>
                    <td style={{ padding: '0.4rem 0.5rem', whiteSpace: 'nowrap' }}>{new Date(p.ts).toLocaleTimeString()}</td>
                    <td style={{ padding: '0.4rem 0.5rem' }}>{p.summary}</td>
                    <td style={{ padding: '0.4rem 0.5rem' }} className="dapp-mono">
                      {href ? (
                        <a href={href} target="_blank" rel="noreferrer">
                          {p.hash.slice(0, 10)}…
                        </a>
                      ) : (
                        `${p.hash.slice(0, 10)}…`
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </WidgetShell>
  );
}
