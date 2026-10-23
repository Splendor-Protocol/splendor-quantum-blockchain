/**
 * Read-only hooks that work without a connected wallet (they use the page's RPC
 * connection), plus a fee estimate for review screens.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { EstimateGasRequest, EstimateGasResponse } from '@splendor/sqc-sdk';
import { toUnits } from '@splendor/sqc-sdk';
import { useConnectState } from '../context.js';
import { useVisiblePolling } from '../usePageVisible.js';
import { useReadConnection } from './core.js';
import { loadTokens, validDecimals } from './qrc20.js';

export type ReadState<T> = {
  data: T | null;
  loading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
};

/**
 * Generic polled read keyed by `key`. Results for an older key or network are dropped,
 * and `data` resets when the key changes so stale values are never shown.
 */
function usePolledRead<T>(
  key: string | null,
  fetcher: () => Promise<T>,
  pollMs: number,
): ReadState<T> {
  const networkId = useConnectState((s) => s.network?.id ?? null);
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const generation = useRef(0);
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    generation.current += 1;
    setData(null);
    setError(null);
  }, [key, networkId]);

  const refresh = useCallback(async () => {
    const gen = ++generation.current;
    if (!key) {
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const value = await fetcherRef.current();
      if (gen !== generation.current) return;
      setData(value);
      setError(null);
    } catch (e) {
      if (gen !== generation.current) return;
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (gen === generation.current) setLoading(false);
    }
  }, [key]);

  useEffect(() => {
    void refresh();
  }, [refresh, networkId]);
  useVisiblePolling(() => void refresh(), pollMs, !!key);

  return { data, loading, error, refresh };
}

export type TokenInfo = {
  name?: string;
  symbol?: string;
  /** Validated 0–36, or null when the token reports something else. */
  decimals: number | null;
  totalSupply?: string;
  [k: string]: unknown;
};

/** QRC-20 metadata, readable before the user connects. */
export function useTokenInfo(tokenAddress: string | null, opts: { pollMs?: number } = {}): ReadState<TokenInfo> {
  const conn = useReadConnection();
  const key = tokenAddress && conn ? tokenAddress : null;
  return usePolledRead<TokenInfo>(
    key,
    async () => {
      const { QRC20 } = await loadTokens();
      const info = (await new QRC20(conn!, tokenAddress!).tokenInfo()) as Record<string, unknown>;
      return { ...info, decimals: validDecimals(info.decimals) } as TokenInfo;
    },
    opts.pollMs ?? 0,
  );
}

/**
 * QRC-20 balance of any owner (raw units), readable without a wallet. `formatted` is the
 * human value once the token's decimals are known.
 */
export function useTokenBalance(
  tokenAddress: string | null,
  owner: string | null,
  opts: { pollMs?: number } = {},
): ReadState<string> & { formatted: string | null; decimals: number | null } {
  const conn = useReadConnection();
  const info = useTokenInfo(tokenAddress);
  const key = tokenAddress && owner && conn ? `${tokenAddress}|${owner}` : null;
  const state = usePolledRead<string>(
    key,
    async () => {
      const { QRC20 } = await loadTokens();
      return String(await new QRC20(conn!, tokenAddress!).balanceOf(owner!));
    },
    opts.pollMs ?? 12_000,
  );
  const decimals = info.data?.decimals ?? null;
  const formatted = useMemo(() => {
    if (state.data == null || decimals == null) return null;
    try {
      return toUnits(state.data, decimals);
    } catch {
      return null;
    }
  }, [state.data, decimals]);
  return { ...state, formatted, decimals };
}

export type ReadContractResult = {
  return_data?: string;
  gas_used?: number;
  reverted?: boolean;
  revert_reason?: string;
};

/** Read-only contract call (no signature, no wallet). Re-runs when the inputs change. */
export function useReadContract(
  contractAddress: string | null,
  functionSig: string | null,
  args: unknown[] = [],
  opts: { pollMs?: number; enabled?: boolean } = {},
): ReadState<ReadContractResult> {
  const conn = useReadConnection();
  const argsKey = JSON.stringify(args, (_k, v) => (typeof v === 'bigint' ? v.toString() : v));
  const enabled = opts.enabled !== false;
  const key = enabled && contractAddress && functionSig && conn ? `${contractAddress}|${functionSig}|${argsKey}` : null;
  return usePolledRead<ReadContractResult>(
    key,
    async () => {
      const { Contract } = await import('@splendor/sqc-sdk/contracts');
      return (await new Contract(conn!, contractAddress!).read(functionSig!, args as never[])) as ReadContractResult;
    },
    opts.pollMs ?? 0,
  );
}

export type FeeEstimate = {
  /** Raw fee (atto SPLD) as reported by the node. */
  fee: string | null;
  /** Human SPLD value of `fee`. */
  feeSpld: string | null;
  /** True when the node could only give the minimum fee (the real fee may be higher). */
  feeIsFloor: boolean;
  gas: number | null;
  reverted: boolean;
  revertReason: string | null;
  loading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
};

const ESTIMATE_TIMEOUT_MS = 15_000;

function requestKey(req: EstimateGasRequest | null): string | null {
  if (!req) return null;
  const data =
    req.data instanceof Uint8Array
      ? Array.from(req.data, (b) => b.toString(16).padStart(2, '0')).join('')
      : (req.data ?? '');
  return JSON.stringify([req.type ?? null, req.from ?? '', req.to ?? '', data, req.encoding ?? '', req.value ?? '']);
}

/**
 * Fee estimate from the node's `/estimate` endpoint. Pass null to skip. A predicted
 * revert sets `reverted` so review screens can block Confirm.
 */
export function useEstimateFee(request: EstimateGasRequest | null, opts: { debounceMs?: number } = {}): FeeEstimate {
  const conn = useReadConnection();
  const key = conn ? requestKey(request) : null;
  const requestRef = useRef(request);
  requestRef.current = request;
  const [debouncedKey, setDebouncedKey] = useState<string | null>(key);
  const debounceMs = opts.debounceMs ?? 300;

  useEffect(() => {
    if (!debounceMs) {
      setDebouncedKey(key);
      return;
    }
    const t = setTimeout(() => setDebouncedKey(key), debounceMs);
    return () => clearTimeout(t);
  }, [key, debounceMs]);

  const state = usePolledRead<EstimateGasResponse>(
    debouncedKey,
    async () => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        const res = await Promise.race([
          conn!.estimateGas(requestRef.current!),
          new Promise<never>((_, reject) => {
            timer = setTimeout(() => reject(new Error('Fee estimate timed out')), ESTIMATE_TIMEOUT_MS);
          }),
        ]);
        // An empty body would otherwise leave the estimate "loading" (and Confirm disabled) forever.
        if (!res || typeof res !== 'object') throw new Error('Fee estimate returned no data');
        return res;
      } finally {
        clearTimeout(timer);
      }
    },
    0,
  );

  const res = debouncedKey === key ? state.data : null;
  const feeSpld = useMemo(() => {
    if (!res?.fee) return null;
    try {
      return toUnits(res.fee, 18);
    } catch {
      return null;
    }
  }, [res?.fee]);

  return {
    fee: res?.fee ?? null,
    feeSpld,
    feeIsFloor: res?.fee_is_floor === true,
    gas: typeof res?.gas === 'number' ? res.gas : null,
    reverted: res?.reverted === true,
    revertReason: res?.reverted ? (res.revert_reason ?? null) : null,
    loading: key != null && (state.loading || debouncedKey !== key || (state.data == null && state.error == null)),
    error: debouncedKey === key ? state.error : null,
    refresh: state.refresh,
  };
}
