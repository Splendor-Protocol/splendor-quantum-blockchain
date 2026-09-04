import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  CallOpts,
  DeployOpts,
  DeriveInfo,
  NetworkConfig,
  NetworkId,
  PreflightResult,
  SplendorPQBridge,
  SqcProvider,
  TransferOpts,
} from '@splendor/sqc-sdk';
import { bindModules, listSelectableNetworks, listModules, txExplorerUrl } from '@splendor/sqc-sdk';
import type { Connector } from '../../connectors/types.js';
import { connectErrorMessage } from '../../core/controller.js';
import type { ConnectAccount, ConnectingStep, ConnectionStatus } from '../../core/types.js';
import {
  useBrandLabels,
  useConnectController,
  useConnectState,
  useNetworkSwitch,
} from '../context.js';
import { useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useWallet } from './core.js';

export type { TxHookStatus } from '../useTxAction.js';
export function useDex(poolAddress: string | null): {
  reserves: () => Promise<Record<string, unknown>>;
  swap: (params: {
    tokenIn: 0 | 1;
    amountIn: string;
    minAmountOut?: string;
  }) => Promise<{ hash?: string }>;
  addLiquidity: (params: {
    amount0Desired: string;
    amount1Desired: string;
    amount0Min?: string;
    amount1Min?: string;
  }) => Promise<{ hash?: string }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const tx = useTxAction(network);

  const reserves = useCallback(async () => {
    if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
    const { DEX } = await import('@splendor/sqc-sdk/defi');
    return new DEX(wallet.connection, poolAddress).reserves();
  }, [poolAddress, ready, wallet]);

  const swap = useCallback(
    async (params: { tokenIn: 0 | 1; amountIn: string; minAmountOut?: string }) => {
      if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
      const res = await tx.run(async () => {
        const { DEX } = await import('@splendor/sqc-sdk/defi');
        return new DEX(wallet.connection, poolAddress).swap(wallet, params);
      });
      return { hash: res.hash };
    },
    [poolAddress, ready, wallet, tx],
  );

  const addLiquidity = useCallback(
    async (params: {
      amount0Desired: string;
      amount1Desired: string;
      amount0Min?: string;
      amount1Min?: string;
    }) => {
      if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
      const res = await tx.run(async () => {
        const { DEX } = await import('@splendor/sqc-sdk/defi');
        return new DEX(wallet.connection, poolAddress).addLiquidity(wallet, params);
      });
      return { hash: res.hash };
    },
    [poolAddress, ready, wallet, tx],
  );

  return {
    reserves,
    swap,
    addLiquidity,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployPool(): {
  deploy: (
    token0: string,
    token1: string,
    opts?: { fee?: string; waitForConfirmation?: boolean },
  ) => Promise<{ address: string; hash?: string }>;
  status: TxHookStatus;
  address: string | null;
  hash: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [address, setAddress] = useState<string | null>(null);
  const [hash, setHash] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setAddress(null);
    setHash(null);
    setError(null);
  }, []);

  const deploy = useCallback(
    async (
      token0: string,
      token1: string,
      opts?: { fee?: string; waitForConfirmation?: boolean },
    ) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployPool } = await import('@splendor/sqc-sdk/defi');
        const { address: addr, result } = await deployPool(wallet, token0, token1, opts);
        setAddress(addr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: addr, hash: result.hash };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { deploy, status, address, hash, error, reset };
}

