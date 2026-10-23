import { useCallback } from 'react';
import { useConnectState } from '../context.js';
import { notReady } from '../hookErrors.js';
import { useDeployAction, useTxAction, type TxHookStatus } from '../useTxAction.js';
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
  /** UI copy for `error`. */
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const tx = useTxAction(network);

  const info = useCallback(async () => {
    if (!bridgeAddress || !ready || !wallet) throw notReady('Bridge not ready', ready && wallet ? 'bridge' : null);
    const { BridgeModule } = await import('@splendor/sqc-sdk/bridge');
    return new BridgeModule(wallet.connection, bridgeAddress).info();
  }, [bridgeAddress, ready, wallet]);

  const claimed = useCallback(
    async (id: number) => {
      if (!bridgeAddress || !ready || !wallet) throw notReady('Bridge not ready', ready && wallet ? 'bridge' : null);
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
      if (!bridgeAddress || !ready || !wallet) throw notReady('Bridge not ready', ready && wallet ? 'bridge' : null);
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
    errorMessage: tx.errorMessage,
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
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
  const action = useDeployAction(
    network,
    async (params: DeployBridgeParams) => {
      if (!ready || !wallet) throw notReady('Wallet not connected');
      const { deployBridge } = await import('@splendor/sqc-sdk/bridge');
      return deployBridge(wallet, params, { fee: params.fee, waitForConfirmation: params.waitForConfirmation });
    },
    (params, res) => ({ kind: 'deploy', summary: `Deploy bridge ${params.splvSymbol} (${res.address})` }),
  );
  const { deploy: run } = action;
  const deploy = useCallback(
    async (params: DeployBridgeParams) => {
      const { address, splvAddress, result } = await run(params);
      return { address, splvAddress, hash: result.hash };
    },
    [run],
  );
  return { ...action, deploy };
}

type DeployBridgeParams = {
  evmChainId: number;
  evmContract: string;
  splvName: string;
  splvSymbol: string;
  splvMaxSupply: string;
  maxClaimAmount: string;
  threshold: number;
  fee?: string;
  waitForConfirmation?: boolean;
};

