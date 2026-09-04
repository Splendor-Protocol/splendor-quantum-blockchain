import { useMemo } from 'react';
import { bindModules, listModules } from '@splendor/sqc-sdk';
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
