import type { ReactNode } from 'react';
import { WidgetShell, connectErrorMessage, useTokenInfo } from '@splendor/connect-react';

export type TokenInfoCardProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `tokenAddress` before render; ignored here. */
  contractId?: string;
  tokenAddress?: string;
};

/** Raw integer units → "1,234.56" (thousands separators, at most 2 decimals, truncated). */
function formatUnits(raw: unknown, decimals: number, maxFrac = 2): string {
  let v: bigint;
  try {
    v = BigInt(String(raw));
  } catch {
    return String(raw ?? '—');
  }
  const base = 10n ** BigInt(decimals);
  const whole = (v / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (v % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${whole}${frac ? `.${frac}` : ''}`;
}

const pulse = (el: HTMLElement | null) => {
  el?.animate?.([{ opacity: 0.35 }, { opacity: 0.8 }], { duration: 900, iterations: Infinity, direction: 'alternate' });
};

function Skeleton({ width = '6rem' }: { width?: string }) {
  return (
    <span
      ref={pulse}
      aria-hidden="true"
      style={{ display: 'inline-block', width, height: '1.1em', borderRadius: 4, verticalAlign: 'middle', background: 'var(--sqc-border, #2c3242)' }}
    />
  );
}

/** Shows name, symbol, decimals and total supply for a QRC-20. */
export function TokenInfoCard({ title = 'Token', tokenAddress = '' }: TokenInfoCardProps) {
  const info = useTokenInfo(tokenAddress || null);

  if (!tokenAddress) {
    return (
      <WidgetShell kind="token-info" title={title} gate={false}>
        <p className="sqc-widget__msg">Pick a contract in Content.</p>
      </WidgetShell>
    );
  }

  if (info.error) {
    return (
      <WidgetShell kind="token-info" title={title} gate={false}>
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(info.error)}
        </p>
      </WidgetShell>
    );
  }

  const d = info.data;
  const supplyRaw = d?.totalSupply ?? (typeof d?.total_supply === 'string' ? d.total_supply : undefined);
  const rows: [string, ReactNode][] = [
    ['Name', d ? (d.name ?? '—') : <Skeleton key="n" />],
    ['Symbol', d ? (d.symbol ?? '—') : <Skeleton key="s" width="3rem" />],
    ['Decimals', d ? (d.decimals ?? '—') : <Skeleton key="d" width="2rem" />],
  ];
  if (!d || supplyRaw != null) {
    rows.push([
      'Total supply',
      d && supplyRaw != null ? `${formatUnits(supplyRaw, d.decimals ?? 18)}${d.symbol ? ` ${d.symbol}` : ''}` : <Skeleton key="t" width="8rem" />,
    ]);
  }

  return (
    <WidgetShell kind="token-info" title={title} gate={false}>
      <dl className="dapp-stats" aria-busy={!d}>
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </WidgetShell>
  );
}
