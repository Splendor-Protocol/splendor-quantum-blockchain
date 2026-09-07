import { useState } from 'react';
import { useAccount, useExchange } from '@splendor/connect-react';

export function ExchangePanel() {
  const { isConnected } = useAccount();
  const [addrInput, setAddrInput] = useState('');
  const [exchange, setExchange] = useState<string | null>(null);
  const [infoText, setInfoText] = useState<string | null>(null);

  const { info, status, error, reset } = useExchange(exchange);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Exchange</h2>
        <p className="msg">Connect a wallet to read exchange_info.</p>
      </div>
    );
  }

  return (
    <div className="panel">
      <h2>Exchange — info</h2>
      <label htmlFor="ex-addr">Exchange (SQ:…)</label>
      <input
        id="ex-addr"
        value={addrInput}
        onChange={(e) => setAddrInput(e.target.value)}
        placeholder="SQ:…"
      />
      <div className="btn-row">
        <button
          type="button"
          onClick={() => {
            reset();
            setExchange(addrInput.trim() || null);
            setInfoText(null);
          }}
        >
          Load
        </button>
        <button
          type="button"
          className="btn ghost"
          disabled={!exchange || status === 'pending'}
          onClick={() =>
            void info()
              .then((r) => setInfoText(JSON.stringify(r)))
              .catch(() => undefined)
          }
        >
          Read exchange_info
        </button>
      </div>
      {infoText ? <p className="msg mono">{infoText}</p> : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}
