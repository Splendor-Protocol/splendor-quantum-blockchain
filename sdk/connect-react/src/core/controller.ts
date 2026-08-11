import {
  BridgeError,
  createSplendorClient,
  isBridgeError,
  type CreateClientOptions,
  type DeriveInfo,
  type NetworkConfig,
  type SplendorClient,
  type SplendorPQBridge,
  type SqcProvider,
} from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';
import { getLastConnectorId, setLastConnectorId } from './storage.js';
import type {
  ConnectAccount,
  ConnectionStatus,
  ConnectControllerState,
  ModalView,
} from './types.js';

export type CreateConnectControllerOptions = CreateClientOptions & {
  connectors: Connector[];
  /** Attempt to reconnect last MetaMask/injected connector on init. Default false. */
  autoReconnect?: boolean;
};

type Listener = () => void;

function toAccount(info: DeriveInfo): ConnectAccount {
  return {
    address: info.address,
    evmAddress: info.evmAddress,
    info,
  };
}

export type ConnectController = {
  readonly client: SplendorClient;
  getState(): ConnectControllerState;
  subscribe(listener: Listener): () => void;
  openModal(view?: 'connect' | 'account'): void;
  closeModal(): void;
  connect(connectorId: string, opts?: Record<string, unknown>): Promise<DeriveInfo>;
  disconnect(): Promise<void>;
  /** Try last connector (MetaMask/injected only — never seed). */
  tryAutoReconnect(): Promise<boolean>;
  getWallet(): SplendorPQBridge;
  getProvider(): SqcProvider;
  getNetwork(): NetworkConfig;
  destroy(): void;
};

/**
 * Headless connect controller: client + connectors + modal/account state.
 */
export function createConnectController(
  options: CreateConnectControllerOptions,
): ConnectController {
  if (!options.connectors?.length) {
    throw new Error('@splendor/connect-react: at least one connector is required');
  }

  const client = createSplendorClient(options);
  const connectors = [...options.connectors];
  const listeners = new Set<Listener>();

  let status: ConnectionStatus = 'disconnected';
  let account: ConnectAccount | null = null;
  let error: Error | null = null;
  let activeConnectorId: string | null = null;
  let modal: ModalView = 'closed';
  let destroyed = false;

  const unsubs: Array<() => void> = [];

  const emit = () => {
    for (const l of listeners) l();
  };

  const setStatus = (next: ConnectionStatus) => {
    status = next;
    emit();
  };

  const syncFromWallet = () => {
    const info = client.wallet.getDeriveInfo();
    if (info && client.wallet.getConnectionState() === 'connected') {
      account = toAccount(info);
      status = 'connected';
    } else {
      account = null;
      if (status !== 'connecting' && status !== 'error') status = 'disconnected';
    }
    emit();
  };

  unsubs.push(
    client.wallet.on('connect', () => {
      syncFromWallet();
    }),
  );
  unsubs.push(
    client.wallet.on('accountsChanged', () => {
      syncFromWallet();
    }),
  );
  unsubs.push(
    client.wallet.on('disconnect', () => {
      account = null;
      activeConnectorId = null;
      status = 'disconnected';
      emit();
    }),
  );
  unsubs.push(
    client.wallet.on('error', (payload) => {
      error = new Error(payload.message);
      account = null;
      status = 'error';
      emit();
    }),
  );

  const getState = (): ConnectControllerState => ({
    status,
    account,
    error,
    activeConnectorId,
    modal,
    connectors,
    network: client.network,
  });

  const controller: ConnectController = {
    client,
    getState,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    openModal(view = 'connect') {
      modal = view;
      emit();
    },
    closeModal() {
      modal = 'closed';
      emit();
    },
    async connect(connectorId, opts) {
      if (destroyed) throw new Error('ConnectController destroyed');
      const connector = connectors.find((c) => c.id === connectorId);
      if (!connector) throw new Error(`Unknown connector: ${connectorId}`);

      error = null;
      status = 'connecting';
      activeConnectorId = connectorId;
      emit();

      try {
        const ready = await connector.ready();
        if (!ready) {
          const err = BridgeError.noProvider(
            connector.id === 'metamask'
              ? 'MetaMask (or compatible wallet) not found. Install an EIP-1193 wallet.'
              : `Connector "${connector.name}" is not ready`,
          );
          throw err;
        }

        await client.wallet.init();
        const info = await connector.connect(
          {
            wallet: client.wallet,
            provider: client.provider,
            network: client.network,
          },
          opts,
        );
        account = toAccount(info);
        activeConnectorId = connectorId;
        setLastConnectorId(connectorId === 'seed' ? null : connectorId);
        status = 'connected';
        modal = 'closed';
        emit();
        return info;
      } catch (e) {
        const err = e instanceof Error ? e : new Error(String(e));
        error = err;
        account = null;
        status = 'error';
        emit();
        throw err;
      }
    },
    async disconnect() {
      const connector = connectors.find((c) => c.id === activeConnectorId);
      try {
        await connector?.disconnect?.();
      } catch {
        /* ignore */
      }
      client.wallet.disconnect();
      account = null;
      activeConnectorId = null;
      error = null;
      status = 'disconnected';
      setLastConnectorId(null);
      modal = 'closed';
      emit();
    },
    async tryAutoReconnect() {
      if (!options.autoReconnect) return false;
      const last = getLastConnectorId();
      if (!last || last === 'seed') return false;
      const connector = connectors.find((c) => c.id === last);
      if (!connector || connector.advanced) return false;
      try {
        const ready = await connector.ready();
        if (!ready) return false;
        await controller.connect(last);
        return true;
      } catch (e) {
        if (isBridgeError(e) && e.code === 'USER_REJECTED') return false;
        return false;
      }
    },
    getWallet() {
      return client.wallet;
    },
    getProvider() {
      return client.provider;
    },
    getNetwork() {
      return client.network;
    },
    destroy() {
      destroyed = true;
      for (const off of unsubs) off();
      unsubs.length = 0;
      listeners.clear();
      client.wallet.disconnect();
    },
  };

  return controller;
}

/** Map bridge / connector errors to short UI copy. */
export function connectErrorMessage(err: unknown): string {
  if (isBridgeError(err)) {
    switch (err.code) {
      case 'USER_REJECTED':
        return 'Request rejected in wallet';
      case 'NO_PROVIDER':
        return 'No browser wallet found';
      case 'WASM':
        return 'Signer failed to load — check WASM hosting';
      case 'NETWORK':
      case 'RPC':
        return err.message || 'Network error';
      case 'CHAIN_MISMATCH':
        return 'Chain mismatch — check network config';
      default:
        return err.message || 'Connection failed';
    }
  }
  if (err instanceof Error) {
    if ((err as Error & { code?: string }).code === 'NO_PROVIDER') {
      return 'No browser wallet found';
    }
    if (/EIP-1193|ethereum.*not found|MetaMask/i.test(err.message)) {
      return 'No browser wallet found';
    }
    if (/user rejected|denied/i.test(err.message)) {
      return 'Request rejected in wallet';
    }
    return err.message;
  }
  return 'Connection failed';
}
