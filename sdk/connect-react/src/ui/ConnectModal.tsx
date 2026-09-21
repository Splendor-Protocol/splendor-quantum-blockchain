import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { generateSeedHex } from '../connectors/seed.js';
import type { Connector } from '../connectors/types.js';
import { useConnect, useNetwork } from '../react/hooks.js';
import { sanitizeIconUrl } from '../widgets/utils.js';
import { groupConnectors, isNativeConnector } from './connectorGroups.js';
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
  /** Which native connector the user stepped into, or null for the wallet list. */
  const [nativeId, setNativeId] = useState<string | null>(null);
  /**
   * A freshly generated seed, held in component state only: it is the account,
   * so it is never persisted, logged, or broadcast, and it is dropped the
   * moment the modal closes or the user steps back.
   */
  const [newSeed, setNewSeed] = useState<string | null>(null);
  const [seedCopied, setSeedCopied] = useState(false);
  const [seedRevealed, setSeedRevealed] = useState(false);
  const [seedBackupConfirmed, setSeedBackupConfirmed] = useState(false);
  /** Seed generation fails before any connect attempt, so it has its own slot. */
  const [seedError, setSeedError] = useState<string | null>(null);
  const advancedPanelId = useId();

  const { native, legacy, advanced } = useMemo(() => groupConnectors(connectors), [connectors]);
  const selectable = useMemo(() => [...native, ...legacy], [native, legacy]);
  const nativeConnector = useMemo(
    () => native.find((c) => c.id === nativeId) ?? null,
    [native, nativeId],
  );

  const open = modal === 'connect';

  const resetNative = useCallback(() => {
    setNativeId(null);
    setNewSeed(null);
    setSeedCopied(false);
    setSeedRevealed(false);
    setSeedBackupConfirmed(false);
    setSeedError(null);
  }, []);

  useEffect(() => {
    if (!open) {
      setFieldValues({});
      setBusyId(null);
      resetNative();
      return;
    }
    let cancelled = false;
    setReadyLoading(true);
    void (async () => {
      const next: Record<string, boolean> = {};
      await Promise.all(
        selectable.map(async (c) => {
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
  }, [open, selectable, resetNative]);

  const onConnect = async (id: string, opts?: Record<string, unknown>) => {
    setBusyId(id);
    try {
      await connect(id, opts);
      setFieldValues({});
      setAdvancedOpen(false);
      // Wipe seed material from React state after successful unlock.
      resetNative();
    } catch {
      /* error surfaced via useConnect */
    } finally {
      setBusyId(null);
    }
  };

  const renderIcon = (c: { id: string; icon?: string }) => {
    const safe = sanitizeIconUrl(c.icon);
    if (safe) {
      return <img src={safe} alt="" width={18} height={18} referrerPolicy="no-referrer" />;
    }
    return connectorIcon(c.id);
  };

  const setField = (connectorId: string, field: string, value: string) =>
    setFieldValues((prev) => ({
      ...prev,
      [connectorId]: { ...prev[connectorId], [field]: value },
    }));

  const connectorItem = (c: Connector, onPick: () => void) => {
    const readyKnown = c.id in readyMap;
    const unavailable = readyMap[c.id] === false;
    const pendingReady = readyLoading && !readyKnown;
    const notPQ = c.postQuantumSecure === false;
    return (
      <li key={c.id}>
        <button
          type="button"
          className="sqc-connector-item"
          disabled={status === 'connecting' || busyId != null || unavailable || pendingReady}
          onClick={onPick}
        >
          <span className="sqc-connector-item__icon">{renderIcon(c)}</span>
          <span className="sqc-connector-item__meta">
            {c.name}
            {notPQ ? (
              <span
                className="sqc-connector-item__badge"
                title="This account's key is derived from an Ethereum signature, so it is only as strong as secp256k1. Transactions are signed with ML-DSA-65, but a quantum adversary who breaks ECDSA could reproduce this key. Create a native wallet for funds you intend to hold."
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
                    : isNativeConnector(c)
                      ? 'Create or import an ML-DSA-65 key — no Ethereum involved'
                      : 'Key derived from ECDSA — convenient, not post-quantum'}
            </span>
          </span>
        </button>
      </li>
    );
  };

  const title = labels.connectTitle ?? 'Connect to Splendor';
  const subtitle = nativeConnector
    ? 'ML-DSA-65 (FIPS-204) key, generated on this device. No Ethereum key stands behind it.'
    : (labels.connectSubtitle ??
      'Start with a post-quantum ML-DSA-65 wallet. Keys stay in the local signer.');

  const nativeFields = nativeConnector?.connectFields ?? [];
  const nativeValues = nativeConnector ? (fieldValues[nativeConnector.id] ?? {}) : {};
  const canImport =
    nativeFields.length > 0 &&
    nativeFields.every((f) => String(nativeValues[f.name] ?? '').trim().length > 0);
  // Creating a wallet means handing the connector a seed, so only offer it when
  // that is the field the connector actually asks for.
  const canCreate = nativeFields.some((f) => f.name === 'seed');
  const busyNative = nativeConnector != null && busyId === nativeConnector.id;

  const copySeed = async () => {
    if (!newSeed) return;
    try {
      await navigator.clipboard.writeText(newSeed);
      setSeedCopied(true);
    } catch {
      /* clipboard blocked — the seed is on screen to copy by hand */
    }
  };

  return (
    <ModalShell open={open} title={title} subtitle={subtitle} onClose={closeModal}>
      <div className="sqc-modal-network">
        <NetworkBadge />
        {network?.id ? <span className="sqc-modal-network__id">{network.id}</span> : null}
      </div>

      {nativeConnector ? (
        <div className="sqc-native">
          <button type="button" className="sqc-native__back" onClick={resetNative}>
            ← All wallets
          </button>

          {canCreate ? (
            newSeed ? (
              <div className="sqc-native__section">
                <div className="sqc-account-row">
                  <span className="sqc-account-row__label">Recovery seed — save it now</span>
                  <div className="sqc-account-row__value">
                    <span className={seedRevealed ? undefined : 'sqc-seed-masked'}>
                      {seedRevealed ? newSeed : '•'.repeat(Math.min(64, newSeed.length))}
                    </span>
                    <button
                      type="button"
                      className="sqc-copy"
                      onClick={() => setSeedRevealed((v) => !v)}
                    >
                      {seedRevealed ? 'Hide' : 'Reveal'}
                    </button>
                    <button type="button" className="sqc-copy" onClick={() => void copySeed()}>
                      {seedCopied ? 'Copied' : 'Copy'}
                    </button>
                  </div>
                </div>
                <p className="sqc-native__warning">
                  This seed <strong>is</strong> the account. It is never sent anywhere and nobody
                  can restore it for you — put it in a password manager before continuing.
                </p>
                <label className="sqc-native__confirm">
                  <input
                    type="checkbox"
                    checked={seedBackupConfirmed}
                    onChange={(e) => setSeedBackupConfirmed(e.target.checked)}
                  />
                  <span>I saved this seed in a safe place</span>
                </label>
                <div className="sqc-modal__actions">
                  <button
                    type="button"
                    className="sqc-btn sqc-btn--ghost"
                    disabled={busyNative}
                    onClick={() => {
                      setNewSeed(null);
                      setSeedCopied(false);
                      setSeedRevealed(false);
                      setSeedBackupConfirmed(false);
                    }}
                  >
                    Discard
                  </button>
                  <button
                    type="button"
                    className="sqc-btn sqc-btn--primary"
                    disabled={busyNative || !seedBackupConfirmed}
                    onClick={() => void onConnect(nativeConnector.id, { seed: newSeed })}
                  >
                    {busyNative ? 'Unlocking…' : 'Unlock'}
                  </button>
                </div>
              </div>
            ) : (
              <div className="sqc-native__section">
                <button
                  type="button"
                  className="sqc-btn sqc-btn--primary sqc-native__cta"
                  disabled={busyNative || status === 'connecting'}
                  onClick={() => {
                    setSeedCopied(false);
                    setSeedError(null);
                    try {
                      setNewSeed(generateSeedHex());
                    } catch (e) {
                      setSeedError(e instanceof Error ? e.message : 'Could not generate a seed');
                    }
                  }}
                >
                  Create a new wallet
                </button>
                <p className="sqc-modal__sub" style={{ margin: 0 }}>
                  32 bytes from this browser&rsquo;s CSPRNG become an ML-DSA-65 keypair and an{' '}
                  <code>SQ:</code> address. You back the seed up once; it never leaves the page.
                </p>
              </div>
            )
          ) : null}

          {canCreate && !newSeed ? <div className="sqc-native__or">or</div> : null}

          {newSeed ? null : (
            <div className="sqc-native__section">
              {nativeFields.map((f) => (
                <label key={f.name} style={{ display: 'block' }}>
                  <span className="sqc-account-row__label">Import an existing {f.label}</span>
                  <input
                    className="sqc-input"
                    type={f.type}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={f.placeholder}
                    value={nativeValues[f.name] ?? ''}
                    onChange={(e) => setField(nativeConnector.id, f.name, e.target.value)}
                  />
                </label>
              ))}
              <button
                type="button"
                className="sqc-btn sqc-btn--ghost"
                disabled={!canImport || busyNative || status === 'connecting'}
                onClick={() => {
                  const opts: Record<string, unknown> = {};
                  for (const f of nativeFields) {
                    opts[f.name] = String(nativeValues[f.name] ?? '').trim();
                  }
                  void onConnect(nativeConnector.id, opts);
                }}
              >
                {busyNative ? 'Unlocking…' : 'Import wallet'}
              </button>
              <p className="sqc-modal__sub" style={{ margin: 0 }}>
                A CLI <code>.sqwallet</code> file holds an encrypted ML-DSA-65 key rather than a
                seed — keep signing those with <code>sqc-wallet</code>.
              </p>
            </div>
          )}
        </div>
      ) : (
        <>
          {readyLoading && selectable.length === 0 ? (
            <p className="sqc-modal__sub">Detecting wallets…</p>
          ) : null}

          {native.length > 0 ? (
            <ul className="sqc-connector-list">
              {native.map((c) =>
                // The create/import step exists to collect a secret. A native
                // connector that asks for nothing — an extension or a deeplink
                // wallet holding its own ML-DSA key — would land on an empty
                // panel with a disabled button, so it connects straight away.
                connectorItem(c, () =>
                  (c.connectFields?.length ?? 0) > 0
                    ? setNativeId(c.id)
                    : void onConnect(c.id),
                ),
              )}
            </ul>
          ) : null}

          {legacy.length > 0 ? (
            <div className="sqc-legacy">
              <p className="sqc-legacy__label">Legacy</p>
              <p className="sqc-legacy__note">
                These derive your ML-DSA-65 key from an Ethereum signature. Convenient if your
                funds already live on such an address — but the account is only as strong as
                secp256k1, so it is not post-quantum.
              </p>
              <ul className="sqc-connector-list">
                {legacy.map((c) => connectorItem(c, () => void onConnect(c.id)))}
              </ul>
            </div>
          ) : null}

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
                      <div
                        key={c.id}
                        style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}
                      >
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
                              onChange={(e) => setField(c.id, f.name, e.target.value)}
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
        </>
      )}

      {(errorMessage ?? seedError) ? (
        <div className="sqc-error-row">
          <p className="sqc-error">{errorMessage ?? seedError}</p>
          <button
            type="button"
            className="sqc-btn sqc-btn--ghost"
            onClick={() => {
              setSeedError(null);
              clearError();
            }}
          >
            Dismiss
          </button>
        </div>
      ) : null}
    </ModalShell>
  );
}
