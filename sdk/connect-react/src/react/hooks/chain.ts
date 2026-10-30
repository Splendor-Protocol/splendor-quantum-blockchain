/**
 * Read-only chain views: the active validator set and an address's token activity.
 * Both work without a connected wallet.
 */
import { useCallback, useMemo } from 'react';
import { toUnits } from '@splendor/sqc-sdk';
import type { LogFilter } from '@splendor/sqc-sdk';
import {
  MAX_LOG_BLOCK_RANGE,
  activityFilters,
  mergeActivity,
  safeIndexerUrl,
  toSqAddress,
  type ActivityItem,
} from '../../core/activity.js';
import { useAccount, useReadConnection } from './core.js';
import { usePendingTransactions, type PendingTx } from './pending.js';
import { usePolledRead, type ReadState } from './polledRead.js';

export type { ActivityItem, ActivityDirection, ActivityKind } from '../../core/activity.js';

export type ValidatorStatus = 'active' | 'exiting' | 'inactive';

export type ValidatorInfo = {
  address: string;
  /** Bond in whole SPLD (decimal string). */
  bondSpld: string | null;
  status: ValidatorStatus;
  joinedHeight: number | null;
  exitHeight: number | null;
};

export type ValidatorSet = {
  validators: ValidatorInfo[];
  /** Bond a new validator must lock, in SPLD. */
  bondSpld: string | null;
};

const STATUS: Record<number, ValidatorStatus> = { 0: 'inactive', 1: 'active', 3: 'exiting' };

function intOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : null;
}

/** Atto-SPLD (JSON number or decimal string) as whole SPLD, or null. */
function attoToSpld(v: unknown): string | null {
  const whole = (s: string) => s.replace(/\.0+$/, '');
  try {
    if (typeof v === 'number' && Number.isFinite(v) && v >= 0) return whole(toUnits(BigInt(Math.round(v)), 18));
    if (typeof v === 'string' && /^\d+$/.test(v)) return whole(toUnits(BigInt(v), 18));
  } catch {
    /* fall through */
  }
  return null;
}

/** One record of the `validators.list` module read, or null when it has no usable address. */
export function parseValidator(raw: unknown): ValidatorInfo | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const address = toSqAddress(r.address);
  if (!address) return null;
  return {
    address,
    bondSpld: attoToSpld(r.bond),
    status: STATUS[typeof r.status === 'number' ? r.status : -1] ?? 'inactive',
    joinedHeight: intOrNull(r.joined_height),
    exitHeight: intOrNull(r.exit_height),
  };
}

/** The node reports an empty set as a null result, which the transport raises as "empty". */
function isEmptyResult(e: unknown): boolean {
  return e instanceof Error && /decoded result is empty/i.test(e.message);
}

/**
 * Active validators and the registration bond, from the routed `validators` module reads.
 * `isApproved` checks a standing admission approval for an address.
 */
export function useValidators(opts: { pollMs?: number } = {}): ReadState<ValidatorSet> & {
  validators: ValidatorInfo[];
  isApproved: (address: string) => Promise<boolean>;
} {
  const conn = useReadConnection();
  const state = usePolledRead<ValidatorSet>(
    conn ? 'validators' : null,
    async () => {
      const [list, bond] = await Promise.all([
        conn!.moduleCall<unknown>({ module: 'validators', action: 'list', params: {} }).catch((e: unknown) => {
          if (isEmptyResult(e)) return [];
          throw e;
        }),
        conn!
          .moduleCall<{ spld?: string }>({ module: 'validators', action: 'bond', params: {} })
          .catch(() => null),
      ]);
      const validators = (Array.isArray(list) ? list : []).map(parseValidator).filter((v): v is ValidatorInfo => !!v);
      const bondSpld = bond && typeof bond.spld === 'string' && /^\d+$/.test(bond.spld) ? bond.spld : null;
      return { validators, bondSpld };
    },
    opts.pollMs ?? 30_000,
  );
  const isApproved = useCallback(
    async (address: string) => {
      if (!conn) return false;
      const res = await conn.moduleCall<{ approved?: boolean }>({
        module: 'validators',
        action: 'approved',
        params: { address },
      });
      return res?.approved === true;
    },
    [conn],
  );
  return { ...state, validators: state.data?.validators ?? [], isApproved };
}

export type ActivityOptions = {
  /** Blocks to look back from the head. Capped at 2000 on the node's `/logs`. Default 2000. */
  blocks?: number;
  /** Rows to keep, newest first. Default 25. */
  limit?: number;
  /** Poll interval (ms). Default 15000; 0 disables polling. */
  pollMs?: number;
  /**
   * Indexer base URL (https, or http on localhost) serving `/idx/v1/logs` with the node's `/logs`
   * query and response shape. Lifts the 2000-block window; the node is used when it fails.
   */
  indexerUrl?: string;
};

export type ActivityResult = {
  items: ActivityItem[];
  fromBlock: number;
  toBlock: number;
  /** True when a query hit the server's result cap, so older rows may be missing. */
  truncated: boolean;
  source: 'node' | 'indexer';
};

type LogsPage = { logs?: unknown[]; truncated?: boolean };

async function fetchIndexer(base: string, filter: LogFilter): Promise<LogsPage> {
  const params = new URLSearchParams();
  if (filter.from_block != null) params.set('from', String(filter.from_block));
  if (filter.to_block != null) params.set('to', String(filter.to_block));
  if (filter.topics?.length) params.set('topics', filter.topics.join(','));
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 15_000);
  try {
    const res = await fetch(`${base}/idx/v1/logs?${params}`, { signal: ctrl.signal, credentials: 'omit' });
    if (!res.ok) throw new Error(`Indexer responded ${res.status}`);
    return (await res.json()) as LogsPage;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Token transfers in and out of `address` (default: the connected account) from event logs —
 * QRC-20 `Transfer` and QRC-1155 `TransferSingle` / `TransferBatch` — plus this browser's pending
 * sends. Native SPLD transfers and QRC-721 moves emit no logs, so they only show while pending.
 */
export function useActivity(
  address?: string | null,
  opts: ActivityOptions = {},
): ReadState<ActivityResult> & { items: ActivityItem[]; pending: PendingTx[] } {
  const conn = useReadConnection();
  const account = useAccount();
  const who = (address ?? account.address)?.trim() || null;
  const me = who ? toSqAddress(who) : null;
  const indexer = safeIndexerUrl(opts.indexerUrl);
  const limit = opts.limit ?? 25;
  const blocks = Math.max(1, Math.floor(opts.blocks ?? MAX_LOG_BLOCK_RANGE));
  const key = conn && me ? `${me}|${blocks}|${limit}|${indexer ?? ''}` : null;
  const { pending } = usePendingTransactions({ toast: false });

  const state = usePolledRead<ActivityResult>(
    key,
    async () => {
      const status = await conn!.getStatus();
      const head = typeof status.height === 'number' ? status.height : Number(status.height) || 0;
      const filters = activityFilters(me!);
      if (indexer) {
        try {
          const from = opts.blocks != null ? Math.max(0, head - blocks) : 0;
          const pages = await Promise.all(
            filters.map((f) => fetchIndexer(indexer, { ...f, from_block: from, to_block: head })),
          );
          return {
            items: mergeActivity(pages.flatMap((p) => p.logs ?? []), me!, limit),
            fromBlock: from,
            toBlock: head,
            truncated: pages.some((p) => p.truncated === true),
            source: 'indexer' as const,
          };
        } catch {
          /* fall back to the node's bounded window */
        }
      }
      const from = Math.max(0, head - Math.min(blocks, MAX_LOG_BLOCK_RANGE));
      const pages = await Promise.all(filters.map((f) => conn!.getLogs({ ...f, from_block: from, to_block: head })));
      return {
        items: mergeActivity(pages.flatMap((p) => (Array.isArray(p?.logs) ? p.logs : [])), me!, limit),
        fromBlock: from,
        toBlock: head,
        truncated: pages.some((p) => p?.truncated === true),
        source: 'node' as const,
      };
    },
    opts.pollMs ?? 15_000,
  );
  const items = useMemo(() => state.data?.items ?? [], [state.data]);
  return { ...state, items, pending };
}
