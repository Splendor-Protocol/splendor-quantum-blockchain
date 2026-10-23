import { useEffect, useRef, useState } from 'react';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { WidgetShell, useNetwork, usePendingTransactions, type PendingTx } from '@splendor/connect-react';

export type TxHistoryTableProps = {
  title?: string;
  /** List transactions from every network instead of only the current one. */
  allNetworks?: boolean;
  /** Maximum rows. */
  limit?: number;
};

const cell = { padding: '0.4rem 0.5rem' } as const;

function Chip({ done }: { done: boolean }) {
  return (
    <span
      title={done ? 'No longer pending: it confirmed or failed. Open the explorer to check.' : 'Waiting for confirmation'}
      style={{
        display: 'inline-block',
        padding: '0.1rem 0.55rem',
        borderRadius: 999,
        fontSize: '0.75rem',
        fontWeight: 600,
        border: '1px solid currentColor',
        color: done ? 'var(--sqc-success, #3ecf8e)' : 'var(--sqc-accent, #f0c14a)',
      }}
    >
      {done ? 'Done' : 'Pending'}
    </span>
  );
}

/**
 * Table of transactions sent from this browser. Rows start as "Pending"; once the node reports a
 * receipt they leave the pending list and stay in the table (as "Done") until the page reloads.
 */
export function TxHistoryTable({ title = 'Recent transactions', allNetworks = false, limit = 10 }: TxHistoryTableProps) {
  const { network } = useNetwork();
  const { pending, all } = usePendingTransactions({ toast: false });
  const live = allNetworks ? all : pending;
  const [finished, setFinished] = useState<PendingTx[]>([]);
  const prev = useRef<{ key: string; rows: PendingTx[] }>({ key: '', rows: [] });
  const scopeKey = `${allNetworks ? '*' : (network?.id ?? '')}`;

  useEffect(() => {
    const before = prev.current;
    prev.current = { key: scopeKey, rows: live };
    if (before.key !== scopeKey) {
      setFinished([]);
      return;
    }
    const now = new Set(live.map((p) => p.hash));
    const gone = before.rows.filter((p) => !now.has(p.hash));
    if (gone.length) setFinished((f) => [...gone.filter((g) => !f.some((x) => x.hash === g.hash)), ...f].slice(0, 50));
  }, [live, scopeKey]);

  const liveHashes = new Set(live.map((p) => p.hash));
  const rows = [
    ...live.map((p) => ({ p, done: false })),
    ...finished.filter((p) => !liveHashes.has(p.hash)).map((p) => ({ p, done: true })),
  ].slice(0, Math.max(1, limit));

  return (
    <WidgetShell kind="tx-history" title={title} gate={false}>
      {rows.length === 0 ? (
        <p className="sqc-widget__msg">No transactions yet. Transactions you send from this page show up here.</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={cell}>When</th>
                <th style={cell}>What</th>
                <th style={cell}>Status</th>
                <th style={cell}>Hash</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ p, done }) => {
                const href = network && p.network === network.id ? txExplorerUrl(network, p.hash) : null;
                return (
                  <tr key={p.hash} style={{ borderTop: '1px solid var(--sqc-border, #262b38)' }}>
                    <td style={{ ...cell, whiteSpace: 'nowrap' }}>{new Date(p.ts).toLocaleTimeString()}</td>
                    <td style={cell}>{p.summary}</td>
                    <td style={cell}>
                      <Chip done={done} />
                    </td>
                    <td style={cell} className="dapp-mono">
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
