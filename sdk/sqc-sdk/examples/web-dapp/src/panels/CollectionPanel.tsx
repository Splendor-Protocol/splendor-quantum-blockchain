import { useState } from 'react';
import { useAccount, useQRC1155 } from '@splendor/connect-react';

export function CollectionPanel() {
  const { isConnected } = useAccount();
  const [addrInput, setAddrInput] = useState('');
  const [collection, setCollection] = useState<string | null>(null);
  const [owner, setOwner] = useState('');
  const [id, setId] = useState('1');
  const [bal, setBal] = useState<string | null>(null);

  const { balanceOf, status, error, reset } = useQRC1155(collection);

  if (!isConnected) {
    return (
      <div className="panel">
        <h2>QRC-1155</h2>
        <p className="msg">Connect a wallet to read a collection.</p>
      </div>
    );
  }

  return (
    <div className="panel">
      <h2>QRC-1155 — balance</h2>
      <label htmlFor="c1155">Collection (SQ:…)</label>
      <input
        id="c1155"
        value={addrInput}
        onChange={(e) => setAddrInput(e.target.value)}
        placeholder="SQ:…"
      />
      <button
        type="button"
        onClick={() => {
          reset();
          setCollection(addrInput.trim() || null);
          setBal(null);
        }}
      >
        Load
      </button>
      {collection ? (
        <>
          <label htmlFor="c1155-owner">Owner</label>
          <input id="c1155-owner" value={owner} onChange={(e) => setOwner(e.target.value)} />
          <label htmlFor="c1155-id">Token id</label>
          <input id="c1155-id" value={id} onChange={(e) => setId(e.target.value)} />
          <button
            type="button"
            disabled={status === 'pending'}
            onClick={() =>
              void balanceOf(owner.trim(), id.trim()).then((b) => setBal(b)).catch(() => undefined)
            }
          >
            Read balance
          </button>
          {bal != null ? <p className="msg mono">balance: {bal}</p> : null}
        </>
      ) : null}
      {error ? <p className="msg error">{error.message}</p> : null}
    </div>
  );
}
