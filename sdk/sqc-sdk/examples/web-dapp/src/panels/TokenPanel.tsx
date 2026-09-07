import { useMemo, useState } from 'react';
import { useAccount, useQRC20 } from '@splendor/connect-react';

export function TokenPanel() {
  const { isConnected } = useAccount();
  const [tokenInput, setTokenInput] = useState('');
  const [activeToken, setActiveToken] = useState<string | null>(null);
  const [to, setTo] = useState('');
  const [amount, setAmount] = useState('1');
  const [spender, setSpender] = useState('');
  const [approveAmt, setApproveAmt] = useState('1');
  const [lastHash, setLastHash] = useState<string | null>(null);

  const tokenAddr = useMemo(() => activeToken, [activeToken]);
  const { balance, tokenInfo, refresh, transfer, approve, loading, txStatus, error } = useQRC20(
    tokenAddr,
    12_000,
  );

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>QRC-20 token</h2>
        <p className="msg">Connect a wallet to read and write token contracts.</p>
      </div>
    );
  }

  const onLoad = () => {
    const t = tokenInput.trim();
    setActiveToken(t || null);
    setLastHash(null);
  };

  const onTransfer = async () => {
    setLastHash(null);
    try {
      const res = await transfer(to.trim(), amount.trim());
      setLastHash(res.hash ?? null);
    } catch {
      /* error via hook */
    }
  };

  const onApprove = async () => {
    setLastHash(null);
    try {
      const res = await approve(spender.trim(), approveAmt.trim());
      setLastHash(res.hash ?? null);
    } catch {
      /* error via hook */
    }
  };

  const name = tokenInfo && typeof tokenInfo.name === 'string' ? tokenInfo.name : null;
  const symbol = tokenInfo && typeof tokenInfo.symbol === 'string' ? tokenInfo.symbol : null;

  return (
    <div className="panel">
      <h2>QRC-20 — read & write</h2>
      <label htmlFor="tok-addr">Token address (SQ:…)</label>
      <input
        id="tok-addr"
        value={tokenInput}
        onChange={(e) => setTokenInput(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <div className="btn-row">
        <button type="button" onClick={onLoad} disabled={!tokenInput.trim()}>
          Load token
        </button>
        <button
          type="button"
          className="btn ghost"
          disabled={!activeToken || loading}
          onClick={() => void refresh()}
        >
          {loading ? 'Reading…' : 'Refresh reads'}
        </button>
      </div>

      {activeToken ? (
        <>
          <div className="divider" />
          <p className="meta">
            {name || symbol ? (
              <>
                {name ?? 'Token'}
                {symbol ? ` (${symbol})` : ''}
              </>
            ) : (
              'Token info'
            )}
          </p>
          <p className="mono msg" style={{ marginTop: 0 }}>
            Your balance: {balance ?? (loading ? '…' : '—')}
          </p>

          <div className="divider" />
          <h2>Transfer</h2>
          <label htmlFor="tok-to">To (SQ:…)</label>
          <input id="tok-to" value={to} onChange={(e) => setTo(e.target.value)} placeholder="SQ:…" />
          <label htmlFor="tok-amt">Amount (token units)</label>
          <input
            id="tok-amt"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="numeric"
          />
          <button
            type="button"
            disabled={txStatus === 'pending' || !to.trim()}
            onClick={() => void onTransfer()}
          >
            {txStatus === 'pending' ? 'Sending…' : 'Transfer'}
          </button>

          <div className="divider" />
          <h2>Approve</h2>
          <label htmlFor="tok-spender">Spender (SQ:…)</label>
          <input
            id="tok-spender"
            value={spender}
            onChange={(e) => setSpender(e.target.value)}
            placeholder="SQ:…"
          />
          <label htmlFor="tok-approve-amt">Amount</label>
          <input
            id="tok-approve-amt"
            value={approveAmt}
            onChange={(e) => setApproveAmt(e.target.value)}
            inputMode="numeric"
          />
          <button
            type="button"
            disabled={txStatus === 'pending' || !spender.trim()}
            onClick={() => void onApprove()}
          >
            {txStatus === 'pending' ? 'Approving…' : 'Approve'}
          </button>
        </>
      ) : null}

      {lastHash ? <p className="msg ok">Tx: {lastHash}</p> : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}
