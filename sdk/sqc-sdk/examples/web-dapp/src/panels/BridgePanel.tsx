import { useState } from 'react';
import { useAccount, useBridge } from '@splendor/connect-react';

export function BridgePanel() {
  const { isConnected, address } = useAccount();
  const [bridgeInput, setBridgeInput] = useState('');
  const [bridge, setBridge] = useState<string | null>(null);
  const [claimId, setClaimId] = useState('1');
  const [amount, setAmount] = useState('10');
  const [evmTx, setEvmTx] = useState('0x');
  const [infoText, setInfoText] = useState<string | null>(null);

  const { claim, info, status, hash, explorerUrl, error, reset } = useBridge(bridge);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>Bridge claim</h2>
        <p className="msg">Connect a wallet to claim bridged assets.</p>
      </div>
    );
  }

  const onLoad = () => {
    reset();
    setBridge(bridgeInput.trim() || null);
    setInfoText(null);
  };

  const onInfo = async () => {
    try {
      const r = await info();
      setInfoText(JSON.stringify(r));
    } catch {
      /* error via hook */
    }
  };

  const onClaim = async () => {
    if (!address) return;
    try {
      await claim({
        id: Number(claimId),
        asset: 'spld',
        amount: amount.trim(),
        recipient: address,
        evmTx: evmTx.trim(),
        attestations: [],
      });
    } catch {
      /* error via hook */
    }
  };

  return (
    <div className="panel">
      <h2>Bridge — claim</h2>
      <label htmlFor="br-addr">Bridge address (SQ:…)</label>
      <input
        id="br-addr"
        value={bridgeInput}
        onChange={(e) => setBridgeInput(e.target.value)}
        placeholder="SQ:…"
        autoComplete="off"
      />
      <div className="btn-row">
        <button type="button" onClick={onLoad} disabled={!bridgeInput.trim()}>
          Load bridge
        </button>
        <button type="button" className="btn ghost" disabled={!bridge} onClick={() => void onInfo()}>
          Read info
        </button>
      </div>
      {infoText ? <p className="msg mono">{infoText}</p> : null}
      {bridge ? (
        <>
          <div className="divider" />
          <label htmlFor="br-id">Claim id</label>
          <input id="br-id" value={claimId} onChange={(e) => setClaimId(e.target.value)} />
          <label htmlFor="br-amt">Amount (raw)</label>
          <input id="br-amt" value={amount} onChange={(e) => setAmount(e.target.value)} />
          <label htmlFor="br-evm">EVM tx hash</label>
          <input id="br-evm" value={evmTx} onChange={(e) => setEvmTx(e.target.value)} />
          <p className="meta">Recipient defaults to your connected SQ: address. Attestations empty for local demos.</p>
          <button type="button" disabled={status === 'pending'} onClick={() => void onClaim()}>
            {status === 'pending' ? 'Claiming…' : 'Claim SPLD'}
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
