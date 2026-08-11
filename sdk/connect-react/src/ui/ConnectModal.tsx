import { useMemo, useState } from 'react';
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

  const primary = useMemo(() => connectors.filter((c) => !c.advanced), [connectors]);
  const advanced = useMemo(() => connectors.filter((c) => c.advanced), [connectors]);

  const open = modal === 'connect';

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
        {primary.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className="sqc-connector-item"
              disabled={status === 'connecting' || busyId != null}
              onClick={() => void onConnect(c.id)}
            >
              <span className="sqc-connector-item__icon">{connectorIcon(c.id)}</span>
              <span className="sqc-connector-item__meta">
                {c.name}
                <span className="sqc-connector-item__hint">
                  {busyId === c.id ? 'Sign to unlock…' : 'Sign once to unlock PQ wallet'}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>

      {advanced.length > 0 ? (
        <div className="sqc-advanced">
          <button
            type="button"
            className="sqc-advanced__toggle"
            aria-expanded={advancedOpen}
            onClick={() => setAdvancedOpen((v) => !v)}
          >
            {advancedOpen ? '▾' : '▸'} Advanced
          </button>
          {advancedOpen ? (
            <div className="sqc-advanced__panel">
              <p className="sqc-modal__sub" style={{ margin: 0 }}>
                Dev / agent only. Never paste a seed from an untrusted source.
              </p>
              <input
                className="sqc-input"
                type="password"
                autoComplete="off"
                spellCheck={false}
                placeholder="32-byte seed hex"
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
