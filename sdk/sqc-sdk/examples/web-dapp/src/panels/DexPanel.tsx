import { useState } from 'react';
import { useAccount, useDex } from '@splendor/connect-react';

export function DexPanel() {
  const { isConnected } = useAccount();
  const [poolInput, setPoolInput] = useState('');
  const [pool, setPool] = useState<string | null>(null);
  const [tokenInSide, setTokenInSide] = useState<'0' | '1'>('0');
  const [amountIn, setAmountIn] = useState('100');
  const [minOut, setMinOut] = useState('1');
  const [reservesText, setReservesText] = useState<string | null>(null);

  const { swap, reserves, status, hash, explorerUrl, error, reset } = useDex(pool);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>DEX swap</h2>
        <p className="msg">Connect a wallet to swap on a pool.</p>
      </div>
    );
  }

  const onLoad = () => {
    reset();
    setPool(poolInput.trim() || null);
    setReservesText(null);
  };

  const onReserves = async () => {
    try {
      const r = await reserves();
      setReservesText(JSON.stringify(r));
    } catch {
      /* error via hook */
    }
  };

  const onSwap = async () => {
    try {
      await swap({
        tokenIn: tokenInSide === '1' ? 1 : 0,
        amountIn: amountIn.trim(),
        minAmountOut: minOut.trim() || undefined,
      });
    } catch {
      /* error via hook */
    }
  };

  return (
    <div className="panel">
      <h2>DEX — swap</h2>
      <label htmlFor="dex-pool">Pool address (SQ:…)</label>
      <input
        id="dex-pool"
        value={poolInput}
        onChange={(e) => setPoolInput(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <div className="btn-row">
        <button type="button" onClick={onLoad} disabled={!poolInput.trim()}>
          Load pool
        </button>
        <button type="button" className="btn ghost" disabled={!pool} onClick={() => void onReserves()}>
          Read reserves
        </button>
      </div>
      {reservesText ? <p className="msg mono">{reservesText}</p> : null}
      {pool ? (
        <>
          <div className="divider" />
          <label htmlFor="dex-token-in">Token in side (0 = token0, 1 = token1)</label>
          <select
            id="dex-token-in"
            value={tokenInSide}
            onChange={(e) => setTokenInSide(e.target.value === '1' ? '1' : '0')}
          >
            <option value="0">0 — token0</option>
            <option value="1">1 — token1</option>
          </select>
          <label htmlFor="dex-amt">Amount in (raw units)</label>
          <input
            id="dex-amt"
            value={amountIn}
            onChange={(e) => setAmountIn(e.target.value)}
            inputMode="numeric"
          />
          <label htmlFor="dex-min">Min amount out</label>
          <input
            id="dex-min"
            value={minOut}
            onChange={(e) => setMinOut(e.target.value)}
            inputMode="numeric"
          />
          <button type="button" disabled={status === 'pending'} onClick={() => void onSwap()}>
            {status === 'pending' ? 'Swapping…' : 'Swap'}
          </button>
        </>
      ) : null}
      {hash ? (
        <p className="msg ok">
          Tx: {hash}
          {explorerUrl ? (
            <>
              {' · '}
              <a href={explorerUrl} target="_blank" rel="noreferrer">
                Explorer
              </a>
            </>
          ) : null}
        </p>
      ) : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}
