import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  getNetwork,
  isPlaceholderNetwork,
  registerNetwork,
  installSplendorProvider,
  uninstallSplendorProvider,
  type CreateClientOptions,
  type NetworkConfig,
  type NetworkId,
} from '@splendor/sqc-sdk';
import type { Connector } from '../connectors/types.js';
import { defaultBrowserConnectors } from '../connectors/eip6963.js';
import {
  createConnectController,
  createStubConnectController,
  type ConnectController,
  type CreateConnectControllerOptions,
} from '../core/controller.js';
import { getLastNetworkId, setLastNetworkId } from '../core/storage.js';
import {
  resolveThemeAttr,
  themeOverridesToStyle,
  type ThemeProp,
} from '../theme/themes.js';
import { ModalHost } from '../ui/ModalHost.js';
import {
  BrandContext,
  ControllerContext,
  NetworkSwitchContext,
  type ConnectBrandLabels,
} from './context.js';

export type { ConnectBrandLabels };

export type SplendorConnectProviderProps = Omit<CreateClientOptions, 'network'> & {
  network?: NetworkId;
  /** Explicit connectors, or `'auto'` for native ML-DSA first, then MetaMask + EIP-6963. */
  connectors?: Connector[] | 'auto';
  autoReconnect?: boolean;
  theme?: ThemeProp;
  children: ReactNode;
  onError?: (err: Error) => void;
  labels?: ConnectBrandLabels;
  /** Include placeholder *.example networks in the switcher (default false). */
  allowPlaceholderNetworks?: boolean;
  /** Register custom networks at runtime (real RPCs / explorer URLs). */
  customNetworks?: NetworkConfig[];
  /** Restore last network from localStorage when network prop unset (default true). */
  persistNetwork?: boolean;
  /**
   * Mount ConnectModal + AccountModal once at provider level (default true).
   * Set false if you mount modals yourself or use ConnectButton includeModals.
   */
  hostModals?: boolean;
  /**
   * When true, install `window.splendor` bound to the ConnectKit wallet session
   * so other page scripts can call splendor_* methods. Default false.
   * Skips install if an extension provider is already present.
   */
  exposeInjectedProvider?: boolean;
};

const DEFAULT_IDLE_MS = 15 * 60 * 1000;

/** Register custom nets synchronously (safe before createSplendorClient). */
function syncRegisterCustomNetworks(nets?: NetworkConfig[]): void {
  if (!nets?.length) return;
  for (const n of nets) registerNetwork(n);
}

function isUsableNetworkId(id: string, allowPlaceholder: boolean): boolean {
  const net = getNetwork(id);
  if (!net) return false;
  if (id === 'local') return true;
  if (allowPlaceholder) return true;
  return !isPlaceholderNetwork(net);
}

function initialNetworkId(
  networkProp: NetworkId | undefined,
  persistNetwork: boolean,
  customNetworks: NetworkConfig[] | undefined,
  allowPlaceholder: boolean,
): NetworkId {
  syncRegisterCustomNetworks(customNetworks);
  if (networkProp && isUsableNetworkId(String(networkProp), allowPlaceholder)) {
    return networkProp;
  }
  if (persistNetwork && typeof window !== 'undefined') {
    const last = getLastNetworkId();
    if (last && isUsableNetworkId(last, allowPlaceholder)) return last;
  }
  return 'local';
}

export function SplendorConnectProvider(props: SplendorConnectProviderProps) {
  const {
    children,
    connectors: connectorsProp = 'auto',
    autoReconnect = false,
    theme = 'splendor',
    onError,
    labels,
    allowPlaceholderNetworks = false,
    customNetworks,
    persistNetwork = true,
    network: networkProp,
    sessionIdleMs,
    hostModals = true,
    exposeInjectedProvider = false,
    ...clientOpts
  } = props;

  syncRegisterCustomNetworks(customNetworks);

  const [networkId, setNetworkId] = useState<NetworkId>(() =>
    initialNetworkId(networkProp, persistNetwork, customNetworks, allowPlaceholderNetworks),
  );
  const [autoConnectors, setAutoConnectors] = useState<Connector[] | null>(null);
  const [switching, setSwitching] = useState(false);

  const controllerRef = useRef<ConnectController | null>(null);
  const networkIdRef = useRef(networkId);
  networkIdRef.current = networkId;
  const reconnectAfterSwitchRef = useRef(false);
  const reconnectGenRef = useRef(0);
  const mountedRef = useRef(false);

  const softSwitchTo = useCallback(
    async (id: NetworkId) => {
      if (id === networkIdRef.current) return;
      setSwitching(true);
      try {
        const ctrl = controllerRef.current;
        const wasConnected =
          !!ctrl &&
          !ctrl.isStub &&
          (ctrl.getState().status === 'connected' || ctrl.getState().status === 'locked');
        if (ctrl && !ctrl.isStub) {
          try {
            await ctrl.softDisconnect();
          } catch {
            /* ignore */
          }
        }
        if (wasConnected) reconnectAfterSwitchRef.current = true;
        if (persistNetwork) setLastNetworkId(String(id));
        setNetworkId(id);
      } finally {
        setSwitching(false);
      }
    },
    [persistNetwork],
  );

  useEffect(() => {
    if (networkProp && networkProp !== networkIdRef.current) {
      void softSwitchTo(networkProp);
    }
  }, [networkProp, softSwitchTo]);

  useEffect(() => {
    if (connectorsProp !== 'auto') {
      setAutoConnectors(null);
      return;
    }
    let cancelled = false;
    void defaultBrowserConnectors().then((list) => {
      if (!cancelled) setAutoConnectors(list);
    });
    return () => {
      cancelled = true;
    };
  }, [connectorsProp]);

  const connectors: Connector[] =
    connectorsProp === 'auto' ? (autoConnectors ?? []) : connectorsProp;

  const connectorsReady = connectorsProp !== 'auto' || autoConnectors != null;

  const effectiveIdle =
    sessionIdleMs !== undefined
      ? sessionIdleMs
      : networkId !== 'local'
        ? DEFAULT_IDLE_MS
        : undefined;

  const connectorsKey = useMemo(
    () => connectors.map((c) => c.id).join(','),
    [connectors],
  );
  const customNetsKey = useMemo(
    () => (customNetworks ?? []).map((n) => n.id).join(','),
    [customNetworks],
  );
  const optsKey = JSON.stringify({
    network: networkId,
    wasmUrl: clientOpts.wasmUrl,
    execUrl: clientOpts.execUrl,
    wasmSha256: clientOpts.wasmSha256,
    execSha256: clientOpts.execSha256,
    derivation: clientOpts.derivation || 'eip191-v1',
    autoReconnect,
    connectorsKey,
    customNetsKey,
    rpcUrls: clientOpts.rpcUrls,
    gatewayUrl: clientOpts.gatewayUrl,
    chainId: clientOpts.chainId,
    timeoutMs: clientOpts.timeoutMs,
    retries: clientOpts.retries,
    sessionIdleMs: effectiveIdle,
    lockOnHidden: clientOpts.lockOnHidden === true,
    allowUnpinnedWasm: clientOpts.allowUnpinnedWasm === true,
    allowInsecureHttp: clientOpts.allowInsecureHttp === true,
    watchMetaMaskAccounts: clientOpts.watchMetaMaskAccounts,
    connectorsReady,
  });

  const connectorsRef = useRef(connectors);
  connectorsRef.current = connectors;
  const clientOptsRef = useRef(clientOpts);
  clientOptsRef.current = clientOpts;
  const customNetworksRef = useRef(customNetworks);
  customNetworksRef.current = customNetworks;
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;

  const controller = useMemo(() => {
    syncRegisterCustomNetworks(customNetworksRef.current);
    if (typeof window === 'undefined') {
      return createStubConnectController(connectorsRef.current, {
        connectorsLoading: connectorsProp === 'auto',
      });
    }
    if (!connectorsReady || !connectorsRef.current.length) {
      return createStubConnectController([], { connectorsLoading: !connectorsReady });
    }
    const parsed = JSON.parse(optsKey) as {
      network?: NetworkId;
      wasmUrl?: string;
      execUrl?: string;
      wasmSha256?: string;
      derivation?: CreateClientOptions['derivation'];
      autoReconnect?: boolean;
      allowUnpinnedWasm?: boolean;
      allowInsecureHttp?: boolean;
      rpcUrls?: string[];
      gatewayUrl?: string;
      chainId?: string;
      timeoutMs?: number;
      retries?: number;
      sessionIdleMs?: number;
      lockOnHidden?: boolean;
      watchMetaMaskAccounts?: boolean;
    };
    try {
      const createOpts: CreateConnectControllerOptions = {
        ...clientOptsRef.current,
        network: parsed.network,
        networks: customNetworksRef.current,
        wasmUrl: parsed.wasmUrl,
        execUrl: parsed.execUrl,
        wasmSha256: parsed.wasmSha256,
        derivation: parsed.derivation,
        allowUnpinnedWasm: parsed.allowUnpinnedWasm,
        allowInsecureHttp: parsed.allowInsecureHttp,
        rpcUrls: parsed.rpcUrls,
        gatewayUrl: parsed.gatewayUrl,
        chainId: parsed.chainId,
        timeoutMs: parsed.timeoutMs,
        retries: parsed.retries,
        sessionIdleMs: parsed.sessionIdleMs,
        lockOnHidden: parsed.lockOnHidden,
        watchMetaMaskAccounts: parsed.watchMetaMaskAccounts,
        connectors: connectorsRef.current,
        autoReconnect: parsed.autoReconnect,
        onError: (err) => onErrorRef.current?.(err),
      };
      return createConnectController(createOpts);
    } catch (e) {
      console.error('@splendor/connect-react: failed to create controller', e);
      onErrorRef.current?.(e instanceof Error ? e : new Error(String(e)));
      return createStubConnectController(connectorsRef.current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [optsKey]);

  controllerRef.current = controller;

  useEffect(() => {
    if (controller.isStub) return;
    const force = reconnectAfterSwitchRef.current;
    reconnectAfterSwitchRef.current = false;
    const shouldReconnect = force || autoReconnect;

    const gen = ++reconnectGenRef.current;
    if (shouldReconnect) {
      void controller.tryAutoReconnect({ force }).finally(() => {
        void gen;
      });
    }

    return () => {
      reconnectGenRef.current += 1;
      const doomed = controller;
      setTimeout(() => {
        if (controllerRef.current !== doomed) doomed.destroy();
      }, 0);
    };
  }, [controller, autoReconnect]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      const doomed = controllerRef.current;
      setTimeout(() => {
        if (!mountedRef.current && controllerRef.current === doomed) {
          doomed?.destroy();
        }
      }, 0);
    };
  }, []);

  // Opt-in window.splendor for page scripts; emit chainChanged after soft switch.
  const chainAnnounceSkipRef = useRef(true);
  useEffect(() => {
    if (controller.isStub) return;
    let wallet: ReturnType<ConnectController['getWallet']>;
    try {
      wallet = controller.getWallet();
    } catch {
      return;
    }
    const net = controller.getNetwork();
    if (exposeInjectedProvider) {
      installSplendorProvider({
        wallet,
        connection: wallet.connection,
        networkId: net.id,
        onSwitchNetwork: async (id) => {
          await softSwitchTo(id as NetworkId);
        },
      });
    }
    if (chainAnnounceSkipRef.current) {
      chainAnnounceSkipRef.current = false;
    } else {
      wallet.notifyChainChanged({
        chainId: net.chainId,
        networkId: net.id,
      });
    }
    return () => {
      if (exposeInjectedProvider) uninstallSplendorProvider();
    };
  }, [controller, exposeInjectedProvider, softSwitchTo, networkId]);

  const switchNetwork = softSwitchTo;

  const themeAttr = resolveThemeAttr(theme);
  const themeStyle = themeOverridesToStyle(theme);

  const networkCtx = useMemo(
    () => ({
      switchNetwork,
      allowPlaceholderNetworks,
      networkId,
      switching,
    }),
    [switchNetwork, allowPlaceholderNetworks, networkId, switching],
  );

  return (
    <ControllerContext.Provider value={controller}>
      <BrandContext.Provider value={labels ?? {}}>
        <NetworkSwitchContext.Provider value={networkCtx}>
          <div data-sqc-theme={themeAttr} style={themeStyle} className="sqc-root">
            {children}
            {hostModals ? <ModalHost /> : null}
          </div>
        </NetworkSwitchContext.Provider>
      </BrandContext.Provider>
    </ControllerContext.Provider>
  );
}

export {
  useConnectController,
  useConnectState,
  useBrandLabels,
  useNetworkSwitch,
  ControllerContext,
  BrandContext,
  NetworkSwitchContext,
} from './context.js';
