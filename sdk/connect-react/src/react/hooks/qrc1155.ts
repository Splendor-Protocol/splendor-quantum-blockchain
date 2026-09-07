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
export function useQRC1155(collectionAddress: string | null): {
  balanceOf: (owner: string, id: string) => Promise<string>;
  collectionInfo: () => Promise<Record<string, unknown>>;
  mint: (to: string, id: string, amount: string, uri?: string) => Promise<{ hash?: string }>;
  transfer: (to: string, id: string, amount: string) => Promise<{ hash?: string }>;
  setApprovalForAll: (operator: string, approved: boolean) => Promise<{ hash?: string }>;
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
  refresh: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const tx = useTxAction(network);
  const [, setTick] = useState(0);
  const refresh = useCallback(() => setTick((t) => t + 1), []);

  const balanceOf = useCallback(
    async (owner: string, id: string) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Collection not ready');
      const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC1155(wallet.connection, collectionAddress).balanceOf(owner, id);
    },
    [collectionAddress, ready, wallet],
  );

  const collectionInfo = useCallback(async () => {
    if (!collectionAddress || !ready || !wallet) throw new Error('Collection not ready');
    const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
    return new QRC1155(wallet.connection, collectionAddress).collectionInfo();
  }, [collectionAddress, ready, wallet]);

  const mint = useCallback(
    async (to: string, id: string, amount: string, uri?: string) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC1155(wallet.connection, collectionAddress).mint(wallet, to, id, amount, uri);
      });
      refresh();
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx, refresh],
  );

  const transfer = useCallback(
    async (to: string, id: string, amount: string) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC1155(wallet.connection, collectionAddress).transfer(wallet, to, id, amount);
      });
      refresh();
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx, refresh],
  );

  const setApprovalForAll = useCallback(
    async (operator: string, approved: boolean) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC1155(wallet.connection, collectionAddress).setApprovalForAll(
          wallet,
          operator,
          approved,
        );
      });
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx],
  );

  return {
    balanceOf,
    collectionInfo,
    mint,
    transfer,
    setApprovalForAll,
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
    refresh,
  };
}

export function useDeployCollection(): {
  deploy: (params: {
    name: string;
    uriPrefix?: string;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ address: string; hash?: string }>;
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
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployQRC1155 } = await import('@splendor/sqc-sdk/tokens');
        const { address: addr, result } = await deployQRC1155(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
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

