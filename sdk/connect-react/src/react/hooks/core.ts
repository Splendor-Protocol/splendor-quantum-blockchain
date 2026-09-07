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
import { bindModules, listSelectableNetworks, txExplorerUrl } from '@splendor/sqc-sdk';
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

export type { TxHookStatus } from '../useTxAction.js';
export function useConnect(): {
  connect: (connectorId: string, opts?: Record<string, unknown>) => Promise<DeriveInfo>;
  disconnect: () => Promise<void>;
  connectors: Connector[];
  status: ConnectionStatus;
  connectingStep: ConnectingStep;
  error: Error | null;
  errorMessage: string | null;
  clearError: () => void;
  openModal: (view?: 'connect' | 'account') => void;
  closeModal: () => void;
  modal: 'closed' | 'connect' | 'account';
  activeConnectorId: string | null;
  labels: ReturnType<typeof useBrandLabels>;
} {
  const ctrl = useConnectController();
  const state = useConnectState();
  const labels = useBrandLabels();

  const connect = useCallback(
    (connectorId: string, opts?: Record<string, unknown>) => ctrl.connect(connectorId, opts),
    [ctrl],
  );
  const disconnect = useCallback(() => ctrl.disconnect(), [ctrl]);
  const clearError = useCallback(() => ctrl.clearError(), [ctrl]);
  const openModal = useCallback(
    (view?: 'connect' | 'account') => ctrl.openModal(view),
    [ctrl],
  );
  const closeModal = useCallback(() => ctrl.closeModal(), [ctrl]);

  return {
    connect,
    disconnect,
    connectors: state.connectors,
    status: state.status,
    connectingStep: state.connectingStep,
    error: state.error,
    errorMessage: state.error ? connectErrorMessage(state.error) : null,
    clearError,
    openModal,
    closeModal,
    modal: state.modal,
    activeConnectorId: state.activeConnectorId,
    labels,
  };
}

export function useAccount(): {
  address: string | null;
  evmAddress: string | null;
  isConnected: boolean;
  info: DeriveInfo | null;
  account: ConnectAccount | null;
} {
  const state = useConnectState();
  return {
    address: state.account?.address ?? null,
    evmAddress: state.account?.evmAddress ?? null,
    isConnected: state.status === 'connected' && !!state.account,
    info: state.account?.info ?? null,
    account: state.account,
  };
}

export function useBalance(pollMs = 12_000): {
  balance: string | null;
  balanceSPLD: string | null;
  refresh: () => Promise<void>;
  loading: boolean;
  error: Error | null;
} {
  const ctrl = useConnectController();
  const { address, isConnected } = useAccount();
  const [balance, setBalance] = useState<string | null>(null);
  const [balanceSPLD, setBalanceSPLD] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    if (!address || !isConnected || ctrl.isStub) {
      setBalance(null);
      setBalanceSPLD(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await ctrl.getProvider().getBalance(address);
      setBalance(res.balance);
      setBalanceSPLD(res.balance_spld);
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setLoading(false);
    }
  }, [address, isConnected, ctrl]);

  useEffect(() => {
    void refresh();
    if (!isConnected || !pollMs) return;
    const id = setInterval(() => void refresh(), pollMs);
    return () => clearInterval(id);
  }, [refresh, isConnected, pollMs]);

  return { balance, balanceSPLD, refresh, loading, error };
}

export function useNetwork(): {
  network: NetworkConfig | null;
  chainId: string | null;
  networkId: NetworkId;
  switchNetwork: (id: NetworkId) => Promise<void>;
  networks: NetworkConfig[];
  switching: boolean;
} {
  const state = useConnectState();
  const { switchNetwork, allowPlaceholderNetworks, networkId, switching } = useNetworkSwitch();
  const networks = listSelectableNetworks({
    allowPlaceholder: allowPlaceholderNetworks,
  });
  return {
    network: state.network,
    chainId: state.network?.chainId ?? null,
    networkId,
    switchNetwork,
    networks,
    switching,
  };
}

export function useWallet(): {
  wallet: SplendorPQBridge | null;
  provider: SqcProvider | null;
  ready: boolean;
} {
  const ctrl = useConnectController();
  if (ctrl.isStub) {
    return { wallet: null, provider: null, ready: false };
  }
  return {
    wallet: ctrl.getWallet(),
    provider: ctrl.getProvider(),
    ready: true,
  };
}

export function usePreflight(): {
  preflight: (opts?: {
    fee?: string;
    amount?: string;
    amountSPLD?: string;
  }) => Promise<PreflightResult>;
  status: TxHookStatus;
  result: PreflightResult | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [result, setResult] = useState<PreflightResult | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setResult(null);
    setError(null);
  }, []);

  const preflight = useCallback(
    async (opts?: { fee?: string; amount?: string; amountSPLD?: string }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const res = await wallet.preflight(opts ?? {});
        setResult(res);
        setStatus('success');
        return res;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { preflight, status, result, error, reset };
}

export function useSendTransfer(): {
  sendTransfer: (
    opts: TransferOpts,
  ) => Promise<{ status: string; hash?: string; confirmed?: boolean; explorerUrl?: string | null }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  confirmed: boolean;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [explorerUrl, setExplorerUrl] = useState<string | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setExplorerUrl(null);
    setConfirmed(false);
    setError(null);
  }, []);

  const sendTransfer = useCallback(
    async (opts: TransferOpts) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      setHash(null);
      setExplorerUrl(null);
      setConfirmed(false);
      try {
        const res = await wallet.sendTransfer(opts);
        const h = res.hash ?? null;
        setHash(h);
        const exp = network && h ? txExplorerUrl(network, h) : null;
        setExplorerUrl(exp);
        const didConfirm = !!opts.waitForConfirmation && !!res.confirmation;
        setConfirmed(didConfirm);
        setStatus('success');
        return {
          status: String(res.status),
          hash: res.hash,
          confirmed: didConfirm,
          explorerUrl: exp,
        };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready, network],
  );

  return { sendTransfer, status, hash, explorerUrl, confirmed, error, reset };
}

export function useCallContract(): {
  callContract: (opts: CallOpts) => Promise<{ status: string; hash?: string; explorerUrl?: string | null }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [explorerUrl, setExplorerUrl] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setExplorerUrl(null);
    setError(null);
  }, []);

  const callContract = useCallback(
    async (opts: CallOpts) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      setHash(null);
      setExplorerUrl(null);
      try {
        const res = await wallet.callContract(opts);
        const h = res.hash ?? null;
        setHash(h);
        const exp = network && h ? txExplorerUrl(network, h) : null;
        setExplorerUrl(exp);
        setStatus('success');
        return { status: String(res.status), hash: res.hash, explorerUrl: exp };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready, network],
  );

  return { callContract, status, hash, explorerUrl, error, reset };
}

/** @deprecated Prefer `useDeployBytecode` — same underlying deployBytecode helper. */
export function useDeployContract(): {
  deployContract: (
    opts: DeployOpts,
  ) => Promise<{ status: string; hash?: string; address: string; explorerUrl?: string | null }>;
  status: TxHookStatus;
  hash: string | null;
  address: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [address, setAddress] = useState<string | null>(null);
  const [explorerUrl, setExplorerUrl] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setAddress(null);
    setExplorerUrl(null);
    setError(null);
  }, []);

  const deployContract = useCallback(
    async (opts: DeployOpts) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      setHash(null);
      setAddress(null);
      setExplorerUrl(null);
      try {
        const { deployBytecode } = await import('@splendor/sqc-sdk/contracts');
        const { address: predicted, result } = await deployBytecode(wallet, opts.bytecode, opts);
        const h = result.hash ?? null;
        setHash(h);
        setAddress(predicted);
        const exp = network && h ? txExplorerUrl(network, h) : null;
        setExplorerUrl(exp);
        setStatus('success');
        return { status: String(result.status), hash: result.hash, address: predicted, explorerUrl: exp };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready, network],
  );

  return { deployContract, status, hash, address, explorerUrl, error, reset };
}

export function useWaitForConfirmation(): {
  wait: (opts: {
    from: string;
    nonceBefore: number;
    timeoutMs?: number;
    pollMs?: number;
  }) => Promise<void>;
  status: TxHookStatus;
  error: Error | null;
  reset: () => void;
} {
  const { provider, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setError(null);
  }, []);

  const wait = useCallback(
    async (opts: {
      from: string;
      nonceBefore: number;
      timeoutMs?: number;
      pollMs?: number;
    }) => {
      if (!ready || !provider) throw new Error('Provider not ready');
      setStatus('pending');
      setError(null);
      try {
        await provider.waitForConfirmation(opts);
        setStatus('success');
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [provider, ready],
  );

  return { wait, status, error, reset };
}

