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
    clearError,
    labels,
    connectingStep,
  } = useConnect();
  const { network } = useNetwork();
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [fieldValues, setFieldValues] = useState<Record<string, Record<string, string>>>({});
  const [busyId, setBusyId] = useState<string | null>(null);
  const [readyMap, setReadyMap] = useState<Record<string, boolean>>({});
  const [readyLoading, setReadyLoading] = useState(false);
  const advancedPanelId = useId();

  const primary = useMemo(() => connectors.filter((c) => !c.advanced), [connectors]);
  const advanced = useMemo(() => connectors.filter((c) => c.advanced), [connectors]);

  const open = modal === 'connect';

  useEffect(() => {
    if (!open) {
      setFieldValues({});
      setBusyId(null);
      return;
    }
    let cancelled = false;
    setReadyLoading(true);
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
      if (!cancelled) {
        setReadyMap(next);
        setReadyLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, primary]);

  const onConnect = async (id: string, opts?: Record<string, unknown>) => {
    setBusyId(id);
    try {
      await connect(id, opts);
      setFieldValues({});
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

  const title = labels.connectTitle ?? 'Connect to Splendor';
  const subtitle =
    labels.connectSubtitle ??
    'Derive your post-quantum SQ: wallet. Keys stay in the local signer.';

  return (
    <ModalShell open={open} title={title} subtitle={subtitle} onClose={closeModal}>
      <div className="sqc-modal-network">
        <NetworkBadge />
        {network?.id ? <span className="sqc-modal-network__id">{network.id}</span> : null}
      </div>

      {readyLoading && primary.length === 0 ? (
        <p className="sqc-modal__sub">Detecting wallets…</p>
      ) : null}

      <ul className="sqc-connector-list">
        {primary.map((c) => {
          const readyKnown = c.id in readyMap;
          const unavailable = readyMap[c.id] === false;
          const pendingReady = readyLoading && !readyKnown;
          return (
            <li key={c.id}>
              <button
                type="button"
                className="sqc-connector-item"
                disabled={
                  status === 'connecting' ||
                  busyId != null ||
                  unavailable ||
                  pendingReady
                }
                onClick={() => void onConnect(c.id)}
              >
                <span className="sqc-connector-item__icon">{renderIcon(c)}</span>
                <span className="sqc-connector-item__meta">
                  {c.name}
                  {c.postQuantumSecure === false ? (
                    <span
                      className="sqc-connector-item__badge"
                      title="This account's key is derived from an Ethereum signature, so it is only as strong as secp256k1. Transactions are signed with ML-DSA-65, but a quantum adversary who breaks ECDSA could reproduce this key. Use a native seed for funds you intend to hold."
                    >
                      Not quantum-safe
                    </span>
                  ) : null}
                  <span className="sqc-connector-item__hint">
                    {busyId === c.id
                      ? connectingStep === 'init'
                        ? 'Loading signer…'
                        : 'Sign to unlock…'
                      : pendingReady
                        ? 'Checking…'
                        : unavailable
                          ? 'Wallet not detected'
                          : c.postQuantumSecure === false
                            ? 'Key derived from ECDSA — convenient, not post-quantum'
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
                Dev / agent only. Never paste secrets from an untrusted source.
              </p>
              {advanced.map((c) => {
                const fields = c.connectFields ?? [];
                const values = fieldValues[c.id] ?? {};
                const filled =
                  fields.length === 0 ||
                  fields.every((f) => String(values[f.name] ?? '').trim().length > 0);
                return (
                  <div key={c.id} style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
                    <div className="sqc-connector-item__meta" style={{ fontWeight: 600 }}>
                      {c.name}
                    </div>
                    {fields.map((f) => (
                      <label key={f.name} style={{ display: 'block' }}>
                        <span className="sqc-account-row__label">{f.label}</span>
                        <input
                          className="sqc-input"
                          type={f.type}
                          autoComplete="off"
                          spellCheck={false}
                          placeholder={f.placeholder}
                          value={values[f.name] ?? ''}
                          onChange={(e) =>
                            setFieldValues((prev) => ({
                              ...prev,
                              [c.id]: { ...prev[c.id], [f.name]: e.target.value },
                            }))
                          }
                        />
                      </label>
                    ))}
                    <button
                      type="button"
                      className="sqc-btn sqc-btn--ghost"
                      disabled={!filled || status === 'connecting'}
                      onClick={() => {
                        const opts: Record<string, unknown> = {};
                        for (const f of fields) {
                          opts[f.name] = String(values[f.name] ?? '').trim();
                        }
                        void onConnect(c.id, opts);
                      }}
                    >
                      {busyId === c.id ? 'Unlocking…' : `Unlock with ${c.name}`}
                    </button>
                  </div>
                );
              })}
            </div>
          ) : null}
        </div>
      ) : null}

      {errorMessage ? (
        <div className="sqc-error-row">
          <p className="sqc-error">{errorMessage}</p>
          <button type="button" className="sqc-btn sqc-btn--ghost" onClick={() => clearError()}>
            Dismiss
          </button>
        </div>
      ) : null}
    </ModalShell>
  );
}
