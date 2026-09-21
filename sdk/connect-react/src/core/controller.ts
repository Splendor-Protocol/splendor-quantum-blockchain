import {
  BridgeError,
  createSplendorClient,
  isBridgeError,
  type BridgeErrorCode,
  type CreateClientOptions,
  type DeriveInfo,
  type ExtensionSigner,
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

export type SessionWallet = SplendorPQBridge | ExtensionSigner;

export type CreateConnectControllerOptions = CreateClientOptions & {
  connectors: Connector[];
  /** Attempt to reconnect last resumable connector (e.g. extension) on init. Default false. */
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
    'RPC_UNAVAILABLE',
    'NETWORK',
    'WASM',
    'INVALID_ADDRESS',
    'INVALID_AMOUNT',
    'INVALID_FEE',
    'TIMEOUT',
    'TX_NOT_CONFIRMED',
    'TX_FAILED',
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
   * Try last connector (extension / resumable only — never a connector whose secret
   * the user types in).
   * Pass `{ force: true }` after a soft network switch (ignores autoReconnect flag).
   * Pass `isStale` so rapid soft-switches abort before mutating state.
   */
  tryAutoReconnect(opts?: {
    force?: boolean;
    isStale?: () => boolean;
  }): Promise<boolean>;
  getWallet(): SessionWallet;
  getProvider(): SqcProvider;
  getNetwork(): NetworkConfig;
  /** Replace the active signing wallet (used by extension connector). */
  setSessionWallet(wallet: SessionWallet): void;
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

  /** Active signing wallet — defaults to in-page WASM; extension connector replaces it. */
  let sessionWallet: SessionWallet = client.wallet as SplendorPQBridge;

  let status: ConnectionStatus = 'disconnected';
  let connectingStep: ConnectingStep = null;
  let account: ConnectAccount | null = null;
  let error: Error | null = null;
  let activeConnectorId: string | null = null;
  let modal: ModalView = 'closed';
  let destroyed = false;
  let connectInFlight: Promise<DeriveInfo> | null = null;
  /** Bumped on softDisconnect / disconnect / destroy so in-flight connect cannot commit. */
  let connectEpoch = 0;
  let snapshot: ConnectControllerState = {
    status,
    connectingStep,
    account,
    error,
    activeConnectorId,
    modal,
    connectors,
    network: client.network,
  };
  let connectorAccountsUnsub: (() => void) | null = null;
  let sessionWalletUnsubs: Array<() => void> = [];

  const unsubs: Array<() => void> = [];

  const clearConnectorAccountsWatch = () => {
    if (connectorAccountsUnsub) {
      connectorAccountsUnsub();
      connectorAccountsUnsub = null;
    }
  };

  const clearSessionWalletWatch = () => {
    for (const off of sessionWalletUnsubs) off();
    sessionWalletUnsubs = [];
  };

  const wireSessionWallet = (wallet: SessionWallet) => {
    clearSessionWalletWatch();
    sessionWalletUnsubs.push(
      wallet.on('connect', () => {
        syncFromWallet();
      }),
      wallet.on('accountsChanged', () => {
        syncFromWallet();
      }),
      wallet.on('lock', () => {
        account = null;
        connectingStep = null;
        status = 'locked';
        emit();
      }),
      wallet.on('disconnect', () => {
        account = null;
        activeConnectorId = null;
        status = 'disconnected';
        emit();
      }),
      wallet.on('error', (payload) => {
        error = errorFromWalletPayload(payload);
        account = null;
        activeConnectorId = null;
        status = 'error';
        options.onError?.(error);
        emit();
      }),
    );
  };

  const setSessionWallet = (wallet: SessionWallet) => {
    if (wallet !== sessionWallet) {
      // Do not disconnect the previous in-page client.wallet — extension owns keys.
      sessionWallet = wallet;
      wireSessionWallet(wallet);
    }
  };

  const refreshSnapshot = (): ConnectControllerState => {
    const next: ConnectControllerState = {
      status,
      connectingStep,
      account,
      error,
      activeConnectorId,
      modal,
      connectors,
      network: client.network,
    };
    const prev = snapshot;
    if (
      prev.status === next.status &&
      prev.connectingStep === next.connectingStep &&
      prev.account === next.account &&
      prev.error === next.error &&
      prev.activeConnectorId === next.activeConnectorId &&
      prev.modal === next.modal &&
      prev.connectors === next.connectors &&
      prev.network === next.network &&
      prev.connectorsLoading === next.connectorsLoading
    ) {
      return prev;
    }
    snapshot = next;
    return snapshot;
  };

  const emit = () => {
    refreshSnapshot();
    for (const l of listeners) l();
  };

  const syncFromWallet = () => {
    const info = sessionWallet.getDeriveInfo();
    if (info && sessionWallet.getConnectionState() === 'connected') {
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

  wireSessionWallet(sessionWallet);

  unsubs.push(
    subscribeConnectBroadcast((msg) => {
      if (msg.type === 'disconnect' && (status === 'connected' || status === 'locked')) {
        sessionWallet.disconnect();
        account = null;
        activeConnectorId = null;
        status = 'disconnected';
        emit();
      }
    }),
  );

  const getState = (): ConnectControllerState => snapshot;

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
      if (connectInFlight) {
        return Promise.reject(
          new BridgeError(
            'USER_REJECTED',
            'Another connect is already in progress — wait or cancel before switching connectors',
          ),
        );
      }

      const connector = connectors.find((c) => c.id === connectorId);
      if (!connector) {
        return Promise.reject(new Error(`Unknown connector: ${connectorId}`));
      }

      connectInFlight = (async () => {
        const epoch = connectEpoch;
        const stillCurrent = () => epoch === connectEpoch && !destroyed;
        error = null;
        status = 'connecting';
        connectingStep = 'init';
        activeConnectorId = connectorId;
        emit();

        try {
          const ready = await connector.ready();
          if (!stillCurrent()) {
            throw BridgeError.userRejected('Connect cancelled — session switched or disconnected');
          }
          if (!ready) {
            throw BridgeError.noProvider(
              connector.id === 'splendor-extension' || connector.id === 'extension'
                ? 'Splendor Quantum Wallet extension not found. Install the extension to continue.'
                : `Connector "${connector.name}" is not ready`,
            );
          }

          // Seed path needs WASM init on the in-page wallet; extension skips it.
          if (connector.id !== 'splendor-extension' && connector.id !== 'extension') {
            await (client.wallet as SplendorPQBridge).init();
          }
          if (!stillCurrent()) {
            throw BridgeError.userRejected('Connect cancelled — session switched or disconnected');
          }
          connectingStep = 'sign';
          emit();
          const info = await connector.connect(
            {
              wallet: client.wallet as SplendorPQBridge,
              connection: (client.wallet as SplendorPQBridge).connection,
              provider: client.provider,
              network: client.network,
              setSessionWallet,
            },
            opts,
          );
          if (!stillCurrent()) {
            // Stale connect completed after soft-switch — do not commit session.
            try {
              await connector.disconnect?.();
            } catch {
              /* ignore */
            }
            throw BridgeError.userRejected('Connect cancelled — session switched or disconnected');
          }
          account = toAccount(info);
          activeConnectorId = connectorId;
          const resumable = !needsUserSecret(connector);
          setLastConnectorId(resumable ? connectorId : null);
          status = 'connected';
          connectingStep = null;
          modal = 'closed';
          clearConnectorAccountsWatch();
          if (connector.onAccountsChanged) {
            connectorAccountsUnsub = connector.onAccountsChanged((_addr) => {
              syncFromWallet();
            });
          }
          if (resumable) {
            broadcastConnectEvent({
              type: 'connected',
              connectorId,
              address: info.address,
            });
          }
          emit();
          // Soft RPC probe — keep session connected but surface network-down copy.
          const probeEpoch = connectEpoch;
          void sessionWallet.connection.probeNetwork?.().then((probe) => {
            if (probeEpoch !== connectEpoch || destroyed) return;
            if (probe && !probe.ok) {
              error = probe.error;
              emit();
            }
          }).catch(() => {});
          return info;
        } catch (e) {
          if (!stillCurrent()) {
            // Discard error state from cancelled connect.
            connectingStep = null;
            if (status === 'connecting') status = 'disconnected';
            throw BridgeError.userRejected('Connect cancelled — session switched or disconnected');
          }
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
          if (epoch === connectEpoch) {
            connectInFlight = null;
          }
        }
      })();

      return connectInFlight;
    },
    async disconnect() {
      connectEpoch += 1;
      connectInFlight = null;
      const connector = connectors.find((c) => c.id === activeConnectorId);
      clearConnectorAccountsWatch();
      try {
        await connector?.disconnect?.();
      } catch {
        /* ignore */
      }
      sessionWallet.disconnect();
      if (sessionWallet !== client.wallet) {
        (client.wallet as SplendorPQBridge).disconnect();
      }
      setSessionWallet(client.wallet as SplendorPQBridge);
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
      connectEpoch += 1;
      connectInFlight = null;
      const connector = connectors.find((c) => c.id === activeConnectorId);
      clearConnectorAccountsWatch();
      try {
        await connector?.disconnect?.();
      } catch {
        /* ignore */
      }
      sessionWallet.disconnect();
      if (sessionWallet !== client.wallet) {
        setSessionWallet(client.wallet as SplendorPQBridge);
      }
      account = null;
      connectingStep = null;
      error = null;
      status = 'disconnected';
      modal = 'closed';
      // Keep lastConnectorId for autoReconnect after network switch.
      activeConnectorId = null;
      emit();
    },
    async tryAutoReconnect(opts?: { force?: boolean; isStale?: () => boolean }) {
      if (!options.autoReconnect && !opts?.force) return false;
      if (opts?.isStale?.()) return false;
      const last = getLastConnectorId();
      if (!last) return false;
      const connector = connectors.find((c) => c.id === last);
      if (!connector || needsUserSecret(connector)) return false;
      try {
        if (opts?.isStale?.()) return false;
        const ready = await connector.ready();
        if (opts?.isStale?.()) return false;
        if (!ready) return false;
        if (connector.authorized) {
          const ok = await connector.authorized();
          if (opts?.isStale?.()) return false;
          if (!ok) return false;
        }
        if (opts?.isStale?.()) return false;
        await controller.connect(last);
        if (opts?.isStale?.()) {
          // Stale after connect: soft-disconnect so the newer controller owns the session.
          try {
            await controller.softDisconnect();
          } catch {
            /* ignore */
          }
          return false;
        }
        return true;
      } catch (e) {
        if (opts?.isStale?.()) return false;
        if (isBridgeError(e) && e.code === 'USER_REJECTED') return false;
        error = e instanceof Error ? e : new Error(String(e));
        status = 'error';
        activeConnectorId = null;
        emit();
        return false;
      }
    },
    getWallet() {
      return sessionWallet;
    },
    getProvider() {
      return client.provider;
    },
    getNetwork() {
      return client.network;
    },
    setSessionWallet,
    destroy() {
      destroyed = true;
      connectEpoch += 1;
      connectInFlight = null;
      clearConnectorAccountsWatch();
      clearSessionWalletWatch();
      for (const off of unsubs) off();
      unsubs.length = 0;
      listeners.clear();
      sessionWallet.disconnect();
      if (sessionWallet !== client.wallet) {
        (client.wallet as SplendorPQBridge).disconnect();
      }
    },
  };

  refreshSnapshot();
  return controller;
}

/** SSR / pre-hydrate stub — hooks work; connect is a no-op reject. */
export function createStubConnectController(
  connectors: Connector[] = [],
  opts?: { connectorsLoading?: boolean },
): ConnectController {
  const listeners = new Set<Listener>();
  let snapshot: ConnectControllerState = {
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
  const emit = () => {
    for (const l of listeners) l();
  };
  const patch = (partial: Partial<ConnectControllerState>) => {
    const next = { ...snapshot, ...partial };
    if (
      next.status === snapshot.status &&
      next.connectingStep === snapshot.connectingStep &&
      next.account === snapshot.account &&
      next.error === snapshot.error &&
      next.activeConnectorId === snapshot.activeConnectorId &&
      next.modal === snapshot.modal &&
      next.connectors === snapshot.connectors &&
      next.network === snapshot.network &&
      next.connectorsLoading === snapshot.connectorsLoading
    ) {
      return;
    }
    snapshot = next;
    emit();
  };
  const notReady = () => {
    throw new Error('@splendor/connect-react: wallet not ready (SSR or hydrating)');
  };
  return {
    isStub: true,
    client: null as unknown as SplendorClient,
    getState: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    openModal(view = 'connect') {
      patch({ modal: view, error: null });
    },
    closeModal() {
      patch({ modal: 'closed', error: null });
    },
    clearError() {
      patch({ error: null });
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
    async tryAutoReconnect(_opts?: { force?: boolean; isStale?: () => boolean }) {
      return false;
    },
    getWallet: notReady,
    getProvider: notReady,
    getNetwork: notReady,
    setSessionWallet() {
      /* no-op during SSR */
    },
    destroy() {
      listeners.clear();
    },
  };
}

/** Map bridge / connector errors to short UI copy. */
export function connectErrorMessage(err: unknown): string {
  if (isBridgeError(err)) {
    if (err.userHint) return err.userHint;
    switch (err.code) {
      case 'USER_REJECTED':
        return 'Request rejected in wallet';
      case 'NO_PROVIDER':
        return 'No browser wallet found';
      case 'WASM':
        return 'Signer failed to load — check WASM hosting';
      case 'RPC_UNAVAILABLE':
        return 'Network unavailable — check your connection or try again later';
      case 'NETWORK':
        return 'Network error — cannot reach the chain';
      case 'RPC':
        return 'Network error';
      case 'CHAIN_MISMATCH':
        return 'Chain mismatch — check network config';
      case 'TIMEOUT':
        return 'Request timed out';
      case 'NOT_CONNECTED':
        return 'Wallet not connected';
      case 'TX_FAILED':
        return 'Transaction failed';
      case 'TX_NOT_CONFIRMED':
        return 'Transaction not confirmed in time';
      case 'INVALID_AMOUNT':
        return 'Invalid amount';
      case 'INVALID_FEE':
        return 'Invalid fee';
      case 'INVALID_ADDRESS':
        return 'Invalid address';
      case 'UNKNOWN':
        return 'Connection failed';
      default:
        return 'Connection failed';
    }
  }
  if (err instanceof Error) {
    if ((err as Error & { code?: string }).code === 'NO_PROVIDER') {
      return 'No browser wallet found';
    }
    if (
      /extension.*not found|Splendor Quantum Wallet|EIP-1193|provider.*not found|window\.ethereum/i.test(
        err.message,
      )
    ) {
      return 'No browser wallet found';
    }
    if (/user rejected|denied/i.test(err.message)) {
      return 'Request rejected in wallet';
    }
    return 'Connection failed';
  }
  return 'Connection failed';
}
