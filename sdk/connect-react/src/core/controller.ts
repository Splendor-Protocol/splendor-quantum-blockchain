import {
  BridgeError,
  createSplendorClient,
  isBridgeError,
  type BridgeErrorCode,
  type CreateClientOptions,
  type DeriveInfo,
  type NetworkConfig,
  type SplendorClient,
  type SplendorPQBridge,
  type SqcProvider,
} from '@splendor/sqc-sdk';
import type { Connector } from '../connectors/types.js';
import { broadcastConnectEvent, getLastConnectorId, setLastConnectorId, subscribeConnectBroadcast } from './storage.js';
import type {
  ConnectAccount,
  ConnectionStatus,
  ConnectingStep,
  ConnectControllerState,
  ModalView,
} from './types.js';

export type CreateConnectControllerOptions = CreateClientOptions & {
  connectors: Connector[];
  /** Attempt to reconnect last MetaMask/injected connector on init. Default false. */
  autoReconnect?: boolean;
  /** Called when connect/session errors occur (optional observability). */
  onError?: (err: Error) => void;
};

type Listener = () => void;

/**
 * True when connecting requires a secret the user types in (native seed, or any
 * advanced connector). Such a session cannot be resumed without asking again,
 * so its id is never persisted and autoReconnect skips it — otherwise a reload
 * would fire a connect with an empty seed and land the UI in `error`.
 */
function needsUserSecret(connector: Connector): boolean {
  return connector.advanced === true || (connector.connectFields?.length ?? 0) > 0;
}

function toAccount(info: DeriveInfo): ConnectAccount {
  return {
    address: info.address,
    evmAddress: info.evmAddress,
    info,
  };
}

function errorFromWalletPayload(payload: { code: string; message: string }): Error {
  const code = payload.code as BridgeErrorCode;
  const known: BridgeErrorCode[] = [
    'NOT_CONNECTED',
    'USER_REJECTED',
    'NO_PROVIDER',
    'RPC',
    'NETWORK',
    'WASM',
    'INVALID_ADDRESS',
    'INVALID_AMOUNT',
    'INVALID_FEE',
    'TIMEOUT',
    'TX_NOT_CONFIRMED',
    'CHAIN_MISMATCH',
    'UNKNOWN',
  ];
  if (known.includes(code)) {
    return new BridgeError(code, payload.message);
  }
  return new BridgeError('UNKNOWN', payload.message);
}

export type ConnectController = {
  readonly client: SplendorClient;
  readonly isStub?: boolean;
  getState(): ConnectControllerState;
  subscribe(listener: Listener): () => void;
  openModal(view?: 'connect' | 'account'): void;
  closeModal(): void;
  clearError(): void;
  connect(connectorId: string, opts?: Record<string, unknown>): Promise<DeriveInfo>;
  disconnect(): Promise<void>;
  /** Tear down wallet without clearing lastConnectorId (network switch). */
  softDisconnect(): Promise<void>;
  /**
   * Try last connector (MetaMask/injected only — never a connector whose secret
   * the user types in).
   * Pass `{ force: true }` after a soft network switch (ignores autoReconnect flag).
   */
  tryAutoReconnect(opts?: { force?: boolean }): Promise<boolean>;
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
  let connectingStep: ConnectingStep = null;
  let account: ConnectAccount | null = null;
  let error: Error | null = null;
  let activeConnectorId: string | null = null;
  let modal: ModalView = 'closed';
  let destroyed = false;
  let connectInFlight: Promise<DeriveInfo> | null = null;

  const unsubs: Array<() => void> = [];

  const emit = () => {
    for (const l of listeners) l();
  };

  const syncFromWallet = () => {
    const info = client.wallet.getDeriveInfo();
    if (info && client.wallet.getConnectionState() === 'connected') {
      account = toAccount(info);
      status = 'connected';
    } else if (status === 'locked') {
      account = null;
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
    client.wallet.on('lock', () => {
      account = null;
      connectingStep = null;
      status = 'locked';
      emit();
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
      error = errorFromWalletPayload(payload);
      account = null;
      activeConnectorId = null;
      status = 'error';
      options.onError?.(error);
      emit();
    }),
  );

  unsubs.push(
    subscribeConnectBroadcast((msg) => {
      if (msg.type === 'disconnect' && (status === 'connected' || status === 'locked')) {
        client.wallet.disconnect();
        account = null;
        activeConnectorId = null;
        status = 'disconnected';
        emit();
      }
    }),
  );

  const getState = (): ConnectControllerState => ({
    status,
    connectingStep,
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
      error = null;
      modal = view;
      emit();
    },
    closeModal() {
      modal = 'closed';
      error = null;
      emit();
    },
    clearError() {
      error = null;
      if (status === 'error') status = account ? 'connected' : 'disconnected';
      emit();
    },
    connect(connectorId, opts) {
      if (destroyed) {
        return Promise.reject(new Error('ConnectController destroyed'));
      }
      if (connectInFlight) return connectInFlight;

      const connector = connectors.find((c) => c.id === connectorId);
      if (!connector) {
        return Promise.reject(new Error(`Unknown connector: ${connectorId}`));
      }

      connectInFlight = (async () => {
        error = null;
        status = 'connecting';
        connectingStep = 'init';
        activeConnectorId = connectorId;
        emit();

        try {
          const ready = await connector.ready();
          if (!ready) {
            throw BridgeError.noProvider(
              connector.id === 'metamask'
                ? 'MetaMask (or compatible wallet) not found. Install an EIP-1193 wallet.'
                : `Connector "${connector.name}" is not ready`,
            );
          }

          await client.wallet.init();
          connectingStep = 'sign';
          emit();
          const info = await connector.connect(
            {
              wallet: client.wallet,
              connection: client.wallet.connection,
              provider: client.provider,
              network: client.network,
            },
            opts,
          );
          account = toAccount(info);
          activeConnectorId = connectorId;
          const resumable = !needsUserSecret(connector);
          setLastConnectorId(resumable ? connectorId : null);
          status = 'connected';
          connectingStep = null;
          modal = 'closed';
          if (resumable) {
            broadcastConnectEvent({
              type: 'connected',
              connectorId,
              address: info.address,
            });
          }
          emit();
          return info;
        } catch (e) {
          const err = e instanceof Error ? e : new Error(String(e));
          error = err;
          account = null;
          activeConnectorId = null;
          connectingStep = null;
          status = 'error';
          options.onError?.(err);
          emit();
          throw err;
        } finally {
          connectInFlight = null;
        }
      })();

      return connectInFlight;
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
      connectingStep = null;
      error = null;
      status = 'disconnected';
      setLastConnectorId(null);
      modal = 'closed';
      broadcastConnectEvent({ type: 'disconnect' });
      emit();
    },
    async softDisconnect() {
      const connector = connectors.find((c) => c.id === activeConnectorId);
      try {
        await connector?.disconnect?.();
      } catch {
        /* ignore */
      }
      client.wallet.disconnect();
      account = null;
      connectingStep = null;
      error = null;
      status = 'disconnected';
      modal = 'closed';
      // Keep lastConnectorId for autoReconnect after network switch.
      activeConnectorId = null;
      emit();
    },
    async tryAutoReconnect(opts?: { force?: boolean }) {
      if (!options.autoReconnect && !opts?.force) return false;
      const last = getLastConnectorId();
      if (!last) return false;
      const connector = connectors.find((c) => c.id === last);
      if (!connector || needsUserSecret(connector)) return false;
      try {
        const ready = await connector.ready();
        if (!ready) return false;
        if (connector.authorized) {
          const ok = await connector.authorized();
          if (!ok) return false;
        }
        await controller.connect(last);
        return true;
      } catch (e) {
        if (isBridgeError(e) && e.code === 'USER_REJECTED') return false;
        error = e instanceof Error ? e : new Error(String(e));
        status = 'error';
        activeConnectorId = null;
        emit();
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

/** SSR / pre-hydrate stub — hooks work; connect is a no-op reject. */
export function createStubConnectController(
  connectors: Connector[] = [],
  opts?: { connectorsLoading?: boolean },
): ConnectController {
  const listeners = new Set<Listener>();
  const emit = () => {
    for (const l of listeners) l();
  };
  const state: ConnectControllerState = {
    status: 'disconnected',
    connectingStep: null,
    account: null,
    error: null,
    activeConnectorId: null,
    modal: 'closed',
    connectors,
    network: null,
    connectorsLoading: opts?.connectorsLoading === true,
  };
  const notReady = () => {
    throw new Error('@splendor/connect-react: wallet not ready (SSR or hydrating)');
  };
  return {
    isStub: true,
    client: null as unknown as SplendorClient,
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    openModal(view = 'connect') {
      state.modal = view;
      state.error = null;
      emit();
    },
    closeModal() {
      state.modal = 'closed';
      state.error = null;
      emit();
    },
    clearError() {
      state.error = null;
      emit();
    },
    async connect() {
      throw new Error('@splendor/connect-react: cannot connect during SSR');
    },
    async disconnect() {
      /* no-op */
    },
    async softDisconnect() {
      /* no-op */
    },
    async tryAutoReconnect(_opts?: { force?: boolean }) {
      return false;
    },
    getWallet: notReady,
    getProvider: notReady,
    getNetwork: notReady,
    destroy() {
      listeners.clear();
    },
  };
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
      case 'TIMEOUT':
        return 'Request timed out';
      case 'NOT_CONNECTED':
        return 'Wallet not connected';
      case 'UNKNOWN':
        return err.message || 'Connection failed';
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
