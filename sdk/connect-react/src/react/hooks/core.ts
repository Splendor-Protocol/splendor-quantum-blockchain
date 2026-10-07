import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type {
  Connection,
  CallOpts,
  DeployOpts,
  DeriveInfo,
  ExtensionSigner,
  NetworkConfig,
  NetworkId,
  PreflightResult,
  SplendorPQBridge,
  SqcProvider,
  TransferOpts,
} from '@splendor/sqc-sdk';
import { bindModules } from '@splendor/sqc-sdk';
import { listSelectableNetworks, txExplorerUrl } from '@splendor/sqc-sdk/networks';
import type { Connector } from '../../connectors/types.js';
import { connectErrorMessage } from '../../core/controller.js';
import { sharedRead } from '../../core/readCache.js';
import type { ConnectAccount, ConnectingStep, ConnectionStatus } from '../../core/types.js';
import { toAccountDto, type AccountDto } from '../../core/dto.js';
import {
  useBrandLabels,
  useConnectController,
  useConnectState,
  useNetworkSwitch,
} from '../context.js';
import { useRecordPendingTx, useSingleFlight, useTxAction, type TxHookStatus } from '../useTxAction.js';

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
  const connectors = useConnectState((s) => s.connectors);
  const status = useConnectState((s) => s.status);
  const connectingStep = useConnectState((s) => s.connectingStep);
  const error = useConnectState((s) => s.error);
  const modal = useConnectState((s) => s.modal);
  const activeConnectorId = useConnectState((s) => s.activeConnectorId);
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
    connectors,
    status,
    connectingStep,
    error,
    errorMessage: error ? connectErrorMessage(error) : null,
    clearError,
    openModal,
    closeModal,
    modal,
    activeConnectorId,
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
  const account = useConnectState((s) => s.account);
  const status = useConnectState((s) => s.status);
  return {
    address: account?.address ?? null,
    evmAddress: account?.evmAddress ?? null,
    isConnected: status === 'connected' && !!account,
    info: account?.info ?? null,
    account,
  };
}

/** Thin account DTO for UI (avoids digging into SplendorPQBridge). */
export function useAccountDto(): AccountDto {
  const account = useConnectState((s) => s.account);
  const status = useConnectState((s) => s.status);
  return toAccountDto(account, status);
}

export function useBalance(pollMs = 12_000): {
  balance: string | null;
  balanceSPLD: string | null;
  refresh: () => Promise<void>;
  loading: boolean;
  error: Error | null;
  errorMessage: string | null;
} {
  const ctrl = useConnectController();
  const { address, isConnected } = useAccount();
  const networkId = useConnectState((s) => s.network?.id ?? null);
  const [balance, setBalance] = useState<string | null>(null);
  const [balanceSPLD, setBalanceSPLD] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  /** Bumped per request; a response from an older account/network is dropped. */
  const generation = useRef(0);

  useEffect(() => {
    setBalance(null);
    setBalanceSPLD(null);
    setError(null);
  }, [address, networkId]);

  const load = useCallback(async (force: boolean) => {
    const gen = ++generation.current;
    if (!address || !isConnected || ctrl.isStub) {
      setBalance(null);
      setBalanceSPLD(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const provider = ctrl.getProvider();
      const res = await sharedRead(provider, `balance|${address}`, () => provider.getBalance(address), {
        maxAgeMs: Math.min(pollMs / 2, 5_000),
        force,
      });
      if (gen !== generation.current) return;
      setBalance(res.balance);
      setBalanceSPLD(res.balance_spld);
    } catch (e) {
      if (gen !== generation.current) return;
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (gen === generation.current) setLoading(false);
    }
    // networkId: the provider is swapped on network switch, so refetch then too.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [address, isConnected, ctrl, networkId, pollMs]);

  const refresh = useCallback(() => load(true), [load]);

  useEffect(() => {
    void load(false);
    if (!isConnected || !pollMs) return;
    const id = setInterval(() => void load(false), pollMs);
    return () => clearInterval(id);
  }, [load, isConnected, pollMs]);

  return {
    balance,
    balanceSPLD,
    refresh,
    loading,
    error,
    errorMessage: error ? connectErrorMessage(error) : null,
  };
}

/**
 * Network whose chain id matches the wallet's. Prefers the app's current network, then
 * a public one over `local` (local shares mainnet's chain id).
 */
export function resolveWalletNetwork(
  walletChainId: string | null | undefined,
  networks: NetworkConfig[],
  current: NetworkConfig | null,
): NetworkConfig | null {
  if (!walletChainId) return null;
  if (current?.chainId === walletChainId) return current;
  const matches = networks.filter((n) => n.chainId === walletChainId);
  return matches.find((n) => n.id !== 'local') ?? matches[0] ?? null;
}

export function useNetwork(): {
  network: NetworkConfig | null;
  chainId: string | null;
  networkId: NetworkId;
  switchNetwork: (id: NetworkId) => Promise<void>;
  networks: NetworkConfig[];
  switching: boolean;
  /** Chain id the connected wallet reports (extension), or null when unknown / not applicable. */
  walletChainId: string | null;
  /** App network matching the wallet's chain id, when one is configured. */
  walletNetwork: NetworkConfig | null;
  /** True while connected and the wallet is on a different chain than the app. */
  walletMismatch: boolean;
} {
  const network = useConnectState((s) => s.network);
  const walletChainId = useConnectState((s) => s.walletChainId ?? null);
  const status = useConnectState((s) => s.status);
  const { switchNetwork, allowPlaceholderNetworks, networkId, switching } = useNetworkSwitch();
  const networks = listSelectableNetworks({
    allowPlaceholder: allowPlaceholderNetworks,
  });
  const connected = status === 'connected' || status === 'locked';
  const walletMismatch =
    connected && !!walletChainId && !!network?.chainId && walletChainId !== network.chainId;
  return {
    network,
    chainId: network?.chainId ?? null,
    networkId,
    switchNetwork,
    networks,
    switching,
    walletChainId: connected ? walletChainId : null,
    walletNetwork: connected ? resolveWalletNetwork(walletChainId, networks, network) : null,
    walletMismatch,
  };
}

/**
 * Connection for chain reads. Works without a connected wallet (it falls back to the
 * page wallet's RPC connection) and follows network switches.
 */
export function useReadConnection(): Connection | null {
  const ctrl = useConnectController();
  const networkId = useConnectState((s) => s.network?.id ?? null);
  const status = useConnectState((s) => s.status);
  const epoch =
    ctrl.isStub || typeof ctrl.getWalletEpoch !== 'function' ? 0 : ctrl.getWalletEpoch();
  return useMemo(() => {
    if (ctrl.isStub) return null;
    try {
      return ctrl.getWallet().connection;
    } catch {
      return null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctrl, networkId, status, epoch]);
}

/**
 * Active session wallet. `ready` is true only while connected and unlocked; a locked
 * session returns `wallet: null` so no hook can ask a locked signer to sign.
 */
export function useWallet(): {
  wallet: SplendorPQBridge | ExtensionSigner | null;
  provider: SqcProvider | null;
  ready: boolean;
} {
  const ctrl = useConnectController();
  const status = useConnectState((s) => s.status);
  const activeConnectorId = useConnectState((s) => s.activeConnectorId);
  // Re-render when session wallet identity changes (extension swap).
  const epoch =
    ctrl.isStub || typeof ctrl.getWalletEpoch !== 'function' ? 0 : ctrl.getWalletEpoch();
  return useMemo(() => {
    if (ctrl.isStub) {
      return { wallet: null, provider: null, ready: false };
    }
    if (status !== 'connected') {
      return { wallet: null, provider: ctrl.getProvider(), ready: false };
    }
    return {
      wallet: ctrl.getWallet(),
      provider: ctrl.getProvider(),
      ready: true,
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctrl, status, activeConnectorId, epoch]);
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
  errorMessage: string | null;
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

  return { preflight, status, result, error, errorMessage: error ? connectErrorMessage(error) : null, reset };
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
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
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

  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();
  const sendTransfer = useCallback(
    (opts: TransferOpts) => singleFlight(async () => {
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
        recordPending(res, {
          kind: 'transfer',
          summary: `Send ${opts.amountSPLD ? `${opts.amountSPLD} SPLD` : `${opts.amount ?? '?'} atto`} to ${opts.to}`,
        });
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
    }),
    [wallet, ready, network, singleFlight, recordPending],
  );

  return { sendTransfer, status, hash, explorerUrl, confirmed, error, errorMessage: error ? connectErrorMessage(error) : null, reset };
}

export function useCallContract(): {
  callContract: (opts: CallOpts) => Promise<{ status: string; hash?: string; explorerUrl?: string | null }>;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
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

  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();
  const callContract = useCallback(
    (opts: CallOpts) => singleFlight(async () => {
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
        recordPending(res, { kind: 'call', summary: `Call ${opts.function ?? 'contract'} on ${opts.contract}` });
        return { status: String(res.status), hash: res.hash, explorerUrl: exp };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    }),
    [wallet, ready, network, singleFlight, recordPending],
  );

  return { callContract, status, hash, explorerUrl, error, errorMessage: error ? connectErrorMessage(error) : null, reset };
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
  errorMessage: string | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const network = useConnectState((s) => s.network);
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

  const singleFlight = useSingleFlight();
  const recordPending = useRecordPendingTx();
  const deployContract = useCallback(
    (opts: DeployOpts) => singleFlight(async () => {
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
        recordPending(result, { kind: 'deploy', summary: `Deploy contract ${predicted}` });
        return { status: String(result.status), hash: result.hash, address: predicted, explorerUrl: exp };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    }),
    [wallet, ready, network, singleFlight, recordPending],
  );

  return { deployContract, status, hash, address, explorerUrl, error, errorMessage: error ? connectErrorMessage(error) : null, reset };
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

