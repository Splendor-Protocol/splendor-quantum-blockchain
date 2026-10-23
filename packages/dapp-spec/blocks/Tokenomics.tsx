import { useId } from 'react';
import { WidgetShell, connectErrorMessage, useTokenInfo } from '@splendor/connect-react';

export type TokenomicsProps = {
  title?: string;
  /** One allocation per line: `Label|Percent|Note` (note optional). */
  items?: string;
  /** Contract id picked in the studio. Replaced by `tokenAddress` before render; ignored here. */
  contractId?: string;
  tokenAddress?: string;
  /** Fallback decimals when the chain does not report valid ones. */
  decimals?: number;
  chart?: 'donut' | 'bar';
};

const DEFAULT_ITEMS = 'Community|40|Airdrops and rewards\nTreasury|25|\nTeam|20|4-year vesting\nLiquidity|15|';

/** Fixed palette: the theme accent first, then colours that stay distinct on light and dark. */
const PALETTE = [
  'var(--sqc-accent, #8b6cff)',
  'var(--sqc-success, #3ecf8e)',
  '#f0c14a',
  '#5cb8ff',
  '#ff7a8a',
  '#e98bd8',
  '#ffa657',
  '#9aa4b2',
];

type Row = { label: string; percent: number; note: string; valid: boolean };

/** Rows are separated by newlines only, so notes may contain commas. */
function parseRows(raw: string): Row[] {
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [label = '', pct = '', ...note] = line.split('|').map((s) => s.trim());
      const n = Number(pct.replace(/%$/, '').trim());
      const valid = pct !== '' && Number.isFinite(n) && n >= 0;
      return { label, percent: valid ? n : 0, note: note.join('|'), valid };
    })
    .filter((r) => r.label);
}

function formatUnits(raw: unknown, decimals: number, maxFrac = 2): string {
  let v: bigint;
  try {
    v = BigInt(String(raw));
  } catch {
    return String(raw ?? '—');
  }
  const neg = v < 0n;
  if (neg) v = -v;
  const base = 10n ** BigInt(decimals);
  const whole = (v / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (v % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${neg ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`;
}

const fmtPct = (n: number) => `${Math.round(n * 100) / 100}%`;

/** Token allocation as an accessible donut or bar chart with a legend. */
export function Tokenomics({
  title = 'Tokenomics',
  items = DEFAULT_ITEMS,
  tokenAddress = '',
  decimals: fallbackDecimals,
  chart = 'donut',
}: TokenomicsProps) {
  const uid = useId();
  const info = useTokenInfo(tokenAddress || null);
  const rows = parseRows(items);
  const sum = rows.reduce((a, r) => a + r.percent, 0);
  const denom = Math.max(sum, 100);
  const invalid = rows.filter((r) => !r.valid);
  const off = Math.abs(sum - 100) > 0.01;

  const d = info.data;
  const decimals = d?.decimals ?? (Number.isInteger(fallbackDecimals) && fallbackDecimals! >= 0 && fallbackDecimals! <= 36 ? fallbackDecimals! : 18);
  const supplyRaw = d?.totalSupply ?? (typeof d?.total_supply === 'string' ? d.total_supply : undefined);
  let supply: bigint | null = null;
  try {
    supply = supplyRaw != null ? BigInt(supplyRaw) : null;
  } catch {
    supply = null;
  }
  const symbol = d?.symbol ?? '';
  const amountOf = (percent: number): string | null =>
    supply == null ? null : `${formatUnits((supply * BigInt(Math.round(percent * 100))) / 10000n, decimals)}${symbol ? ` ${symbol}` : ''}`;

  if (rows.length === 0) {
    return (
      <WidgetShell kind="tokenomics" title={title} gate={false}>
        <p className="sqc-widget__msg">Add allocation rows in Content (Group | Share % | Note).</p>
      </WidgetShell>
    );
  }

  const summary = rows.map((r) => `${r.label} ${fmtPct(r.percent)}`).join(', ');
  const color = (i: number) => PALETTE[i % PALETTE.length]!;

  // Donut geometry: a ring of stroked arcs starting at 12 o'clock.
  const R = 44;
  const C = 2 * Math.PI * R;
  let acc = 0;
  const arcs = rows.map((r, i) => {
    const len = (r.percent / denom) * C;
    const arc = { i, len, offset: acc };
    acc += len;
    return arc;
  });

  const BAR_H = 14;
  const BAR_GAP = 8;
  const barsHeight = rows.length * (BAR_H + BAR_GAP);

  return (
    <WidgetShell kind="tokenomics" title={title} gate={false}>
      {info.error && tokenAddress ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(info.error)}
        </p>
      ) : null}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '1.25rem', alignItems: 'center' }}>
        {chart === 'bar' ? (
          <svg
            role="img"
            aria-labelledby={`${uid}-t`}
            viewBox={`0 0 300 ${barsHeight}`}
            style={{ flex: '1 1 220px', minWidth: 200, maxWidth: 420, height: 'auto' }}
          >
            <title id={`${uid}-t`}>{`Token allocation: ${summary}`}</title>
            {rows.map((r, i) => (
              <g key={`${r.label}-${i}`} transform={`translate(0 ${i * (BAR_H + BAR_GAP)})`}>
                <rect x={0} y={0} width={300} height={BAR_H} rx={4} style={{ fill: 'var(--sqc-border, #2c3242)' }} opacity={0.5} />
                <rect x={0} y={0} width={Math.max(0, (r.percent / denom) * 300)} height={BAR_H} rx={4} style={{ fill: color(i) }} />
              </g>
            ))}
          </svg>
        ) : (
          <svg role="img" aria-labelledby={`${uid}-t`} viewBox="0 0 120 120" style={{ width: 180, height: 180, flex: '0 0 auto' }}>
            <title id={`${uid}-t`}>{`Token allocation: ${summary}`}</title>
            <circle cx={60} cy={60} r={R} fill="none" strokeWidth={22} style={{ stroke: 'var(--sqc-border, #2c3242)' }} opacity={0.5} />
            <g transform="rotate(-90 60 60)">
              {arcs.map((a) =>
                a.len > 0 ? (
                  <circle
                    key={a.i}
                    cx={60}
                    cy={60}
                    r={R}
                    fill="none"
                    strokeWidth={22}
                    strokeDasharray={`${a.len} ${C - a.len}`}
                    strokeDashoffset={-a.offset}
                    style={{ stroke: color(a.i) }}
                  />
                ) : null,
              )}
            </g>
            <text x={60} y={58} textAnchor="middle" fontSize={11} fontWeight={700} style={{ fill: 'var(--sqc-text, currentColor)' }}>
              {fmtPct(sum)}
            </text>
            <text x={60} y={72} textAnchor="middle" fontSize={7} style={{ fill: 'var(--sqc-text-muted, #9aa4b2)' }}>
              {symbol || 'allocated'}
            </text>
          </svg>
        )}
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.5rem', flex: '1 1 220px' }}>
          {rows.map((r, i) => {
            const amount = amountOf(r.percent);
            return (
              <li key={`${r.label}-${i}`} style={{ display: 'flex', gap: '0.6rem', alignItems: 'flex-start' }}>
                <span aria-hidden="true" style={{ flex: '0 0 auto', width: 12, height: 12, borderRadius: 3, marginTop: 4, background: color(i) }} />
                <span style={{ minWidth: 0 }}>
                  <strong>{r.label}</strong> <span>{r.valid ? fmtPct(r.percent) : '—'}</span>
                  {amount ? <span className="sqc-widget__msg" style={{ marginLeft: '0.4rem' }}>{amount}</span> : null}
                  {r.note ? (
                    <span className="sqc-widget__msg" style={{ display: 'block', margin: 0 }}>
                      {r.note}
                    </span>
                  ) : null}
                </span>
              </li>
            );
          })}
        </ul>
      </div>
      {off || invalid.length > 0 ? (
        <p className="sqc-widget__msg" role="status" style={{ color: 'var(--sqc-danger, #ff7a8a)' }}>
          {invalid.length > 0
            ? `Check the share of ${invalid.map((r) => r.label).join(', ')}: use a number such as 25. `
            : ''}
          {off ? `Shares add up to ${fmtPct(sum)}, not 100%.` : ''}
        </p>
      ) : null}
    </WidgetShell>
  );
}
