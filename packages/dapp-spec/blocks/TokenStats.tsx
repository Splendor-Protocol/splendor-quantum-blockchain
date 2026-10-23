import type { ReactNode } from 'react';
import { WidgetShell, connectErrorMessage, useAccount, useTokenBalance, useTokenInfo } from '@splendor/connect-react';

export type TokenStatsProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `tokenAddress` before render; ignored here. */
  contractId?: string;
  tokenAddress?: string;
  /** Fallback decimals when the chain does not report valid ones. */
  decimals?: number;
  showSupply?: boolean;
  showBalance?: boolean;
  layout?: 'row' | 'card';
};

/** Raw integer units â†’ "1,234.56" (thousands separators, at most `maxFrac` decimals, truncated). */
function formatUnits(raw: unknown, decimals: number, maxFrac = 2): string {
  let v: bigint;
  try {
    v = BigInt(String(raw));
  } catch {
    return String(raw ?? 'â€”');
  }
  const neg = v < 0n;
  if (neg) v = -v;
  const base = 10n ** BigInt(decimals);
  const whole = (v / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (v % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${neg ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`;
}

const pulse = (el: HTMLElement | null) => {
  el?.animate?.([{ opacity: 0.35 }, { opacity: 0.8 }], { duration: 900, iterations: Infinity, direction: 'alternate' });
};

function Skeleton({ width = '6rem' }: { width?: string }) {
  return (
    <span
      ref={pulse}
      aria-hidden="true"
      data-testid="dapp-skeleton"
      style={{ display: 'inline-block', width, height: '1.1em', borderRadius: 4, verticalAlign: 'middle', background: 'var(--sqc-border, #2c3242)' }}
    />
  );
}

/** Name, symbol, total supply and (optionally) the connected wallet's balance of a QRC-20. */
export function TokenStats({
  title = 'Token stats',
  tokenAddress = '',
  decimals: fallbackDecimals,
  showSupply = true,
  showBalance = true,
  layout = 'card',
}: TokenStatsProps) {
  const { address } = useAccount();
  const info = useTokenInfo(tokenAddress || null);
  const balance = useTokenBalance(tokenAddress || null, showBalance && address ? address : null);

  if (!tokenAddress) {
    return (
      <WidgetShell kind="token-stats" title={title} gate={false}>
        <p className="sqc-widget__msg">Connect your token in Setup or pick one in Content.</p>
      </WidgetShell>
    );
  }
  if (info.error) {
    return (
      <WidgetShell kind="token-stats" title={title} gate={false}>
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(info.error)}
        </p>
      </WidgetShell>
    );
  }

  const d = info.data;
  const loading = !d;
  const decimals = d?.decimals ?? (Number.isInteger(fallbackDecimals) && fallbackDecimals! >= 0 && fallbackDecimals! <= 36 ? fallbackDecimals! : 18);
  const supplyRaw = d?.totalSupply ?? (typeof d?.total_supply === 'string' ? d.total_supply : undefined);
  const symbol = d?.symbol ?? '';

  const rows: { key: string; label: string; value: ReactNode }[] = [
    { key: 'name', label: 'Name', value: loading ? <Skeleton /> : (d.name ?? 'â€”') },
    { key: 'symbol', label: 'Symbol', value: loading ? <Skeleton width="3rem" /> : (symbol || 'â€”') },
  ];
  if (showSupply) {
    rows.push({
      key: 'supply',
      label: 'Total supply',
      value: loading ? <Skeleton width="8rem" /> : supplyRaw == null ? 'â€”' : `${formatUnits(supplyRaw, decimals)}${symbol ? ` ${symbol}` : ''}`,
    });
  }
  if (showBalance) {
    rows.push({
      key: 'balance',
      label: 'Your balance',
      value: !address ? (
        <span className="sqc-widget__msg">Connect a wallet</span>
      ) : balance.data == null ? (
        balance.error ? 'â€”' : <Skeleton width="5rem" />
      ) : (
        `${formatUnits(balance.data, decimals)}${symbol ? ` ${symbol}` : ''}`
      ),
    });
  }

  const isRow = layout === 'row';
  return (
    <WidgetShell kind="token-stats" title={title} gate={false}>
      <dl
        aria-busy={loading}
        style={
          isRow
            ? { display: 'flex', flexWrap: 'wrap', gap: '1.5rem', margin: 0 }
            : { display: 'grid', gap: '0.75rem', margin: 0 }
        }
      >
        {rows.map((r) => (
          <div
            key={r.key}
            style={
              isRow
                ? { display: 'flex', flexDirection: 'column', gap: '0.15rem' }
                : { display: 'flex', justifyContent: 'space-between', gap: '1rem', borderBottom: '1px solid var(--sqc-border, #262b38)', paddingBottom: '0.5rem' }
            }
          >
            <dt style={{ margin: 0, fontSize: '0.8rem', color: 'var(--sqc-text-muted, #9aa4b2)' }}>{r.label}</dt>
            <dd style={{ margin: 0, fontWeight: 600, fontSize: isRow ? '1.25rem' : '1rem' }}>{r.value}</dd>
          </div>
        ))}
      </dl>
    </WidgetShell>
  );
}
