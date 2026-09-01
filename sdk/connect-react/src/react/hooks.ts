import { useCallback, useEffect, useState } from 'react';
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
} from '@splendor/pq-bridge';
import { listSelectableNetworks, txExplorerUrl } from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';
import { connectErrorMessage } from '../core/controller.js';
import type { ConnectAccount, ConnectingStep, ConnectionStatus } from '../core/types.js';
import {
  useBrandLabels,
  useConnectController,
  useConnectState,
  useNetworkSwitch,
} from './context.js';

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

export type TxHookStatus = 'idle' | 'pending' | 'success' | 'error';

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
  const { network } = useNetwork();
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
  const { network } = useNetwork();
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

export function useDeployContract(): {
  deployContract: (
    opts: DeployOpts,
  ) => Promise<{ status: string; hash?: string; explorerUrl?: string | null }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useNetwork();
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

  const deployContract = useCallback(
    async (opts: DeployOpts) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      setHash(null);
      setExplorerUrl(null);
      try {
        const res = await wallet.deployContract(opts);
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

  return { deployContract, status, hash, explorerUrl, error, reset };
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

export function useQRC20(tokenAddress: string | null, pollMs = 12_000): {
  balance: string | null;
  tokenInfo: Record<string, unknown> | null;
  refresh: () => Promise<void>;
  transfer: (to: string, amount: string, opts?: TransferOpts) => Promise<{ hash?: string }>;
  approve: (spender: string, amount: string) => Promise<{ hash?: string }>;
  loading: boolean;
  txStatus: TxHookStatus;
  error: Error | null;
} {
  const { wallet, ready } = useWallet();
  const [balance, setBalance] = useState<string | null>(null);
  const [tokenInfo, setTokenInfo] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(false);
  const [txStatus, setTxStatus] = useState<TxHookStatus>('idle');
  const [error, setError] = useState<Error | null>(null);
  const { address } = useAccount();

  const refresh = useCallback(async () => {
    if (!tokenAddress || !ready || !wallet) {
      setBalance(null);
      setTokenInfo(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      const token = new QRC20(wallet.connection, tokenAddress);
      if (address) {
        const bal = await token.balanceOf(address);
        setBalance(bal);
      }
      const info = await token.tokenInfo();
      setTokenInfo(info as Record<string, unknown>);
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setLoading(false);
    }
  }, [tokenAddress, ready, wallet, address]);

  useEffect(() => {
    void refresh();
    if (!tokenAddress || !pollMs) return;
    const id = setInterval(() => void refresh(), pollMs);
    return () => clearInterval(id);
  }, [refresh, tokenAddress, pollMs]);

  const transfer = useCallback(
    async (to: string, amount: string, opts?: TransferOpts) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      setTxStatus('pending');
      try {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        const token = new QRC20(wallet.connection, tokenAddress);
        const res = await token.transfer(wallet, to, amount);
        setTxStatus('success');
        await refresh();
        return { hash: res.hash };
      } catch (e) {
        setTxStatus('error');
        throw e;
      }
    },
    [ready, wallet, tokenAddress, refresh],
  );

  const approve = useCallback(
    async (spender: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      setTxStatus('pending');
      try {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        const token = new QRC20(wallet.connection, tokenAddress);
        const res = await token.approve(wallet, spender, amount);
        setTxStatus('success');
        return { hash: res.hash };
      } catch (e) {
        setTxStatus('error');
        throw e;
      }
    },
    [ready, wallet, tokenAddress],
  );

  return { balance, tokenInfo, refresh, transfer, approve, loading, txStatus, error };
}

export function useDeployToken(): {
  deploy: (params: {
    name: string;
    symbol: string;
    decimals: number;
    totalSupply: string;
    maxSupply?: string;
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
      symbol: string;
      decimals: number;
      totalSupply: string;
      maxSupply?: string;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployQRC20 } = await import('@splendor/sqc-sdk/tokens');
        const { address: tokenAddr, result } = await deployQRC20(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
        setAddress(tokenAddr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: tokenAddr, hash: result.hash };
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

export function usePayX402(): {
  pay: (opts: {
    to: string;
    amount?: string;
    amountSPLD?: string;
    requirements?: object;
    settle?: boolean;
    fee?: string;
  }) => Promise<{ payload: object; verify?: { valid: boolean; reason?: string }; settle?: object }>;
  status: TxHookStatus;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setError(null);
  }, []);

  const pay = useCallback(
    async (opts: {
      to: string;
      amount?: string;
      amountSPLD?: string;
      requirements?: object;
      settle?: boolean;
      fee?: string;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const result = await wallet.payX402({
          to: opts.to,
          amount: opts.amount ?? '0',
          amountSPLD: opts.amountSPLD,
          requirements: opts.requirements,
          settle: opts.settle,
          fee: opts.fee,
        });
        setStatus('success');
        return result;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { pay, status, error, reset };
}
