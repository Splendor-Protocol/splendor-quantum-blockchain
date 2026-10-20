import { useCallback } from 'react';
import type { QRC721CollectionInfo } from '@splendor/sqc-sdk/tokens';
import { useConnectState } from '../context.js';
import { useDeployAction, useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useReadConnection, useWallet } from './core.js';
import { loadTokens } from './qrc20.js';

export type { TxHookStatus } from '../useTxAction.js';

/** QRC-721 collection: reads work without a wallet, writes need a connected one. */
export function useQRC721(collectionAddress: string | null): {
  ownerOf: (id: string) => Promise<string>;
  balanceOf: (owner: string) => Promise<string>;
  collectionInfo: () => Promise<QRC721CollectionInfo>;
  /** Collection owner only; `id` must be unminted. */
  mint: (to: string, id: string, uri?: string) => Promise<{ hash?: string }>;
  transfer: (to: string, id: string) => Promise<{ hash?: string }>;
  burn: (id: string) => Promise<{ hash?: string }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const readConnection = useReadConnection();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);

  const reader = useCallback(async () => {
    if (!collectionAddress || !readConnection) throw new Error('Collection not ready');
    const { QRC721 } = await loadTokens();
    return new QRC721(readConnection, collectionAddress);
  }, [collectionAddress, readConnection]);

  const ownerOf = useCallback(async (id: string) => (await reader()).ownerOf(id), [reader]);
  const balanceOf = useCallback(async (owner: string) => (await reader()).balanceOf(owner), [reader]);
  const collectionInfo = useCallback(async () => (await reader()).collectionInfo(), [reader]);

  const write = useCallback(
    async (
      fn: (c: import('@splendor/sqc-sdk/tokens').QRC721, w: NonNullable<typeof wallet>) => Promise<{ hash?: string }>,
      summary: string,
    ) => {
      if (!collectionAddress || !ready || !wallet) throw new Error('Wallet or collection not ready');
      const res = await tx.run(async () => {
        const { QRC721 } = await loadTokens();
        return fn(new QRC721(wallet.connection, collectionAddress), wallet);
      }, { kind: 'nft', summary });
      return { hash: res.hash };
    },
    [collectionAddress, ready, wallet, tx],
  );

  const mint = useCallback(
    (to: string, id: string, uri?: string) => write((c, w) => c.mint(w, to, id, uri), `Mint NFT #${id} to ${to}`),
    [write],
  );
  const transfer = useCallback(
    (to: string, id: string) => write((c, w) => c.transfer(w, to, id), `Send NFT #${id} to ${to}`),
    [write],
  );
  const burn = useCallback((id: string) => write((c, w) => c.burn(w, id), `Burn NFT #${id}`), [write]);

  return {
    ownerOf,
    balanceOf,
    collectionInfo,
    mint,
    transfer,
    burn,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    errorMessage: tx.errorMessage,
    reset: tx.reset,
  };
}

/** Deploy a QRC-721 collection; the connected wallet becomes its owner. */
export function useDeployQRC721(): {
  deploy: (params: {
    name: string;
    symbol: string;
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
    async (params: { name: string; symbol: string; fee?: string; waitForConfirmation?: boolean }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      const { deployQRC721 } = await loadTokens();
      return deployQRC721(wallet, { name: params.name, symbol: params.symbol }, {
        fee: params.fee,
        waitForConfirmation: params.waitForConfirmation,
      });
    },
    (params, res) => ({ kind: 'deploy', summary: `Deploy NFT collection ${params.symbol} (${res.address})` }),
  );
  const { deploy: run } = action;
  const deploy = useCallback(
    async (params: { name: string; symbol: string; fee?: string; waitForConfirmation?: boolean }) => {
      const { address, result } = await run(params);
      return { address, hash: result.hash };
    },
    [run],
  );
  return { ...action, deploy };
}
