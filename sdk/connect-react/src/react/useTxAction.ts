/**
 * Shared mutation state for connect-react tx hooks.
 */
import { useCallback, useState } from 'react';
import { txExplorerUrl, type NetworkConfig } from '@splendor/sqc-sdk';

export type TxHookStatus = 'idle' | 'pending' | 'success' | 'error';

export function useTxAction(network?: NetworkConfig | null): {
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
  run: <T extends { hash?: string }>(fn: () => Promise<T>) => Promise<T>;
} {
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [explorerUrl, setExplorerUrl] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setExplorerUrl(null);
    setError(null);
  }, []);

  const run = useCallback(
    async <T extends { hash?: string }>(fn: () => Promise<T>): Promise<T> => {
      setStatus('pending');
      setError(null);
      setHash(null);
      setExplorerUrl(null);
      try {
        const res = await fn();
        const h = res.hash ?? null;
        setHash(h);
        setExplorerUrl(network && h ? txExplorerUrl(network, h) : null);
        setStatus('success');
        return res;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [network],
  );

  return { status, hash, explorerUrl, error, reset, run };
}
