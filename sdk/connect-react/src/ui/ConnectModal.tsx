import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { generateSeedHex } from '../connectors/seed.js';
import type { Connector } from '../connectors/types.js';
import { useConnect, useNetwork } from '../react/hooks.js';
import { sanitizeIconUrl } from '../widgets/utils.js';
import { groupConnectors } from './connectorGroups.js';
import { connectorIcon } from './icons.js';
import { ModalShell } from './ModalShell.js';
import { NetworkBadge } from './NetworkBadge.js';

/** Clear a copied seed from the clipboard after this long (if it is still there). */
export const SEED_CLIPBOARD_CLEAR_MS = 45_000;
/** Re-mask a revealed seed after this long. */
export const SEED_REVEAL_MS = 60_000;
/** How many seed characters the user types back before unlocking a new wallet. */
export const SEED_CONFIRM_CHARS = 4;

/** Attributes that keep secrets out of autofill, session replay and error tooling. */
const SECRET_ATTRS = {
  autoComplete: 'new-password',
  autoCorrect: 'off',
  autoCapitalize: 'off',
  spellCheck: false,
  'data-private': 'true',
  'data-sentry-mask': 'true',
  'data-hj-suppress': 'true',
  'data-lpignore': 'true',
} as const;

const MASK_ATTRS = {
  'data-private': 'true',
  'data-sentry-mask': 'true',
  'data-hj-suppress': 'true',
} as const;

function pickConfirmStart(seed: string): number {
  const span = Math.max(1, seed.length - SEED_CONFIRM_CHARS + 1);
  const buf = new Uint32Array(1);
  globalThis.crypto.getRandomValues(buf);
  return buf[0]! % span;
}

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
  /**
   * Connect fields are uncontrolled: their values live only in the DOM inputs (read via
   * refs on submit), never in React state where devtools and error reporters can see them.
   */
  const fieldRefs = useRef<Record<string, HTMLInputElement | null>>({});
  const [filled, setFilled] = useState<Record<string, boolean>>({});
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
  const [confirmStart, setConfirmStart] = useState(0);
  const [confirmInput, setConfirmInput] = useState('');
  /** Seed generation fails before any connect attempt, so it has its own slot. */
  const [seedError, setSeedError] = useState<string | null>(null);
  const advancedPanelId = useId();
  const clipboardTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const { native, advanced } = useMemo(() => groupConnectors(connectors), [connectors]);
  const selectable = useMemo(() => [...native, ...advanced], [native, advanced]);
  const nativeConnector = useMemo(
    () => native.find((c) => c.id === nativeId) ?? null,
    [native, nativeId],
  );

  const open = modal === 'connect';

  const fieldKey = (connectorId: string, field: string) => `${connectorId}:${field}`;

  const clearFields = useCallback((connectorId?: string) => {
    for (const [key, el] of Object.entries(fieldRefs.current)) {
      if (!el || (connectorId && !key.startsWith(`${connectorId}:`))) continue;
      el.value = '';
    }
    setFilled((prev) => {
      if (!connectorId) return {};
      const next = { ...prev };
      for (const key of Object.keys(next)) if (key.startsWith(`${connectorId}:`)) delete next[key];
      return next;
    });
  }, []);

  const resetNative = useCallback(() => {
    setNativeId(null);
    setNewSeed(null);
    setSeedCopied(false);
    setSeedRevealed(false);
    setSeedBackupConfirmed(false);
    setConfirmInput('');
    setSeedError(null);
  }, []);

  useEffect(() => {
    if (!open) {
      clearFields();
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
  }, [open, selectable, resetNative, clearFields]);

  useEffect(() => {
    if (!seedRevealed) return;
    const t = setTimeout(() => setSeedRevealed(false), SEED_REVEAL_MS);
    return () => clearTimeout(t);
  }, [seedRevealed]);

  useEffect(
    () => () => {
      if (clipboardTimer.current) clearTimeout(clipboardTimer.current);
    },
    [],
  );

  const readFields = (connectorId: string, fields: Connector['connectFields'] = []) => {
    const opts: Record<string, unknown> = {};
    for (const f of fields) {
      opts[f.name] = String(fieldRefs.current[fieldKey(connectorId, f.name)]?.value ?? '').trim();
    }
    return opts;
  };

  const onConnect = async (id: string, opts?: Record<string, unknown>, fromFields = false) => {
    setBusyId(id);
    try {
      await connect(id, opts);
      setAdvancedOpen(false);
      clearFields();
      resetNative();
    } catch {
      // Error surfaced via useConnect. A typed secret is wiped so it does not linger on
      // screen; a generated seed stays so the user can retry the unlock.
      if (fromFields) clearFields(id);
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

  const fieldInput = (connectorId: string, f: NonNullable<Connector['connectFields']>[number]) => {
    const key = fieldKey(connectorId, f.name);
    return (
      <input
        className="sqc-input"
        type={f.type}
        placeholder={f.placeholder}
        defaultValue=""
        ref={(el) => {
          fieldRefs.current[key] = el;
        }}
        onChange={(e) => {
          const has = e.target.value.trim().length > 0;
          setFilled((prev) => (prev[key] === has ? prev : { ...prev, [key]: has }));
        }}
        {...SECRET_ATTRS}
      />
    );
  };

  const connectorItem = (c: Connector, onPick: () => void) => {
    const readyKnown = c.id in readyMap;
    const unavailable = readyMap[c.id] === false;
    const pendingReady = readyLoading && !readyKnown;
    const hint =
      busyId === c.id
        ? connectingStep === 'init'
          ? 'Loading signer…'
          : 'Approve in wallet…'
        : pendingReady
          ? 'Checking…'
          : unavailable
            ? 'Extension not installed'
            : c.id === 'splendor-extension'
              ? 'Browser extension — ML-DSA-65 keys stay in the wallet'
              : c.id === 'seed'
                ? 'Create or import an ML-DSA-65 seed in this page'
                : 'Post-quantum ML-DSA-65 wallet';
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
            <span className="sqc-connector-item__hint">{hint}</span>
          </span>
        </button>
      </li>
    );
  };

  const title = labels.connectTitle ?? 'Connect to Splendor';
  const subtitle = nativeConnector
    ? 'ML-DSA-65 (FIPS-204) key from a native seed on this device.'
    : (labels.connectSubtitle ??
      'Use the Splendor Quantum Wallet extension, or create/import a native ML-DSA-65 seed.');

  const nativeFields = nativeConnector?.connectFields ?? [];
  const canImport =
    nativeConnector != null &&
    nativeFields.length > 0 &&
    nativeFields.every((f) => filled[fieldKey(nativeConnector.id, f.name)]);
  // Creating a wallet means handing the connector a seed, so only offer it when
  // that is the field the connector actually asks for.
  const canCreate = nativeFields.some((f) => f.name === 'seed');
  const busyNative = nativeConnector != null && busyId === nativeConnector.id;
  const expectedConfirm = newSeed ? newSeed.slice(confirmStart, confirmStart + SEED_CONFIRM_CHARS) : '';
  const confirmOk = !!newSeed && confirmInput.trim().toLowerCase() === expectedConfirm.toLowerCase();

  const copySeed = async () => {
    if (!newSeed) return;
    const copied = newSeed;
    try {
      await navigator.clipboard.writeText(copied);
      setSeedCopied(true);
    } catch {
      /* clipboard blocked — the seed is on screen to copy by hand */
      return;
    }
    if (clipboardTimer.current) clearTimeout(clipboardTimer.current);
    clipboardTimer.current = setTimeout(() => {
      clipboardTimer.current = null;
      void (async () => {
        try {
          // Only wipe the clipboard if it still holds the seed; never clobber something else.
          if ((await navigator.clipboard.readText()) === copied) await navigator.clipboard.writeText('');
        } catch {
          /* no read permission — nothing safe to do */
        }
      })();
    }, SEED_CLIPBOARD_CLEAR_MS);
  };

  const discardSeed = () => {
    setNewSeed(null);
    setSeedCopied(false);
    setSeedRevealed(false);
    setSeedBackupConfirmed(false);
    setConfirmInput('');
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
            ← Back
          </button>

          {canCreate ? (
            newSeed ? (
              <div className="sqc-native__section">
                <div className="sqc-account-row">
                  <span className="sqc-account-row__label">Recovery seed — save it now</span>
                  <div className="sqc-account-row__value">
                    <span className={seedRevealed ? undefined : 'sqc-seed-masked'} {...MASK_ATTRS}>
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
                  {seedCopied ? ' The clipboard is cleared in 45 seconds.' : ''}
                </p>
                <label className="sqc-native__confirm">
                  <input
                    type="checkbox"
                    checked={seedBackupConfirmed}
                    onChange={(e) => setSeedBackupConfirmed(e.target.checked)}
                  />
                  <span>I saved this seed in a safe place</span>
                </label>
                {seedBackupConfirmed ? (
                  <label style={{ display: 'block' }}>
                    <span className="sqc-account-row__label">
                      Type characters {confirmStart + 1}–{confirmStart + SEED_CONFIRM_CHARS} of your seed
                    </span>
                    <input
                      className="sqc-input"
                      data-testid="sqc-seed-confirm"
                      maxLength={SEED_CONFIRM_CHARS}
                      value={confirmInput}
                      onChange={(e) => setConfirmInput(e.target.value)}
                      aria-invalid={confirmInput.length === SEED_CONFIRM_CHARS && !confirmOk}
                      {...SECRET_ATTRS}
                    />
                  </label>
                ) : null}
                <div className="sqc-modal__actions">
                  <button type="button" className="sqc-btn sqc-btn--ghost" disabled={busyNative} onClick={discardSeed}>
                    Discard
                  </button>
                  <button
                    type="button"
                    className="sqc-btn sqc-btn--primary"
                    disabled={busyNative || !seedBackupConfirmed || !confirmOk}
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
                    setConfirmInput('');
                    try {
                      const seed = generateSeedHex();
                      setConfirmStart(pickConfirmStart(seed));
                      setNewSeed(seed);
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
                  {fieldInput(nativeConnector.id, f)}
                </label>
              ))}
              <button
                type="button"
                className="sqc-btn sqc-btn--ghost"
                disabled={!canImport || busyNative || status === 'connecting'}
                onClick={() => void onConnect(nativeConnector.id, readFields(nativeConnector.id, nativeFields), true)}
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
                // Seed needs create/import; extension connects immediately.
                connectorItem(c, () =>
                  (c.connectFields?.length ?? 0) > 0
                    ? setNativeId(c.id)
                    : void onConnect(c.id),
                ),
              )}
            </ul>
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
                    const ok = fields.length === 0 || fields.every((f) => filled[fieldKey(c.id, f.name)]);
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
                            {fieldInput(c.id, f)}
                          </label>
                        ))}
                        <button
                          type="button"
                          className="sqc-btn sqc-btn--ghost"
                          disabled={!ok || status === 'connecting'}
                          onClick={() => void onConnect(c.id, readFields(c.id, fields), true)}
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
