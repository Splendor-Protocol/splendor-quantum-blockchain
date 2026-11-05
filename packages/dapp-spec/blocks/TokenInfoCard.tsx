import type { ReactNode } from 'react';
import { WidgetShell, connectErrorMessage, useTokenInfo } from '@splendor/connect-react';
import { formatUnits, pulse } from './parseList';

export type TokenInfoCardProps = {
  title?: string;
  /** Contract id picked in the studio. Replaced by `tokenAddress` before render; ignored here. */
  contractId?: string;
  tokenAddress?: string;
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
