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
export function useContract(contractAddress: string | null): {
  read: (functionSig: string, args?: unknown[]) => Promise<{
    return_data?: string;
    gas_used?: number;
    reverted?: boolean;
    revert_reason?: string;
  }>;
  readUint256: (functionSig: string, args?: unknown[]) => Promise<bigint>;
  call: (
    functionSig: string,
    args?: unknown[],
    opts?: { fee?: string; value?: string; waitForConfirmation?: boolean },
  ) => Promise<{ hash?: string }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const tx = useTxAction(network);

  const read = useCallback(
    async (functionSig: string, args: unknown[] = []) => {
      if (!contractAddress || !ready || !wallet) throw new Error('Contract not ready');
      const { Contract } = await import('@splendor/sqc-sdk/contracts');
      return new Contract(wallet.connection, contractAddress).read(
        functionSig,
        args as never[],
      );
    },
    [contractAddress, ready, wallet],
  );

  const readUint256 = useCallback(
    async (functionSig: string, args: unknown[] = []) => {
      if (!contractAddress || !ready || !wallet) throw new Error('Contract not ready');
      const { Contract } = await import('@splendor/sqc-sdk/contracts');
      return new Contract(wallet.connection, contractAddress).readUint256(
        functionSig,
        args as never[],
      );
    },
    [contractAddress, ready, wallet],
  );

  const call = useCallback(
    async (
      functionSig: string,
      args: unknown[] = [],
      opts?: { fee?: string; value?: string; waitForConfirmation?: boolean },
    ) => {
      if (!contractAddress || !ready || !wallet) throw new Error('Contract not ready');
      const res = await tx.run(async () => {
        const { Contract } = await import('@splendor/sqc-sdk/contracts');
        return new Contract(wallet.connection, contractAddress).call(
          wallet,
          functionSig,
          args as never[],
          opts,
        );
      });
      return { hash: res.hash };
    },
    [contractAddress, ready, wallet, tx],
  );

  return {
    read,
    readUint256,
    call,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployBytecode(): {
  deploy: (
    bytecode: Uint8Array | string,
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
      bytecode: Uint8Array | string,
      opts?: { fee?: string; waitForConfirmation?: boolean },
    ) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployBytecode } = await import('@splendor/sqc-sdk/contracts');
        const { address: addr, result } = await deployBytecode(wallet, bytecode, opts);
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


