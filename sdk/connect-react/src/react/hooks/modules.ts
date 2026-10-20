import { useCallback, useMemo, useState } from 'react';
import { bindModules, listModules } from '@splendor/sqc-sdk';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import { connectErrorMessage } from '../../core/controller.js';
import { useConnectState } from '../context.js';
import { useRecordPendingTx, useSingleFlight, type TxHookStatus } from '../useTxAction.js';
import { useWallet } from './core.js';

/**
 * Bound module clients for the connected wallet.
 * `registered` lists module ids from the SDK registry (built-ins + addons).
 */
export function useModules() {
  const { wallet, ready } = useWallet();
  return useMemo(() => {
    if (!ready || !wallet) return null;
    const bound = bindModules(wallet.connection, wallet);
    return {
      ...bound,
      registered: listModules().map((m) => m.id),
    };
  }, [wallet, ready]);
}

function moduleCallProblem(opts: { module: unknown; action: unknown; params: unknown }): string | null {
  if (typeof opts.module !== 'string' || !opts.module.trim()) return 'Module call needs a module, e.g. "qrc20"';
  if (typeof opts.action !== 'string' || !opts.action.trim()) return 'Module call needs an action, e.g. "transfer"';
  const p = opts.params;
  if (!p || typeof p !== 'object' || Array.isArray(p)) {
    return 'Module call params must be a JSON object, e.g. { "token": "SQ:...", "to": "SQ:...", "amount": "1" }';
  }
  return null;
}

/**
 * Raw module router call (`{ module, action, params }`) for routed modules without a typed
 * client, such as verify, validators and qrc721.
 */
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
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setError(null);
  }, []);

  const call = useCallback(
    (opts: {
      module: string;
      action: string;
      params: Record<string, unknown>;
      fee?: string;
      waitForConfirmation?: boolean;
    }) =>
      singleFlight(async () => {
        if (!ready || !wallet) throw new Error('Wallet not connected');
        const invalid = moduleCallProblem(opts);
        if (invalid) {
          const err = new Error(invalid);
          setHash(null);
          setError(err);
          setStatus('error');
          throw err;
        }
        setStatus('pending');
        setError(null);
        setHash(null);
        const module = opts.module.trim();
        const action = opts.action.trim();
        try {
          const res = await wallet.sendModuleCall({ ...opts, module, action });
          setHash(res.hash ?? null);
          setStatus('success');
          recordPending(res, { kind: 'module', summary: `${module}.${action}` });
          return { hash: res.hash, status: String(res.status) };
        } catch (e) {
          const err = e instanceof Error ? e : new Error(String(e));
          setError(err);
          setStatus('error');
          throw err;
        }
      }),
    [wallet, ready, singleFlight, recordPending],
  );

  const explorerUrl = network && hash ? txExplorerUrl(network, hash) : null;
  return { call, status, hash, explorerUrl, error, errorMessage: error ? connectErrorMessage(error) : null, reset };
}
