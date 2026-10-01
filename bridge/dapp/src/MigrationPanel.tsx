import { useCallback, useEffect, useState } from 'react';
import { useAccount, useBridge } from '@splendor/connect-react';
import { connectLegacy, burnNative, burnToken, sqToBytes32, LEGACY_CHAIN_ID, type LegacyState } from './legacy';

const DEFAULT_BRIDGE = (import.meta.env.VITE_BRIDGE_ADDRESS ?? '').trim();

export function MigrationPanel() {
  const { isConnected, address } = useAccount();
  const [bridgeAddr, setBridgeAddr] = useState<string>(DEFAULT_BRIDGE);
  const [legacy, setLegacy] = useState<LegacyState>({ address: null, chainId: null, spld: null, splv: null });
  const [asset, setAsset] = useState<'spld' | 'splv'>('spld');
  const [amount, setAmount] = useState('');
  const [busy, setBusy] = useState(false);
  const [burnId, setBurnId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [claimed, setClaimed] = useState<boolean | null>(null);

  const bridge = useBridge(bridgeAddr || null);

  const refreshLegacy = useCallback(async () => {
    try {
      setLegacy(await connectLegacy());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  // Poll the quantum bridge for the claim status of the last burn.
  useEffect(() => {
    if (!burnId || !bridgeAddr) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const r = await bridge.claimed(Number(burnId));
        const c = (r as { claimed?: boolean }).claimed === true;
        if (!cancelled) setClaimed(c);
        if (c) return; // stop once claimed
      } catch {
        /* keep polling */
      }
      if (!cancelled) setTimeout(poll, 5000);
    };
    poll();
    return () => {
      cancelled = true;
    };
  }, [burnId, bridgeAddr, bridge]);

  const canBurn = isConnected && !!address && legacy.address && !!amount && !busy;

  const onBurn = async () => {
    setError(null);
    setBurnId(null);
    setClaimed(null);
    if (!address) return;
    setBusy(true);
    try {
      const recipient = sqToBytes32(address);
      const id = asset === 'spld' ? await burnNative(amount, recipient) : await burnToken(amount, recipient);
      setBurnId(id);
      await refreshLegacy();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <h2>1. Connect</h2>
      <p>
        Quantum wallet: {isConnected ? <b className="ok">{address}</b> : <b className="warn">not connected</b>}
      </p>
      <p>
        Legacy wallet:{' '}
        {legacy.address ? (
          <b className="ok">
            {legacy.address} (chain {legacy.chainId})
          </b>
        ) : (
          <b className="warn">not connected</b>
        )}
      </p>
      {legacy.chainId !== null && legacy.chainId !== LEGACY_CHAIN_ID && (
        <p className="error">Legacy wallet is on chain {legacy.chainId}, expected {LEGACY_CHAIN_ID}. Switch networks in MetaMask.</p>
      )}
      <div className="btn-row">
        <button type="button" onClick={() => void refreshLegacy()}>
          {legacy.address ? 'Refresh legacy balances' : 'Connect MetaMask'}
        </button>
      </div>

      <div className="divider" />

      <h2>2. Burn on legacy</h2>
      <label htmlFor="br-addr">Bridge address (SQ:…)</label>
      <input id="br-addr" value={bridgeAddr} onChange={(e) => setBridgeAddr(e.target.value)} placeholder="SQ:…" autoComplete="off" />
      <p className="meta">
        Legacy balances — SPLD: {legacy.spld ?? '—'} · SPLV: {legacy.splv ?? '—'}
      </p>
      <div className="row">
        <label htmlFor="asset">Asset</label>
        <select id="asset" value={asset} onChange={(e) => setAsset(e.target.value as 'spld' | 'splv')}>
          <option value="spld">SPLD (native)</option>
          <option value="splv">SPLV (ERC-20)</option>
        </select>
      </div>
      <label htmlFor="amount">Amount (whole tokens, min 1)</label>
      <input id="amount" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="e.g. 100" inputMode="decimal" />
      <p className="meta">Recipient: your connected quantum address ({address ?? 'not connected'}).</p>
      <button type="button" disabled={!canBurn} onClick={() => void onBurn()}>
        {busy ? 'Burning…' : asset === 'spld' ? 'Burn SPLD' : 'Burn SPLV'}
      </button>

      {burnId && (
        <div className="divider" />
      )}
      {burnId && (
        <p className="msg">
          Burn id <b>{burnId}</b> submitted.{' '}
          {claimed === null && 'Waiting for the relayer to credit the quantum chain…'}
          {claimed === true && <b className="ok">Claimed — funds are on the quantum chain.</b>}
          {claimed === false && 'Not claimed yet.'}
        </p>
      )}
      {error ? <p className="msg error">{error}</p> : null}
    </section>
  );
}
