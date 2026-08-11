import { useCallback, useEffect, useState } from 'react';
import type { DeriveInfo, NetworkConfig, SplendorPQBridge, SqcProvider } from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';
import { connectErrorMessage } from '../core/controller.js';
import type { ConnectAccount, ConnectionStatus } from '../core/types.js';
import { useConnectController, useConnectState } from './provider.js';

export function useConnect(): {
  connect: (connectorId: string, opts?: Record<string, unknown>) => Promise<DeriveInfo>;
  disconnect: () => Promise<void>;
  connectors: Connector[];
  status: ConnectionStatus;
  error: Error | null;
  errorMessage: string | null;
  openModal: (view?: 'connect' | 'account') => void;
  closeModal: () => void;
  modal: 'closed' | 'connect' | 'account';
  activeConnectorId: string | null;
} {
  const ctrl = useConnectController();
  const state = useConnectState();

  const connect = useCallback(
    (connectorId: string, opts?: Record<string, unknown>) => ctrl.connect(connectorId, opts),
    [ctrl],
  );
  const disconnect = useCallback(() => ctrl.disconnect(), [ctrl]);
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
    error: state.error,
    errorMessage: state.error ? connectErrorMessage(state.error) : null,
    openModal,
    closeModal,
    modal: state.modal,
    activeConnectorId: state.activeConnectorId,
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
    if (!address || !isConnected) {
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
} {
  const state = useConnectState();
  const network = state.network;
  return {
    network,
    chainId: network?.chainId ?? null,
  };
}

export function useWallet(): {
  wallet: SplendorPQBridge;
  provider: SqcProvider;
} {
  const ctrl = useConnectController();
  return {
    wallet: ctrl.getWallet(),
    provider: ctrl.getProvider(),
  };
}
