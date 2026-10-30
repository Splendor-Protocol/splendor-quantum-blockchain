import { useCallback, useEffect, useRef, useState } from 'react';
import { useConnectState } from '../context.js';
import { useVisiblePolling } from '../usePageVisible.js';

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
export function usePolledRead<T>(
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
