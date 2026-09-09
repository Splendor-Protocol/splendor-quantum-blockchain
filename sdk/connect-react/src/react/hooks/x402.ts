import { useCallback, useState } from 'react';
import { useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useWallet } from './core.js';

export type { TxHookStatus } from '../useTxAction.js';
export function usePayX402(): {
  pay: (opts: {
    to: string;
    amount?: string;
    amountSPLD?: string;
    requirements?: object;
    settle?: boolean;
    fee?: string;
  }) => Promise<{ payload: object; verify?: { valid: boolean; reason?: string }; settle?: object }>;
  status: TxHookStatus;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setError(null);
  }, []);

  const pay = useCallback(
    async (opts: {
      to: string;
      amount?: string;
      amountSPLD?: string;
      requirements?: object;
      settle?: boolean;
      fee?: string;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const result = await wallet.payX402({
          to: opts.to,
          amount: opts.amount ?? '0',
          amountSPLD: opts.amountSPLD,
          requirements: opts.requirements,
          settle: opts.settle,
          fee: opts.fee,
        });
        setStatus('success');
        return result;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { pay, status, error, reset };
}

export function useModuleCall(): {
  call: (opts: {
    module: string;
    action: string;
    params: Record<string, unknown>;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ hash?: string; status: string }>;
  status: TxHookStatus;
  hash: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setError(null);
  }, []);

  const call = useCallback(
    async (opts: {
      module: string;
      action: string;
      params: Record<string, unknown>;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const res = await wallet.sendModuleCall(opts);
        setHash(res.hash ?? null);
        setStatus('success');
        return { hash: res.hash, status: String(res.status) };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { call, status, hash, error, reset };
}

