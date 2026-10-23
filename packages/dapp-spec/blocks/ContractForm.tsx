import { useEffect, useId, useMemo, useState } from 'react';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import {
  ConnectButton,
  WidgetShell,
  connectErrorMessage,
  useAccount,
  useContract,
  useNetwork,
  usePendingTransactions,
} from '@splendor/connect-react';
import { pageHref } from './pageHref';

export type ContractFormProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `contract` before render; ignored here. */
  contractId?: string;
  contract?: string;
  functionSig?: string;
  buttonLabel?: string;
  successMessage?: string;
  /** Page path (or URL) offered as a "Continue" link once the transaction is confirmed. */
  successPath?: string;
  /** ABI params of the picked function as JSON `[{ name, type }]`. Added by the studio binding. */
  fnParams?: string;
};

type Param = { name: string; type: string };

const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const UINT_RE = /^(0|[1-9]\d*)$/;

/** `transfer(address,uint256)` → `['address', 'uint256']`. */
function sigTypes(sig: string): string[] {
  const m = /\(([^()]*)\)\s*$/.exec(sig);
  return m && m[1]!.trim() ? m[1]!.split(',').map((t) => t.trim()) : [];
}

function paramsOf(fnParams: string | undefined, sig: string): Param[] {
  const types = sigTypes(sig);
  let named: Param[] = [];
  if (fnParams) {
    try {
      const list: unknown = JSON.parse(fnParams);
      if (Array.isArray(list)) {
        named = list
          .filter((p): p is { name?: unknown; type: string } => !!p && typeof p === 'object' && typeof (p as { type?: unknown }).type === 'string')
          .map((p) => ({ name: typeof p.name === 'string' ? p.name : '', type: p.type }));
      }
    } catch {
      /* fall back to the signature */
    }
  }
  // The signature wins when the two disagree (stale binding).
  if (named.length === types.length && named.every((p, i) => p.type === types[i])) return named;
  return types.map((type) => ({ name: '', type }));
}

const bitsOf = (type: string, prefix: 'uint' | 'int'): number => {
  const n = Number(type.slice(prefix.length));
  return Number.isInteger(n) && n > 0 && n <= 256 ? n : 256;
};

/** Error text for one field, or null when the value is acceptable. */
function fieldError(type: string, value: string): string | null {
  const t = type.trim();
  if (t === 'bool') return null;
  const v = value.trim();
  if (!v) return 'Required';
  if (t === 'address' || t === 'sqaddress') return ADDRESS_RE.test(v) ? null : 'Use an address like SQ: followed by 64 hex characters';
  if (/^uint\d*$/.test(t)) {
    if (!UINT_RE.test(v)) return 'Enter a whole number (0 or more)';
    return BigInt(v) < 1n << BigInt(bitsOf(t, 'uint')) ? null : 'That number is too large for this field';
  }
  if (/^int\d*$/.test(t)) {
    if (!UINT_RE.test(v)) return 'Enter a whole number (0 or more)';
    return BigInt(v) < 1n << BigInt(bitsOf(t, 'int') - 1) ? null : 'That number is too large for this field';
  }
  if (/^bytes\d*$/.test(t)) return /^0x[0-9a-fA-F]+$/.test(v) ? null : 'Enter 0x-prefixed hex';
  return null;
}

/** Argument as the SDK expects it (booleans are 1 / 0). */
const toArg = (type: string, value: string): string => (type.trim() === 'bool' ? (value === 'true' ? '1' : '0') : value.trim());

const hint = (type: string) => (type === 'bool' ? 'yes / no' : type);

/** One labelled, typed input per contract function argument, with inline transaction status. */
export function ContractForm({
  title = 'Contract form',
  contract = '',
  functionSig = '',
  buttonLabel = 'Submit',
  successMessage = 'Done!',
  successPath = '',
  fnParams,
}: ContractFormProps) {
  const uid = useId();
  const sig = functionSig.trim();
  const { address } = useAccount();
  const { network } = useNetwork();
  const c = useContract(contract || null);
  const { all: pendingAll } = usePendingTransactions({ toast: false });
  const params = useMemo(() => paramsOf(fnParams, sig), [fnParams, sig]);
  const paramsKey = params.map((p) => p.type).join(',');
  const [values, setValues] = useState<string[]>([]);
  const [attempted, setAttempted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hash, setHash] = useState<string | null>(null);

  useEffect(() => {
    setValues(params.map((p) => (p.type.trim() === 'bool' ? 'false' : '')));
    setAttempted(false);
    setError(null);
    setHash(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig, paramsKey, contract]);

  if (!contract || !sig) {
    return (
      <WidgetShell kind="contract-form" title={title} gate={false}>
        <p className="sqc-widget__msg">Pick a contract and a function in Content.</p>
      </WidgetShell>
    );
  }

  const errors = params.map((p, i) => fieldError(p.type, values[i] ?? ''));
  const bare = (h: string) => h.toLowerCase().replace(/^0x/, '');
  const isPending = hash != null && pendingAll.some((p) => bare(p.hash) === bare(hash));
  const confirmed = hash != null && !isPending && !busy;
  const explorer = hash && network ? txExplorerUrl(network, hash) : null;
  const href = successPath.trim() ? pageHref(successPath) : '';

  const submit = async () => {
    setAttempted(true);
    if (!address || busy || errors.some(Boolean)) return;
    setBusy(true);
    setError(null);
    setHash(null);
    try {
      const res = await c.call(
        sig,
        params.map((p, i) => toArg(p.type, values[i] ?? '')),
        { waitForConfirmation: true },
      );
      setHash(res.hash ?? null);
    } catch (e) {
      setError(connectErrorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <WidgetShell kind="contract-form" title={title} gate={false} status={busy || isPending ? 'pending' : 'idle'}>
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        style={{ display: 'grid', gap: '0.75rem' }}
      >
        <p className="sqc-widget__msg dapp-mono" style={{ margin: 0 }}>
          {sig}
        </p>
        {params.map((p, i) => {
          const id = `${uid}-${i}`;
          const label = p.name || `Argument ${i + 1}`;
          const err = attempted ? errors[i] : null;
          const value = values[i] ?? '';
          const set = (v: string) => setValues((cur) => cur.map((x, j) => (j === i ? v : x)));
          const isBool = p.type.trim() === 'bool';
          return (
            <div key={id} className="sqc-field">
              {isBool ? (
                <label htmlFor={id} style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                  <input id={id} type="checkbox" checked={value === 'true'} disabled={busy} onChange={(e) => set(e.target.checked ? 'true' : 'false')} />
                  <span>{label}</span>
                  <span className="sqc-widget__msg dapp-mono">{hint(p.type)}</span>
                </label>
              ) : (
                <>
                  <label className="sqc-widget__label" htmlFor={id}>
                    {label} <span className="sqc-widget__msg dapp-mono">{hint(p.type)}</span>
                  </label>
                  <input
                    id={id}
                    className="sqc-input"
                    value={value}
                    disabled={busy}
                    autoComplete="off"
                    inputMode={/^u?int/.test(p.type) ? 'numeric' : undefined}
                    placeholder={p.type === 'address' ? 'SQ:…' : undefined}
                    aria-invalid={err ? true : undefined}
                    aria-describedby={err ? `${id}-err` : undefined}
                    onChange={(e) => set(e.target.value)}
                  />
                </>
              )}
              {err ? (
                <p id={`${id}-err`} className="sqc-widget__msg sqc-widget__msg--error" role="alert" style={{ margin: '0.25rem 0 0' }}>
                  {err}
                </p>
              ) : null}
            </div>
          );
        })}
        <button type="submit" className="sqc-btn sqc-btn--primary sqc-btn--block" disabled={!address || busy || isPending}>
          {busy ? 'Sending…' : buttonLabel}
        </button>
      </form>
      {!address ? (
        <div style={{ marginTop: '0.75rem' }}>
          <p className="sqc-widget__msg">Connect a wallet to send this transaction.</p>
          <ConnectButton />
        </div>
      ) : null}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {error}
        </p>
      ) : null}
      {hash ? (
        <div role="status" aria-live="polite" style={{ marginTop: '0.75rem', display: 'grid', gap: '0.4rem' }}>
          <p className="sqc-widget__msg" style={{ margin: 0 }}>
            <strong>{isPending ? 'Pending…' : 'Confirmed'}</strong>{' '}
            <span className="dapp-mono" title={hash}>
              {hash.slice(0, 10)}…{hash.slice(-6)}
            </span>
            {explorer ? (
              <>
                {' '}
                <a href={explorer} target="_blank" rel="noreferrer">
                  View in explorer
                </a>
              </>
            ) : null}
          </p>
          {confirmed ? <p style={{ margin: 0 }}>{successMessage}</p> : null}
          {confirmed && href ? (
            <a className="sqc-btn sqc-btn--ghost" href={href}>
              Continue
            </a>
          ) : null}
        </div>
      ) : null}
    </WidgetShell>
  );
}
