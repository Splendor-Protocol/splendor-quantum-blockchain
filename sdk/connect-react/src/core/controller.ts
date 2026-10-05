import {
  BridgeError,
  Connection,
  createSplendorClient,
  getNetwork,
  isBridgeError,
  networkToRpcOptions,
  type BridgeErrorCode,
  type CreateClientOptions,
  type DeriveInfo,
  type ExtensionSigner,
  type NetworkConfig,
  type NetworkId,
  type SplendorClient,
  type SplendorPQBridge,
  type SqcProvider,
} from '@splendor/sqc-sdk';
import type { Connector } from '../connectors/types.js';
import { withSignApproval, type SignApprover, type SignRequest } from './signApproval.js';
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
  /** Attempt to reconnect last resumable connector (e.g. extension) on init. Default true in SplendorConnectProvider. */
  autoReconnect?: boolean;
  /** Called when connect/session errors occur (optional observability). */
  onError?: (err: Error) => void;
  /**
   * Approve each signature made with the in-page (seed) key. `'modal'` puts the request
   * in `state.signRequest` for the built-in approval modal; omit to sign without asking
   * (headless use). Extension sessions are never gated — the extension prompts itself.
   */
  onBeforeSign?: SignApprover | 'modal';
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
    'BUSY',
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
  /**
   * Soft network switch: rebind RPC/Connection without destroying WASM or revoking extension.
   * Keeps an unlocked seed/extension session across networks.
   */
  rebindNetwork(networkId: NetworkId): Promise<void>;
  /** Bumps when session wallet identity changes (for reactive hooks). */
  getWalletEpoch(): number;
  /** Replace the seed-session approver (see `onBeforeSign`). */
  setSignApprover?(approver: SignApprover | 'modal' | undefined): void;
  /** Answer the pending `state.signRequest` (used by the approval modal). */
  resolveSignRequest?(approved: boolean): void;
  /** Called by a mounted approval modal; returns the detach function. */
  attachSignApprovalUi?(): () => void;
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
  let walletChainId: string | null = null;
  let destroyed = false;
  let connectInFlight: Promise<DeriveInfo> | null = null;
  /** Bumped on softDisconnect / disconnect / destroy so in-flight connect cannot commit. */
  let connectEpoch = 0;
  /** Bumped when setSessionWallet changes the active signer (hook reactivity). */
  let walletEpoch = 0;
  let signApprover: SignApprover | 'modal' | undefined = options.onBeforeSign;
  let signRequest: SignRequest | null = null;
  let resolvePendingSign: ((ok: boolean) => void) | null = null;
  let signUiMounts = 0;
  /** Gated view of the in-page wallet; rebuilt only when the wallet object changes. */
  let gatedWallet: { target: SessionWallet; proxy: SessionWallet } | null = null;
  /** Debounce soft RPC probes after connect / network switch. */
  let lastProbeAt = 0;
  const PROBE_TTL_MS = 10_000;
  let snapshot: ConnectControllerState = {
    status,
    connectingStep,
    account,
    error,
    activeConnectorId,
    modal,
    connectors,
    network: client.network,
    walletChainId,
  };
  let connectorAccountsUnsub: (() => void) | null = null;
  let connectorChainUnsub: (() => void) | null = null;
  let sessionWalletUnsubs: Array<() => void> = [];

  const unsubs: Array<() => void> = [];

  const clearConnectorAccountsWatch = () => {
    if (connectorAccountsUnsub) {
      connectorAccountsUnsub();
      connectorAccountsUnsub = null;
    }
    if (connectorChainUnsub) {
      connectorChainUnsub();
      connectorChainUnsub = null;
    }
    walletChainId = null;
  };

  const watchConnectorChain = (connector: Connector) => {
    if (connector.onChainChanged) {
      connectorChainUnsub = connector.onChainChanged((id) => {
        if (destroyed || id === walletChainId) return;
        walletChainId = id;
        emit();
      });
    }
    if (connector.getChainId) {
      const epoch = connectEpoch;
      void connector
        .getChainId()
        .then((id) => {
          if (epoch !== connectEpoch || destroyed || !id || id === walletChainId) return;
          walletChainId = id;
          emit();
        })
        .catch(() => {});
    }
  };

  const clearSessionWalletWatch = () => {
    for (const off of sessionWalletUnsubs) off();
    sessionWalletUnsubs = [];
  };

  /** Soft /status probe — TTL so rapid connect + rebind does not spam RPC. */
  const scheduleProbe = (force = false) => {
    const now = Date.now();
    if (!force && now - lastProbeAt < PROBE_TTL_MS) return;
    lastProbeAt = now;
    const probeEpoch = connectEpoch;
    void sessionWallet.connection
      .probeNetwork?.()
      .then((probe) => {
        if (probeEpoch !== connectEpoch || destroyed) return;
        if (probe && !probe.ok) {
          error = probe.error;
          emit();
        }
      })
      .catch(() => {});
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
        // Keep session — rejected sign / RPC should not force a full disconnect.
        options.onError?.(error);
        emit();
      }),
    );
  };

  const setSessionWallet = (wallet: SessionWallet) => {
    if (wallet !== sessionWallet) {
      // Do not disconnect the previous in-page client.wallet — extension owns keys.
      sessionWallet = wallet;
      walletEpoch += 1;
      wireSessionWallet(wallet);
      emit();
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
      walletChainId,
      signRequest,
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
      prev.walletChainId === next.walletChainId &&
      prev.connectorsLoading === next.connectorsLoading &&
      prev.signRequest === next.signRequest
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

  const settleSignRequest = (ok: boolean) => {
    const resolve = resolvePendingSign;
    resolvePendingSign = null;
    if (signRequest) {
      signRequest = null;
      emit();
    }
    resolve?.(ok);
  };

  const approveSign = async (req: SignRequest): Promise<boolean> => {
    const approver = signApprover;
    if (!approver) return true;
    if (approver !== 'modal') return approver(req);
    if (signUiMounts === 0) {
      // Nobody can answer the request, so refuse rather than hang or sign silently.
      throw BridgeError.userRejected(
        'No signing approval UI is mounted — render <SignApprovalModal /> or pass onBeforeSign',
      );
    }
    if (resolvePendingSign) throw BridgeError.busy('Another signature is waiting for approval');
    return new Promise<boolean>((resolve) => {
      resolvePendingSign = resolve;
      signRequest = req;
      emit();
    });
  };

  /** The in-page signer holds the key, so the app must ask before each signature. */
  const walletForHooks = (): SessionWallet => {
    if (sessionWallet !== client.wallet) return sessionWallet;
    if (gatedWallet?.target !== sessionWallet) {
      gatedWallet = { target: sessionWallet, proxy: withSignApproval(sessionWallet, approveSign) };
    }
    return gatedWallet.proxy;
  };

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
          BridgeError.busy(
            'Another connect is already in progress — wait or cancel before switching connectors',
          ),
        );
      }

      const connector = connectors.find((c) => c.id === connectorId);
      if (!connector) {
        return Promise.reject(new Error(`Unknown connector: ${connectorId}`));
      }

      let flight: Promise<DeriveInfo> | undefined;
      flight = (async () => {
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
          watchConnectorChain(connector);
          if (resumable) {
            broadcastConnectEvent({
              type: 'connected',
              connectorId,
              address: info.address,
            });
          }
          emit();
          // Soft RPC probe — keep session connected but surface network-down copy.
          scheduleProbe();
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
          // Always release mutex when this flight ends (even if epoch was bumped).
          if (connectInFlight === flight) {
            connectInFlight = null;
          }
        }
      })();
      connectInFlight = flight;
      return flight;
    },
    async disconnect() {
      connectEpoch += 1;
      settleSignRequest(false);
      // Do not null connectInFlight here — mutex stays until the flight's finally.
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
      settleSignRequest(false);
      // Epoch discards stale commit; keep connectInFlight until unlock settles so overlapping connect is rejected.
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
      return walletForHooks();
    },
    getProvider() {
      return client.provider;
    },
    getNetwork() {
      return client.network;
    },
    setSessionWallet,
    getWalletEpoch() {
      return walletEpoch;
    },
    setSignApprover(approver) {
      signApprover = approver;
      // A pending modal request cannot be answered once the modal is gone.
      if (approver !== 'modal' && resolvePendingSign) settleSignRequest(false);
    },
    resolveSignRequest(approved) {
      settleSignRequest(approved);
    },
    attachSignApprovalUi() {
      signUiMounts += 1;
      let attached = true;
      return () => {
        if (!attached) return;
        attached = false;
        signUiMounts -= 1;
        if (signUiMounts === 0 && resolvePendingSign) settleSignRequest(false);
      };
    },
    async rebindNetwork(networkId) {
      if (destroyed) return;
      const net = getNetwork(String(networkId));
      if (!net) {
        throw BridgeError.rpc(`Unknown network: ${String(networkId)}`);
      }
      const next = new Connection(networkToRpcOptions(net));
      const pageWallet = client.wallet as SplendorPQBridge;
      if (typeof pageWallet.rebindConnection === 'function') {
        pageWallet.rebindConnection(next, { networkId: String(networkId) });
      }
      if (sessionWallet !== pageWallet && typeof (sessionWallet as ExtensionSigner).rebindConnection === 'function') {
        (sessionWallet as ExtensionSigner).rebindConnection(next, {
          networkId: String(networkId),
        });
      }
      // ClientSession is a plain object from createSplendorClient.
      (client as { network: NetworkConfig }).network = net;
      (client as { provider: SqcProvider }).provider = pageWallet.provider;
      error = null;
      emit();
      scheduleProbe();
    },
    destroy() {
      settleSignRequest(false);
      destroyed = true;
      connectEpoch += 1;
      // Flight's finally clears mutex; drop ref so destroy does not await unlock.
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

  unsubs.push(
    subscribeConnectBroadcast((msg) => {
      if (msg.type === 'disconnect' && (status === 'connected' || status === 'locked')) {
        sessionWallet.disconnect();
        account = null;
        activeConnectorId = null;
        status = 'disconnected';
        emit();
        return;
      }
      if (msg.type === 'connected' && status === 'disconnected') {
        void controller.tryAutoReconnect({ force: true }).catch(() => undefined);
      }
    }),
  );

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
    getWalletEpoch() {
      return 0;
    },
    async rebindNetwork() {
      /* no-op during SSR */
    },
    destroy() {
      listeners.clear();
    },
  };
}

/** Map bridge / connector errors to short UI copy. Never surface raw messages with secrets. */
export function connectErrorMessage(err: unknown): string {
  if (isBridgeError(err)) {
    // Prefer modal-aligned install / unlock copy over generic BridgeError hints.
    if (err.code === 'NO_PROVIDER') return 'Install Splendor Quantum Wallet';
    if (err.code === 'NOT_CONNECTED') {
      return err.userHint ?? 'Unlock or connect your Splendor Quantum Wallet';
    }
    if (err.userHint) return err.userHint;
    switch (err.code) {
      case 'USER_REJECTED':
        return 'Request rejected in wallet';
      case 'BUSY':
        return 'Connection already in progress — wait a moment';
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
      return 'Install Splendor Quantum Wallet';
    }
    if (
      /extension.*not found|Splendor Quantum Wallet|EIP-1193|provider.*not found|window\.ethereum/i.test(
        err.message,
      )
    ) {
      return 'Install Splendor Quantum Wallet';
    }
    if (/wallet locked|not connected|4100/i.test(err.message)) {
      return 'Unlock or connect your Splendor Quantum Wallet';
    }
    if (/user rejected|denied/i.test(err.message)) {
      return 'Request rejected in wallet';
    }
    return 'Connection failed';
  }
  return 'Connection failed';
}
