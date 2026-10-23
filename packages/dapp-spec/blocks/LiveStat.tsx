import { WidgetShell, connectErrorMessage, useReadContract } from '@splendor/connect-react';

export type LiveStatProps = {
  label?: string;
  /** Contract id picked in the studio. Replaced by `contract` before render; ignored here. */
  contractId?: string;
  contract?: string;
  functionSig?: string;
  /** Comma-separated arguments for the read call. */
  args?: string;
  format?: 'number' | 'token' | 'address' | 'text' | 'bool';
  /** Decimals for `format="token"` (default 18). */
  decimals?: number;
  suffix?: string;
  /** Auto-refresh in ms. 0 = off; 1 to 999 are treated as 1000. */
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

function formatUnits(raw: bigint, decimals: number, maxFrac = 2): string {
  const base = 10n ** BigInt(decimals);
  const whole = (raw / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (raw % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${whole}${frac ? `.${frac}` : ''}`;
}

const pulse = (el: HTMLElement | null) => {
  el?.animate?.([{ opacity: 0.35 }, { opacity: 0.8 }], { duration: 900, iterations: Infinity, direction: 'alternate' });
};

/** Hex digits of return data without the 0x prefix, or null when it is not hex. */
function hexBody(data: string | undefined): string | null {
  const h = (data ?? '').trim().replace(/^0x/i, '');
  return h.length > 0 && h.length % 2 === 0 && /^[0-9a-fA-F]+$/.test(h) ? h : null;
}

/** First 32-byte word as an unsigned integer (like `readUint256`), or null. */
function firstWord(hex: string | null): bigint | null {
  return hex ? BigInt(`0x${hex.slice(0, 64)}`) : null;
}

/** ABI `string` (offset, length, bytes) or printable UTF-8 bytes; null when neither fits. */
function decodeText(hex: string | null): string | null {
  if (!hex) return null;
  const bytes = (h: string) => new Uint8Array(h.match(/../g)!.map((b) => parseInt(b, 16)));
  let body = hex;
  if (hex.length >= 128 && BigInt(`0x${hex.slice(0, 64)}`) === 32n) {
    const len = Number(BigInt(`0x${hex.slice(64, 128)}`));
    if (Number.isSafeInteger(len) && hex.length >= 128 + len * 2) body = hex.slice(128, 128 + len * 2);
  }
  body = body.replace(/(00)+$/, '');
  if (!body) return null;
  try {
    const s = new TextDecoder('utf-8', { fatal: true }).decode(bytes(body));
    // eslint-disable-next-line no-control-regex
    return /^[^\x00-\x08\x0e-\x1f\x7f]+$/.test(s) ? s : null;
  } catch {
    return null;
  }
}

function present(data: string | undefined, format: string, decimals: number): string {
  const hex = hexBody(data);
  const raw = data && data !== '0x' ? data : '0x';
  if (format === 'number') {
    const n = firstWord(hex);
    return n == null ? raw : formatUnits(n, 0);
  }
  if (format === 'token') {
    const n = firstWord(hex);
    return n == null ? raw : formatUnits(n, decimals);
  }
  if (format === 'bool') {
    const n = firstWord(hex);
    return n == null ? raw : n !== 0n ? 'Yes' : 'No';
  }
  if (format === 'address') {
    if (hex && hex.length >= 64) {
      const a = `SQ:${hex.slice(0, 64).toLowerCase()}`;
      return `${a.slice(0, 11)}…${a.slice(-6)}`;
    }
    return raw;
  }
  return decodeText(hex) ?? raw;
}

/** KPI tile that shows one live contract read. */
export function LiveStat({
  label = 'Live value',
  contract = '',
  functionSig = '',
  args = '',
  format = 'number',
  decimals,
  suffix = '',
  refreshMs = 0,
}: LiveStatProps) {
  const sig = functionSig.trim();
  const pollMs = refreshMs > 0 ? Math.max(1000, refreshMs) : 0;
  const read = useReadContract(contract || null, sig || null, parseArgs(args), { pollMs });

  if (!contract || !sig) {
    return (
      <WidgetShell kind="live-stat" title={label} gate={false}>
        <p className="sqc-widget__msg">Pick a contract and a function in Content.</p>
      </WidgetShell>
    );
  }

  const d = read.data;
  const failure = read.error ? connectErrorMessage(read.error) : d?.reverted ? d.revert_reason || 'The call reverted' : null;
  const dec = Number.isInteger(decimals) && decimals! >= 0 && decimals! <= 36 ? decimals! : 18;
  const value = d && !d.reverted ? present(d.return_data, format, dec) : null;

  return (
    <WidgetShell kind="live-stat" title={label} gate={false}>
      <p
        aria-live="polite"
        aria-busy={value == null && !failure}
        data-testid="dapp-live-stat"
        style={{ fontSize: '2rem', fontWeight: 800, margin: '0.25rem 0', overflowWrap: 'anywhere' }}
      >
        {value != null ? (
          <>
            {value}
            {suffix ? <span style={{ fontSize: '1rem', fontWeight: 600, marginLeft: '0.35rem' }}>{suffix}</span> : null}
          </>
        ) : failure ? (
          '—'
        ) : (
          <span
            ref={pulse}
            aria-hidden="true"
            style={{ display: 'inline-block', width: '5rem', height: '1.6rem', borderRadius: 6, background: 'var(--sqc-border, #2c3242)' }}
          />
        )}
      </p>
      {failure ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {failure}
        </p>
      ) : null}
    </WidgetShell>
  );
}
