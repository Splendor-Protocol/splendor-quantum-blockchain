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
export function useBridge(bridgeAddress: string | null): {
  info: () => Promise<Record<string, unknown>>;
  claimed: (id: number) => Promise<Record<string, unknown>>;
  claim: (params: {
    id: number;
    asset: 'spld' | 'splv';
    amount: string;
    recipient: string;
    evmTx: string;
    attestations: Array<{ pubkey: string; signature: string }>;
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

  const info = useCallback(async () => {
    if (!bridgeAddress || !ready || !wallet) throw new Error('Bridge not ready');
    const { BridgeModule } = await import('@splendor/sqc-sdk/bridge');
    return new BridgeModule(wallet.connection, bridgeAddress).info();
  }, [bridgeAddress, ready, wallet]);

  const claimed = useCallback(
    async (id: number) => {
      if (!bridgeAddress || !ready || !wallet) throw new Error('Bridge not ready');
      const { BridgeModule } = await import('@splendor/sqc-sdk/bridge');
      return new BridgeModule(wallet.connection, bridgeAddress).claimed(id);
    },
    [bridgeAddress, ready, wallet],
  );

  const claim = useCallback(
    async (params: {
      id: number;
      asset: 'spld' | 'splv';
      amount: string;
      recipient: string;
      evmTx: string;
      attestations: Array<{ pubkey: string; signature: string }>;
    }) => {
      if (!bridgeAddress || !ready || !wallet) throw new Error('Bridge not ready');
      const res = await tx.run(async () => {
        const { BridgeModule } = await import('@splendor/sqc-sdk/bridge');
        return new BridgeModule(wallet.connection, bridgeAddress).claim(wallet, params);
      });
      return { hash: res.hash };
    },
    [bridgeAddress, ready, wallet, tx],
  );

  return {
    info,
    claimed,
    claim,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployBridge(): {
  deploy: (params: {
    evmChainId: number;
    evmContract: string;
    splvName: string;
    splvSymbol: string;
    splvMaxSupply: string;
    maxClaimAmount: string;
    threshold: number;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ address: string; splvAddress: string; hash?: string }>;
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
      evmChainId: number;
      evmContract: string;
      splvName: string;
      splvSymbol: string;
      splvMaxSupply: string;
      maxClaimAmount: string;
      threshold: number;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployBridge } = await import('@splendor/sqc-sdk/bridge');
        const { address: addr, splvAddress, result } = await deployBridge(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
        setAddress(addr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: addr, splvAddress, hash: result.hash };
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

