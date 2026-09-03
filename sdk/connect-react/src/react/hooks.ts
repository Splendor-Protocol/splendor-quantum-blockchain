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
import type { Connector } from '../connectors/types.js';
import { connectErrorMessage } from '../core/controller.js';
import type { ConnectAccount, ConnectingStep, ConnectionStatus } from '../core/types.js';
import {
  useBrandLabels,
  useConnectController,
  useConnectState,
  useNetworkSwitch,
} from './context.js';
import { useTxAction, type TxHookStatus } from './useTxAction.js';

export type { TxHookStatus } from './useTxAction.js';

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

export function useQRC20(tokenAddress: string | null, pollMs = 12_000): {
  balance: string | null;
  tokenInfo: Record<string, unknown> | null;
  refresh: () => Promise<void>;
  transfer: (to: string, amount: string) => Promise<{ hash?: string }>;
  approve: (spender: string, amount: string) => Promise<{ hash?: string }>;
  mint: (to: string, amount: string) => Promise<{ hash?: string }>;
  pause: () => Promise<{ hash?: string }>;
  unpause: () => Promise<{ hash?: string }>;
  submitPermit: (signed: {
    token: string;
    owner: string;
    spender: string;
    amount: string;
    deadline: number;
    signature: number[];
    owner_pubkey: number[];
  }) => Promise<{ hash?: string }>;
  loading: boolean;
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const { network } = useConnectState();
  const tx = useTxAction(network);
  const [balance, setBalance] = useState<string | null>(null);
  const [tokenInfo, setTokenInfo] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(false);
  const [readError, setReadError] = useState<Error | null>(null);
  const { address } = useAccount();

  const refresh = useCallback(async () => {
    if (!tokenAddress || !ready || !wallet) {
      setBalance(null);
      setTokenInfo(null);
      return;
    }
    setLoading(true);
    setReadError(null);
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
      setReadError(e instanceof Error ? e : new Error(String(e)));
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
    async (to: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).transfer(wallet, to, amount);
      });
      await refresh();
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, refresh],
  );

  const approve = useCallback(
    async (spender: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).approve(wallet, spender, amount);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  const mint = useCallback(
    async (to: string, amount: string) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).mint(wallet, to, amount);
      });
      await refresh();
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx, refresh],
  );

  const pause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
    const res = await tx.run(async () => {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC20(wallet.connection, tokenAddress).pause(wallet);
    });
    return { hash: res.hash };
  }, [ready, wallet, tokenAddress, tx]);

  const unpause = useCallback(async () => {
    if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
    const res = await tx.run(async () => {
      const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
      return new QRC20(wallet.connection, tokenAddress).unpause(wallet);
    });
    return { hash: res.hash };
  }, [ready, wallet, tokenAddress, tx]);

  const submitPermit = useCallback(
    async (signed: {
      token: string;
      owner: string;
      spender: string;
      amount: string;
      deadline: number;
      signature: number[];
      owner_pubkey: number[];
    }) => {
      if (!ready || !wallet || !tokenAddress) throw new Error('Wallet or token not ready');
      const res = await tx.run(async () => {
        const { QRC20 } = await import('@splendor/sqc-sdk/tokens');
        return new QRC20(wallet.connection, tokenAddress).submitPermit(wallet, signed as never);
      });
      return { hash: res.hash };
    },
    [ready, wallet, tokenAddress, tx],
  );

  return {
    balance,
    tokenInfo,
    refresh,
    transfer,
    approve,
    mint,
    pause,
    unpause,
    submitPermit,
    loading,
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error ?? readError,
    reset: tx.reset,
  };
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

export function useModuleCall(): {
  call: (opts: {
    module: string;
    action: string;
    params: Record<string, unknown>;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ hash?: string; status: string }>;
  status: TxHookStatus;
  hash: string | null;
  error: Error | null;
  reset: () => void;
} {
  const { wallet, ready } = useWallet();
  const [status, setStatus] = useState<TxHookStatus>('idle');
  const [hash, setHash] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);

  const reset = useCallback(() => {
    setStatus('idle');
    setHash(null);
    setError(null);
  }, []);

  const call = useCallback(
    async (opts: {
      module: string;
      action: string;
      params: Record<string, unknown>;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const res = await wallet.sendModuleCall(opts);
        setHash(res.hash ?? null);
        setStatus('success');
        return { hash: res.hash, status: String(res.status) };
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        setError(err);
        setStatus('error');
        throw err;
      }
    },
    [wallet, ready],
  );

  return { call, status, hash, error, reset };
}

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

export function useExchange(exchangeAddress: string | null): {
  info: () => Promise<Record<string, unknown>>;
  signOrder: (params: {
    condition: number;
    outcome: number;
    side: number;
    share_amount: string;
    price: string;
    expiration: number;
    nonce: number;
    salt?: string;
    recipient?: string;
  }) => Promise<object>;
  cancelOrder: (params: {
    condition: number;
    outcome: number;
    side: number;
    share_amount: string;
    price: string;
    expiration: number;
    nonce: number;
    salt: string;
    recipient?: string;
  }) => Promise<{ hash?: string }>;
  createCondition: (params: {
    collateral: string;
    resolver: string;
    endTime: number;
    outcomeCount?: number;
    questionHash: string;
  }) => Promise<{ hash?: string }>;
  split: (condition: number, amount: string) => Promise<{ hash?: string }>;
  merge: (condition: number, amount: string) => Promise<{ hash?: string }>;
  match: (params: {
    takerOrder: Record<string, unknown>;
    makerOrders: Array<Record<string, unknown>>;
    fillShares: string[];
  }) => Promise<{ hash?: string }>;
  resolve: (condition: number, outcome: number) => Promise<{ hash?: string }>;
  redeem: (condition: number) => Promise<{ hash?: string }>;
  /** @deprecated use `status` */
  txStatus: TxHookStatus;
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
    if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
    const { Exchange } = await import('@splendor/sqc-sdk/defi');
    return new Exchange(wallet.connection, exchangeAddress).info();
  }, [exchangeAddress, ready, wallet]);

  const signOrder = useCallback(
    async (params: {
      condition: number;
      outcome: number;
      side: number;
      share_amount: string;
      price: string;
      expiration: number;
      nonce: number;
      salt?: string;
      recipient?: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const { Exchange } = await import('@splendor/sqc-sdk/defi');
      return new Exchange(wallet.connection, exchangeAddress).signOrder(wallet, params);
    },
    [exchangeAddress, ready, wallet],
  );

  const cancelOrder = useCallback(
    async (params: {
      condition: number;
      outcome: number;
      side: number;
      share_amount: string;
      price: string;
      expiration: number;
      nonce: number;
      salt: string;
      recipient?: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        const ex = new Exchange(wallet.connection, exchangeAddress);
        const signed = await ex.signCancel(wallet, params);
        return ex.cancelOrder(wallet, signed);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const createCondition = useCallback(
    async (params: {
      collateral: string;
      resolver: string;
      endTime: number;
      outcomeCount?: number;
      questionHash: string;
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).createCondition(wallet, params);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const split = useCallback(
    async (condition: number, amount: string) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).split(wallet, condition, amount);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const merge = useCallback(
    async (condition: number, amount: string) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).merge(wallet, condition, amount);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const matchOrders = useCallback(
    async (params: {
      takerOrder: Record<string, unknown>;
      makerOrders: Array<Record<string, unknown>>;
      fillShares: string[];
    }) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).match(wallet, params);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const resolve = useCallback(
    async (condition: number, outcome: number) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).resolve(wallet, condition, outcome);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  const redeem = useCallback(
    async (condition: number) => {
      if (!exchangeAddress || !ready || !wallet) throw new Error('Exchange not ready');
      const res = await tx.run(async () => {
        const { Exchange } = await import('@splendor/sqc-sdk/defi');
        return new Exchange(wallet.connection, exchangeAddress).redeem(wallet, condition);
      });
      return { hash: res.hash };
    },
    [exchangeAddress, ready, wallet, tx],
  );

  return {
    info,
    signOrder,
    cancelOrder,
    createCondition,
    split,
    merge,
    match: matchOrders,
    resolve,
    redeem,
    txStatus: tx.status,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployExchange(): {
  deploy: (params: {
    name: string;
    uriPrefix?: string;
    redeemFeeBps?: number;
    feeRecipient?: string;
    fee?: string;
    waitForConfirmation?: boolean;
  }) => Promise<{ address: string; hash?: string; height: number }>;
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
      redeemFeeBps?: number;
      feeRecipient?: string;
      fee?: string;
      waitForConfirmation?: boolean;
    }) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployExchange } = await import('@splendor/sqc-sdk/defi');
        const { address: addr, height, result } = await deployExchange(wallet, params, {
          fee: params.fee,
          waitForConfirmation: params.waitForConfirmation,
        });
        setAddress(addr);
        setHash(result.hash ?? null);
        setStatus('success');
        return { address: addr, hash: result.hash, height };
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

export function useDex(poolAddress: string | null): {
  reserves: () => Promise<Record<string, unknown>>;
  swap: (params: {
    tokenIn: 0 | 1;
    amountIn: string;
    minAmountOut?: string;
  }) => Promise<{ hash?: string }>;
  addLiquidity: (params: {
    amount0Desired: string;
    amount1Desired: string;
    amount0Min?: string;
    amount1Min?: string;
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

  const reserves = useCallback(async () => {
    if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
    const { DEX } = await import('@splendor/sqc-sdk/defi');
    return new DEX(wallet.connection, poolAddress).reserves();
  }, [poolAddress, ready, wallet]);

  const swap = useCallback(
    async (params: { tokenIn: 0 | 1; amountIn: string; minAmountOut?: string }) => {
      if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
      const res = await tx.run(async () => {
        const { DEX } = await import('@splendor/sqc-sdk/defi');
        return new DEX(wallet.connection, poolAddress).swap(wallet, params);
      });
      return { hash: res.hash };
    },
    [poolAddress, ready, wallet, tx],
  );

  const addLiquidity = useCallback(
    async (params: {
      amount0Desired: string;
      amount1Desired: string;
      amount0Min?: string;
      amount1Min?: string;
    }) => {
      if (!poolAddress || !ready || !wallet) throw new Error('Pool not ready');
      const res = await tx.run(async () => {
        const { DEX } = await import('@splendor/sqc-sdk/defi');
        return new DEX(wallet.connection, poolAddress).addLiquidity(wallet, params);
      });
      return { hash: res.hash };
    },
    [poolAddress, ready, wallet, tx],
  );

  return {
    reserves,
    swap,
    addLiquidity,
    status: tx.status,
    hash: tx.hash,
    explorerUrl: tx.explorerUrl,
    error: tx.error,
    reset: tx.reset,
  };
}

export function useDeployPool(): {
  deploy: (
    token0: string,
    token1: string,
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
      token0: string,
      token1: string,
      opts?: { fee?: string; waitForConfirmation?: boolean },
    ) => {
      if (!ready || !wallet) throw new Error('Wallet not connected');
      setStatus('pending');
      setError(null);
      try {
        const { deployPool } = await import('@splendor/sqc-sdk/defi');
        const { address: addr, result } = await deployPool(wallet, token0, token1, opts);
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

/** Bound module clients from the connected wallet (address-only factories). */
export function useModules() {
  const { wallet, ready } = useWallet();
  return useMemo(() => {
    if (!ready || !wallet) return null;
    return bindModules(wallet.connection, wallet);
  }, [wallet, ready]);
}
