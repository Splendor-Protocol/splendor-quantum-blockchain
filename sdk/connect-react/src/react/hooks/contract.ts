import { useCallback } from 'react';
import { useConnectState } from '../context.js';
import { useDeployAction, useTxAction, type TxHookStatus } from '../useTxAction.js';
import { useReadConnection, useWallet } from './core.js';

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
  const readConnection = useReadConnection();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);

  const read = useCallback(
    async (functionSig: string, args: unknown[] = []) => {
      if (!contractAddress || !readConnection) throw new Error('Contract not ready');
      const { Contract } = await import('@splendor/sqc-sdk/contracts');
      return new Contract(readConnection, contractAddress).read(
        functionSig,
        args as never[],
      );
    },
    [contractAddress, readConnection],
  );

  const readUint256 = useCallback(
    async (functionSig: string, args: unknown[] = []) => {
      if (!contractAddress || !readConnection) throw new Error('Contract not ready');
      const { Contract } = await import('@splendor/sqc-sdk/contracts');
      return new Contract(readConnection, contractAddress).readUint256(
        functionSig,
        args as never[],
      );
    },
    [contractAddress, readConnection],
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
      }, { kind: 'call', summary: `Call ${functionSig} on ${contractAddress}` });
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
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const action = useDeployAction(
    network,
    async (p: { bytecode: Uint8Array | string; opts?: { fee?: string; waitForConfirmation?: boolean } }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      const { deployBytecode } = await import('@splendor/sqc-sdk/contracts');
      return deployBytecode(wallet, p.bytecode, p.opts);
    },
    (_p, res) => ({ kind: 'deploy', summary: `Deploy contract ${res.address}` }),
  );
  const { deploy: run } = action;
  const deploy = useCallback(
    async (bytecode: Uint8Array | string, opts?: { fee?: string; waitForConfirmation?: boolean }) => {
      const { address, result } = await run({ bytecode, opts });
      return { address, hash: result.hash };
    },
    [run],
  );
  return { ...action, deploy };
}


