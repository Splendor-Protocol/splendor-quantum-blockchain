import { useMemo, type CSSProperties, type ReactNode } from 'react';
import { useAccount, useBalance, useTokenBalance, useTokenInfo } from '../react/hooks.js';
import { normalizeSqAddress, truncateAddress } from './utils.js';
import { BALANCES_LABELS, useWidgetLabels, type TokenBalancesLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';

/** Max tokens one widget reads, so a long list cannot flood the node. */
export const TOKEN_BALANCES_MAX = 20;

export type TokenBalancesProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<TokenBalancesLabels>;
  /** QRC-20 addresses, as an array or a comma / whitespace separated string. Invalid entries are skipped. */
  tokens?: string | string[];
  /** Show the native SPLD row. Default true. */
  showNative?: boolean;
  /** Hide tokens with a zero balance. Default false. */
  hideZero?: boolean;
  /** Poll interval for token balances (ms). Default 12000. */
  pollMs?: number;
};

/** Parse the `tokens` prop into unique canonical addresses (at most TOKEN_BALANCES_MAX). */
export function parseTokenList(tokens: string | string[] | undefined): string[] {
  const raw = Array.isArray(tokens) ? tokens : (tokens ?? '').split(/[\s,]+/);
  const out: string[] = [];
  for (const t of raw) {
    const a = normalizeSqAddress(t);
    if (a && !out.includes(a)) out.push(a);
    if (out.length >= TOKEN_BALANCES_MAX) break;
  }
  return out;
}

function TokenRow({ token, owner, hideZero, pollMs, l }: { token: string; owner: string; hideZero: boolean; pollMs: number; l: TokenBalancesLabels }) {
  const info = useTokenInfo(token);
  const bal = useTokenBalance(token, owner, { pollMs });
  if (hideZero && bal.data === '0') return null;
  const symbol = typeof info.data?.symbol === 'string' ? info.data.symbol : truncateAddress(token, 8, 4);
  return (
    <li className="sqc-list__item" data-testid="sqc-token-balance" title={token}>
      <span className="sqc-list__label">{symbol}</span>
      <span className="sqc-list__value">{bal.formatted ?? (bal.loading ? l.loading : l.unknown)}</span>
    </li>
  );
}

/** SPLD plus a list of QRC-20 balances for the connected account. */
export function TokenBalances({
  className,
  style,
  title,
  hideTitle,
  labels,
  tokens,
  showNative = true,
  hideZero = false,
  pollMs = 12_000,
}: TokenBalancesProps) {
  const l = useWidgetLabels(BALANCES_LABELS, labels, 'balances');
  const { address } = useAccount();
  const { balanceSPLD, loading } = useBalance();
  const list = useMemo(() => parseTokenList(tokens), [tokens]);
  return (
    <WidgetShell kind="balances" className={className} style={style} title={title ?? l.title} hideTitle={hideTitle} gateLabels={l}>
      {address ? (
        <ul className="sqc-list" aria-label={l.title}>
          {showNative ? (
            <li className="sqc-list__item" data-testid="sqc-native-balance">
              <span className="sqc-list__label">{l.native}</span>
              <span className="sqc-list__value">{balanceSPLD ?? (loading ? l.loading : l.unknown)}</span>
            </li>
          ) : null}
          {list.map((t) => (
            <TokenRow key={t} token={t} owner={address} hideZero={hideZero} pollMs={pollMs} l={l} />
          ))}
        </ul>
      ) : null}
      {address && !list.length && !showNative ? <p className="sqc-widget__hint">{l.empty}</p> : null}
    </WidgetShell>
  );
}
