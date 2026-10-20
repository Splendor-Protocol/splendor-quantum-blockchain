import { useCallback, useState } from 'react';
import { useConnectState } from '../context.js';
import { useDeployAction, useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useReadConnection, useWallet } from './core.js';
import { loadTokens } from './qrc20.js';

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
  const readConnection = useReadConnection();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);
  const [, setTick] = useState(0);
  const refresh = useCallback(() => setTick((t) => t + 1), []);

  const balanceOf = useCallback(
    async (owner: string, id: string) => {
      if (!collectionAddress || !readConnection) throw new Error('Collection not ready');
      const { QRC1155 } = await loadTokens();
      return new QRC1155(readConnection, collectionAddress).balanceOf(owner, id);
    },
    [collectionAddress, readConnection],
  );

  const collectionInfo = useCallback(async () => {
    if (!collectionAddress || !readConnection) throw new Error('Collection not ready');
    const { QRC1155 } = await loadTokens();
    return new QRC1155(readConnection, collectionAddress).collectionInfo();
  }, [collectionAddress, readConnection]);

  const mint = useCallback(
    async (to: string, id: string, amount: string, uri?: string) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await loadTokens();
        return new QRC1155(wallet.connection, collectionAddress).mint(wallet, to, id, amount, uri);
      }, { kind: 'nft', summary: `Mint ${amount} of #${id} to ${to}` });
      refresh();
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx, refresh],
  );

  const transfer = useCallback(
    async (to: string, id: string, amount: string) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await loadTokens();
        return new QRC1155(wallet.connection, collectionAddress).transfer(wallet, to, id, amount);
      }, { kind: 'nft', summary: `Send ${amount} of #${id} to ${to}` });
      refresh();
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx, refresh],
  );

  const setApprovalForAll = useCallback(
    async (operator: string, approved: boolean) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC1155 } = await loadTokens();
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
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const action = useDeployAction(
    network,
    async (params: { name: string; uriPrefix?: string; fee?: string; waitForConfirmation?: boolean }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      const { deployQRC1155 } = await loadTokens();
      return deployQRC1155(wallet, params, { fee: params.fee, waitForConfirmation: params.waitForConfirmation });
    },
    (params, res) => ({ kind: 'deploy', summary: `Deploy collection ${params.name} (${res.address})` }),
  );
  const { deploy: run } = action;
  const deploy = useCallback(
    async (params: { name: string; uriPrefix?: string; fee?: string; waitForConfirmation?: boolean }) => {
      const { address, result } = await run(params);
      return { address, hash: result.hash };
    },
    [run],
  );
  return { ...action, deploy };
}

