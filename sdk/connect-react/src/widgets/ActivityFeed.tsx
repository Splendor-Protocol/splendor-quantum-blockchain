import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { toUnits } from '@splendor/sqc-sdk';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { connectErrorMessage } from '../core/controller.js';
import { useActivity, useAddressBook, useNetwork, useReadConnection, type ActivityItem } from '../react/hooks.js';
import { loadTokens, validDecimals } from '../react/hooks/qrc20.js';
import { truncateAddress } from './utils.js';
import { ACTIVITY_LABELS, formatLabel, useWidgetLabels, type ActivityFeedLabels } from './labels.js';
import { WidgetShell } from './parts/WidgetShell.js';

export type ActivityFeedProps = {
  className?: string;
  style?: CSSProperties;
  title?: ReactNode;
  hideTitle?: boolean;
  labels?: Partial<ActivityFeedLabels>;
  /** Blocks to look back (max 2000 without an indexer). Default 2000. */
  blocks?: number;
  /** Rows to show. Default 25. */
  limit?: number;
  /** Poll interval (ms). Default 15000. */
  pollMs?: number;
  /** Indexer base URL serving `/idx/v1/logs`; lifts the block window. */
  indexerUrl?: string;
  /** Show this browser's unconfirmed sends at the top. Default true. */
  showPending?: boolean;
};

type Meta = { symbol: string | null; decimals: number | null };

/** Symbol and decimals per QRC-20 contract, fetched once each. */
function useTokenMetas(contracts: string[]): Map<string, Meta> {
  const conn = useReadConnection();
  const [metas, setMetas] = useState<Map<string, Meta>>(() => new Map());
  const key = contracts.join(',');
  useEffect(() => {
    if (!conn) return;
    const missing = contracts.filter((c) => !metas.has(c));
    if (!missing.length) return;
    let cancelled = false;
    void (async () => {
      const { QRC20 } = await loadTokens();
      const found = await Promise.all(
        missing.map(async (c): Promise<[string, Meta]> => {
          try {
            const info = (await new QRC20(conn, c).tokenInfo()) as Record<string, unknown>;
            return [c, { symbol: typeof info.symbol === 'string' ? info.symbol : null, decimals: validDecimals(info.decimals) }];
          } catch {
            return [c, { symbol: null, decimals: null }];
          }
        }),
      );
      if (cancelled) return;
      setMetas((prev) => {
        const next = new Map(prev);
        for (const [c, m] of found) next.set(c, m);
        return next;
      });
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conn, key]);
  return metas;
}

function amountText(item: ActivityItem, meta: Meta | undefined): string {
  if (item.kind === 'multi-token' || meta?.decimals == null) return item.amount;
  try {
    return toUnits(BigInt(item.amount), meta.decimals);
  } catch {
    return item.amount;
  }
}

/**
 * Recent token transfers for the connected account, from chain event logs, with pending sends
 * on top. Native SPLD transfers and QRC-721 moves emit no logs, so they show only while pending.
 */
export function ActivityFeed({
  className,
  style,
  title,
  hideTitle,
  labels,
  blocks,
  limit = 25,
  pollMs = 15_000,
  indexerUrl,
  showPending = true,
}: ActivityFeedProps) {
  const l = useWidgetLabels(ACTIVITY_LABELS, labels, 'activity');
  const { network } = useNetwork();
  const { labelOf } = useAddressBook();
  const activity = useActivity(null, { blocks, limit, pollMs, indexerUrl });
  const tokenContracts = useMemo(
    () => [...new Set(activity.items.filter((i) => i.kind === 'token').map((i) => i.contract))],
    [activity.items],
  );
  const metas = useTokenMetas(tokenContracts);
  const who = (a: string) => labelOf(a) ?? truncateAddress(a, 8, 6);

  const directionText = (i: ActivityItem) =>
    i.mint ? l.minted : i.burn ? l.burned : i.direction === 'in' ? l.received : i.direction === 'out' ? l.sent : l.self;

  return (
    <WidgetShell
      kind="activity"
      className={className}
      style={style}
      title={title ?? l.title}
      hideTitle={hideTitle}
      gateLabels={l}
      headerExtra={
        <button type="button" className="sqc-copy" onClick={() => void activity.refresh()} disabled={activity.loading}>
          {activity.loading ? '…' : l.refresh}
        </button>
      }
    >
      {activity.error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {formatLabel(l.loadFailed, { error: connectErrorMessage(activity.error) })}
        </p>
      ) : null}
      <ul className="sqc-list sqc-activity" aria-label={l.title}>
        {showPending
          ? activity.pending.map((p) => {
              const href = network ? txExplorerUrl(network, p.hash) : null;
              return (
                <li key={p.hash} className="sqc-list__item sqc-activity__item--pending" data-testid="sqc-activity-pending">
                  <span className="sqc-list__label">{l.pending}</span>
                  <span className="sqc-list__value">{p.summary}</span>
                  {href ? (
                    <a className="sqc-account-link" href={href} target="_blank" rel="noopener noreferrer">
                      {l.explorer}
                    </a>
                  ) : null}
                </li>
              );
            })
          : null}
        {activity.items.map((i) => {
          const meta = metas.get(i.contract);
          const symbol = i.kind === 'token' ? (meta?.symbol ?? formatLabel(l.token, { address: truncateAddress(i.contract, 8, 4) })) : formatLabel(l.token, { address: truncateAddress(i.contract, 8, 4) });
          const counterparty = i.direction === 'in' ? i.from : i.to;
          const href = network && i.txHash ? txExplorerUrl(network, i.txHash) : null;
          return (
            <li key={i.id} className={`sqc-list__item sqc-activity__item--${i.direction}`} data-testid="sqc-activity-item" data-direction={i.direction}>
              <span className="sqc-list__label">{directionText(i)}</span>
              <span className="sqc-list__value">
                {amountText(i, meta)} {symbol}
                {i.tokenId ? ` ${formatLabel(l.tokenId, { id: i.tokenId })}` : ''}
                {!i.mint && !i.burn && i.direction !== 'self' ? (
                  <span className="sqc-account-row__muted" title={counterparty}>
                    {i.direction === 'in' ? `${l.reviewFrom} ` : `${l.reviewTo} `}
                    {who(counterparty)}
                  </span>
                ) : null}
              </span>
              {href ? (
                <a className="sqc-account-link" href={href} target="_blank" rel="noopener noreferrer" title={i.txHash ?? undefined}>
                  {i.blockHeight != null ? formatLabel(l.block, { height: i.blockHeight }) : l.explorer}
                </a>
              ) : i.blockHeight != null ? (
                <span className="sqc-account-row__muted">{formatLabel(l.block, { height: i.blockHeight })}</span>
              ) : null}
            </li>
          );
        })}
      </ul>
      {activity.data && !activity.items.length && !(showPending && activity.pending.length) ? (
        <p className="sqc-widget__hint">{formatLabel(l.empty, { blocks: activity.data.toBlock - activity.data.fromBlock })}</p>
      ) : null}
      {!activity.data && activity.loading ? <p className="sqc-widget__hint">{l.loading}</p> : null}
      {activity.data?.truncated ? <p className="sqc-widget__hint">{l.truncated}</p> : null}
    </WidgetShell>
  );
}
