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
export function useExchange(exchangeAddress: string | null): {
  info: () => Promise<Record<string, unknown>>;
  signOrder: (params: {
    condition: number;
    outcome: number;
    side: number;
    share_amount: string;
    price: string;
    expiration: number;
    nonce: number;
    salt?: string;
    recipient?: string;
  }) => Promise<object>;
  cancelOrder: (params: {
    condition: number;
    outcome: number;
    side: number;
    share_amount: string;
    price: string;
    expiration: number;
    nonce: number;
    salt: string;
    recipient?: string;
  }) => Promise<{ hash?: string }>;
  createCondition: (params: {
    collateral: string;
    resolver: string;
    endTime: number;
    outcomeCount?: number;
    questionHash: string;
  }) => Promise<{ hash?: string }>;
  split: (condition: number, amount: string) => Promise<{ hash?: string }>;
  merge: (condition: number, amount: string) => Promise<{ hash?: string }>;
  match: (params: {
    takerOrder: Record<string, unknown>;
    makerOrders: Array<Record<string, unknown>>;
    fillShares: string[];
  }) => Promise<{ hash?: string }>;
  resolve: (condition: number, outcome: number) => Promise<{ hash?: string }>;
  redeem: (condition: number) => Promise<{ hash?: string }>;
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const tx = useTxAction(network);

  const info = useCallback(async () => {
    if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
    const { Exchange } = await import('@splendor/sqc-sdk/defi');
    return new Exchange(wallet.connection, exchangeAddress).info();
  }, [exchangeAddress, ready, wallet]);

  const signOrder = useCallback(
    async (params: {
      condition: number;
      outcome: number;
      side: number;
      share_amount: string;
      price: string;
      expiration: number;
      nonce: number;
      salt?: string;
      recipient?: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const { Exchange } = await import('@splendor/sqc-sdk/defi');
      return new Exchange(wallet.connection, exchangeAddress).signOrder(wallet, params);
    },
    [exchangeAddress, ready, wallet],
  );

  const cancelOrder = useCallback(
    async (params: {
      condition: number;
      outcome: number;
      side: number;
      share_amount: string;
      price: string;
      expiration: number;
      nonce: number;
      salt: string;
      recipient?: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        const ex = new Exchange(wallet.connection, exchangeAddress);
        const signed = await ex.signCancel(wallet, params);
        return ex.cancelOrder(wallet, signed);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const createCondition = useCallback(
    async (params: {
      collateral: string;
      resolver: string;
      endTime: number;
      outcomeCount?: number;
      questionHash: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).createCondition(wallet, params);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const split = useCallback(
    async (condition: number, amount: string) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).split(wallet, condition, amount);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const merge = useCallback(
    async (condition: number, amount: string) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).merge(wallet, condition, amount);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const matchOrders = useCallback(
    async (params: {
      takerOrder: Record<string, unknown>;
      makerOrders: Array<Record<string, unknown>>;
      fillShares: string[];
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).match(wallet, params);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const resolve = useCallback(
    async (condition: number, outcome: number) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).resolve(wallet, condition, outcome);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const redeem = useCallback(
    async (condition: number) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).redeem(wallet, condition);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  return {
    info,
    signOrder,
    cancelOrder,
    createCondition,
    split,
    merge,
    match: matchOrders,
    resolve,
    redeem,
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployExchange(): {
  deploy: (params: {
    name: string;
    uriPrefix?: string;
    redeemFeeBps?: number;
    feeRecipient?: string;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ address: string; hash?: string; height: number }>;
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
    async (params: {
      name: string;
      uriPrefix?: string;
      redeemFeeBps?: number;
      feeRecipient?: string;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployExchange } = await import('@splendor/sqc-sdk/defi');
        const { address: addr, height, result } = await deployExchange(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
        setAddress(addr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: addr, hash: result.hash, height };
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

