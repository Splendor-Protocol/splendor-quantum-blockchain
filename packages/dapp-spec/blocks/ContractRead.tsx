import { useEffect, useRef, useState } from 'react';
import { WidgetShell, connectErrorMessage, useContract } from '@splendor/connect-react';

export type ContractReadProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `contract` before render; ignored here. */
  contractId?: string;
  contract?: string;
  functionSig?: string;
  args?: string;
  decode?: string;
  /** Token decimals: with `decode="uint256"`, show the number as a token amount. */
  decimals?: number;
  /** Auto-refresh interval in ms. 0 turns it off; 1 to 999 are treated as 1000. */
  refreshMs?: number;
  /** ABI params of the picked function (JSON). Passed by generated code; not used here. */
  fnParams?: string;
};

function parseArgs(raw: string): string[] {
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Raw integer units → "1,234.56" (thousands separators, at most 2 decimals, truncated). */
function formatUnits(raw: bigint, decimals: number, maxFrac = 2): string {
  const base = 10n ** BigInt(decimals);
  const whole = (raw / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (raw % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${whole}${frac ? `.${frac}` : ''}`;
}

const pulse = (el: HTMLElement | null) => {
  el?.animate?.([{ opacity: 0.35 }, { opacity: 0.8 }], { duration: 900, iterations: Infinity, direction: 'alternate' });
};

/** One load generation: set `cancelled` when its inputs change or the block unmounts. */
type Run = { cancelled: boolean };

/** Live read of one contract function with optional auto-refresh. */
export function ContractRead({
  title = 'On-chain value',
  contract = '',
  functionSig = 'get()',
  args = '',
  decode = 'uint256',
  decimals,
  refreshMs = 0,
}: ContractReadProps) {
  const c = useContract(contract || null);
  const [value, setValue] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const run = useRef<Run>({ cancelled: false });
  const interval = refreshMs > 0 ? Math.max(1000, refreshMs) : 0;
  const dec = Number.isInteger(decimals) && decimals! > 0 && decimals! <= 36 ? decimals! : 0;

  const load = async (r: Run) => {
    if (!contract || !functionSig) {
      setBusy(false);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const list = parseArgs(args);
      let next: string;
      if (decode === 'uint256') {
        const n = await c.readUint256(functionSig, list);
        next = dec ? formatUnits(n, dec) : n.toString();
      } else {
        const res = await c.read(functionSig, list);
        if (res.reverted) throw new Error(res.revert_reason || 'reverted');
        next = res.return_data || '0x';
      }
      if (!r.cancelled) setValue(next);
    } catch (e) {
      if (!r.cancelled) {
        setError(connectErrorMessage(e));
        setValue(null);
      }
    } finally {
      if (!r.cancelled) setBusy(false);
    }
  };

  useEffect(() => {
    const r: Run = { cancelled: false };
    run.current = r;
    setValue(null);
    void load(r);
    const t = interval ? window.setInterval(() => void load(r), interval) : undefined;
    return () => {
      r.cancelled = true;
      if (t !== undefined) window.clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contract, functionSig, args, decode, dec, interval]);

  if (!contract) {
    return (
      <WidgetShell kind="contract-read" title={title} gate={false}>
        <p className="sqc-widget__msg">Pick a contract in Content.</p>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell kind="contract-read" title={title} gate={false}>
      <p aria-live="polite" aria-busy={value == null && !error} style={{ fontSize: '1.5rem', fontWeight: 700, margin: '0.25rem 0' }}>
        {value != null ? (
          value
        ) : error ? (
          '—'
        ) : (
          <span
            ref={pulse}
            aria-hidden="true"
            style={{ display: 'inline-block', width: '4rem', height: '1.4rem', borderRadius: 6, background: 'var(--sqc-border, #2c3242)' }}
          />
        )}
      </p>
      <p className="sqc-widget__msg dapp-mono" style={{ margin: 0 }}>
        {functionSig}
      </p>
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {error}
        </p>
      ) : null}
      <button type="button" className="sqc-btn sqc-btn--ghost" style={{ marginTop: '0.75rem' }} onClick={() => void load(run.current)} disabled={busy}>
        Refresh
      </button>
    </WidgetShell>
  );
}
