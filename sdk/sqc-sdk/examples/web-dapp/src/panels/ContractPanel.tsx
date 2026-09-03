import { useState } from 'react';
import { useAccount, useContract } from '@splendor/connect-react';

export function ContractPanel() {
  const { isConnected } = useAccount();
  const [addrInput, setAddrInput] = useState('');
  const [contract, setContract] = useState<string | null>(null);
  const [sig, setSig] = useState('totalSupply()');
  const [out, setOut] = useState<string | null>(null);

  const { read, status, error, reset } = useContract(contract);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Contract</h2>
        <p className="msg">Connect a wallet to call a bytecode contract.</p>
      </div>
    );
  }

  return (
    <div className="panel">
      <h2>Contract — read</h2>
      <label htmlFor="ct-addr">Contract (SQ:…)</label>
      <input
        id="ct-addr"
        value={addrInput}
        onChange={(e) => setAddrInput(e.target.value)}
        placeholder="SQ:…"
      />
      <button
        type="button"
        onClick={() => {
          reset();
          setContract(addrInput.trim() || null);
          setOut(null);
        }}
      >
        Load
      </button>
      {contract ? (
        <>
          <label htmlFor="ct-sig">Function signature</label>
          <input id="ct-sig" value={sig} onChange={(e) => setSig(e.target.value)} />
          <button
            type="button"
            disabled={status === 'pending'}
            onClick={() =>
              void read(sig.trim())
                .then((r) => setOut(JSON.stringify(r)))
                .catch(() => undefined)
            }
          >
            Read
          </button>
          {out ? <p className="msg mono">{out}</p> : null}
        </>
      ) : null}
      {error ? <p className="msg error">{error.message}</p> : null}
      <p className="msg">
        X402: use <code className="mono">wallet.payX402</code> / <code className="mono">usePayX402</code>{' '}
        — middleware requires <code className="mono">settlement_tx</code>.
      </p>
    </div>
  );
}
