import { useEffect, useId, useMemo, useState } from 'react';
import { useConnect, useNetwork } from '../react/hooks.js';
import { connectorIcon } from './icons.js';
import { ModalShell } from './ModalShell.js';
import { NetworkBadge } from './NetworkBadge.js';

export function ConnectModal() {
  const {
    modal,
    closeModal,
    connectors,
    connect,
    status,
    errorMessage,
  } = useConnect();
  const { network } = useNetwork();
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [seed, setSeed] = useState('');
  const [busyId, setBusyId] = useState<string | null>(null);
  const [readyMap, setReadyMap] = useState<Record<string, boolean>>({});
  const advancedPanelId = useId();

  const primary = useMemo(() => connectors.filter((c) => !c.advanced), [connectors]);
  const advanced = useMemo(() => connectors.filter((c) => c.advanced), [connectors]);

  const open = modal === 'connect';

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void (async () => {
      const next: Record<string, boolean> = {};
      await Promise.all(
        primary.map(async (c) => {
          try {
            next[c.id] = await c.ready();
          } catch {
            next[c.id] = false;
          }
        }),
      );
      if (!cancelled) setReadyMap(next);
    })();
    return () => {
      cancelled = true;
    };
  }, [open, primary]);

  const onConnect = async (id: string, opts?: Record<string, unknown>) => {
    setBusyId(id);
    try {
      await connect(id, opts);
      setSeed('');
      setAdvancedOpen(false);
    } catch {
      /* error surfaced via useConnect */
    } finally {
      setBusyId(null);
    }
  };

  const renderIcon = (c: { id: string; icon?: string }) => {
    if (c.icon) {
      return <img src={c.icon} alt="" width={18} height={18} />;
    }
    return connectorIcon(c.id);
  };

  return (
    <ModalShell
      open={open}
      title="Connect to Splendor"
      subtitle="Derive your post-quantum SQ: wallet. Keys stay in the local signer."
      onClose={closeModal}
    >
      <div style={{ marginBottom: '0.85rem' }}>
        <NetworkBadge />
        {network?.id ? (
          <span
            style={{
              marginLeft: '0.5rem',
              fontSize: '0.75rem',
              color: 'var(--sqc-text-muted)',
            }}
          >
            {network.id}
          </span>
        ) : null}
      </div>

      <ul className="sqc-connector-list">
        {primary.map((c) => {
          const isReady = readyMap[c.id] !== false;
          const unavailable = readyMap[c.id] === false;
          return (
            <li key={c.id}>
              <button
                type="button"
                className="sqc-connector-item"
                disabled={
                  status === 'connecting' || busyId != null || unavailable || !isReady
                }
                onClick={() => void onConnect(c.id)}
              >
                <span className="sqc-connector-item__icon">{renderIcon(c)}</span>
                <span className="sqc-connector-item__meta">
                  {c.name}
                  <span className="sqc-connector-item__hint">
                    {busyId === c.id
                      ? 'Sign to unlock…'
                      : unavailable
                        ? 'Wallet not detected'
                        : 'Sign once to unlock PQ wallet'}
                  </span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>

      {advanced.length > 0 ? (
        <div className="sqc-advanced">
          <button
            type="button"
            className="sqc-advanced__toggle"
            aria-expanded={advancedOpen}
            aria-controls={advancedPanelId}
            onClick={() => setAdvancedOpen((v) => !v)}
          >
            {advancedOpen ? '▾' : '▸'} Advanced
          </button>
          {advancedOpen ? (
            <div id={advancedPanelId} className="sqc-advanced__panel">
              <p className="sqc-modal__sub" style={{ margin: 0 }}>
                Dev / agent only. Never paste a seed from an untrusted source.
              </p>
              <input
                className="sqc-input"
                type="password"
                autoComplete="off"
                spellCheck={false}
                placeholder="32-byte seed hex (64 chars)"
                value={seed}
                onChange={(e) => setSeed(e.target.value)}
              />
              {advanced.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  className="sqc-btn sqc-btn--ghost"
                  disabled={!seed.trim() || status === 'connecting'}
                  onClick={() => void onConnect(c.id, { seed: seed.trim() })}
                >
                  {busyId === c.id ? 'Unlocking…' : `Unlock with ${c.name}`}
                </button>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {errorMessage ? <p className="sqc-error">{errorMessage}</p> : null}
    </ModalShell>
  );
}
