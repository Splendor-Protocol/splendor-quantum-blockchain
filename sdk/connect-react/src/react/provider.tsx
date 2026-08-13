import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import {
  registerNetwork,
  type CreateClientOptions,
  type NetworkConfig,
  type NetworkId,
} from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';
import { defaultBrowserConnectors } from '../connectors/eip6963.js';
import {
  createConnectController,
  createStubConnectController,
  type ConnectController,
  type CreateConnectControllerOptions,
} from '../core/controller.js';
import { getLastNetworkId, setLastNetworkId } from '../core/storage.js';
import type { ConnectControllerState } from '../core/types.js';
import {
  resolveThemeAttr,
  themeOverridesToStyle,
  type ThemeProp,
} from '../theme/themes.js';

export type ConnectBrandLabels = {
  connectTitle?: string;
  connectSubtitle?: string;
};

export type SplendorConnectProviderProps = Omit<CreateClientOptions, 'network'> & {
  network?: NetworkId;
  /** Explicit connectors, or `'auto'` for MetaMask + EIP-6963 + seed. */
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
};

const DEFAULT_IDLE_MS = 15 * 60 * 1000;

const ControllerContext = createContext<ConnectController | null>(null);
const BrandContext = createContext<ConnectBrandLabels>({});
const NetworkSwitchContext = createContext<{
  switchNetwork: (id: NetworkId) => Promise<void>;
  allowPlaceholderNetworks: boolean;
  networkId: NetworkId;
} | null>(null);

function useControllerStore(controller: ConnectController): ConnectControllerState {
  return useSyncExternalStore(
    (onStoreChange) => controller.subscribe(onStoreChange),
    () => controller.getState(),
    () => controller.getState(),
  );
}

function initialNetworkId(
  networkProp: NetworkId | undefined,
  persistNetwork: boolean,
): NetworkId {
  if (networkProp) return networkProp;
  if (persistNetwork && typeof window !== 'undefined') {
    const last = getLastNetworkId();
    if (last) return last;
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
    ...clientOpts
  } = props;

  const [networkId, setNetworkId] = useState<NetworkId>(() =>
    initialNetworkId(networkProp, persistNetwork),
  );
  const [autoConnectors, setAutoConnectors] = useState<Connector[] | null>(null);

  useEffect(() => {
    if (customNetworks?.length) {
      for (const n of customNetworks) registerNetwork(n);
    }
  }, [customNetworks]);

  useEffect(() => {
    if (networkProp) setNetworkId(networkProp);
  }, [networkProp]);

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
    connectorsProp === 'auto'
      ? autoConnectors ?? []
      : connectorsProp;

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
  const optsKey = JSON.stringify({
    network: networkId,
    wasmUrl: clientOpts.wasmUrl,
    execUrl: clientOpts.execUrl,
    wasmSha256: clientOpts.wasmSha256,
    execSha256: clientOpts.execSha256,
    derivation: clientOpts.derivation || 'eip191-v1',
    autoReconnect,
    connectorsKey,
    rpcUrls: clientOpts.rpcUrls,
    gatewayUrl: clientOpts.gatewayUrl,
    chainId: clientOpts.chainId,
    timeoutMs: clientOpts.timeoutMs,
    retries: clientOpts.retries,
    sessionIdleMs: effectiveIdle,
    allowUnpinnedWasm: clientOpts.allowUnpinnedWasm === true,
    allowInsecureHttp: clientOpts.allowInsecureHttp === true,
    watchMetaMaskAccounts: clientOpts.watchMetaMaskAccounts,
    connectorsReady,
  });

  const connectorsRef = useRef(connectors);
  connectorsRef.current = connectors;
  const clientOptsRef = useRef(clientOpts);
  clientOptsRef.current = clientOpts;
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;

  const controller = useMemo(() => {
    if (typeof window === 'undefined') {
      return createStubConnectController(connectorsRef.current);
    }
    if (!connectorsReady || !connectorsRef.current.length) {
      return createStubConnectController([]);
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
      watchMetaMaskAccounts?: boolean;
    };
    const createOpts: CreateConnectControllerOptions = {
      ...clientOptsRef.current,
      network: parsed.network,
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
      watchMetaMaskAccounts: parsed.watchMetaMaskAccounts,
      connectors: connectorsRef.current,
      autoReconnect: parsed.autoReconnect,
      onError: (err) => onErrorRef.current?.(err),
    };
    return createConnectController(createOpts);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [optsKey]);

  const controllerRef = useRef(controller);
  controllerRef.current = controller;
  const mountedRef = useRef(false);
  /** Soft network switch: reconnect with last connector even if autoReconnect is off. */
  const reconnectAfterSwitchRef = useRef(false);

  useEffect(() => {
    if (controller.isStub) return;
    const force = reconnectAfterSwitchRef.current;
    reconnectAfterSwitchRef.current = false;
    if (force || autoReconnect) {
      void controller.tryAutoReconnect({ force });
    }
    return () => {
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

  const switchNetwork = useCallback(
    async (id: NetworkId) => {
      if (id === networkId) return;
      const wasConnected =
        controllerRef.current &&
        !controllerRef.current.isStub &&
        controllerRef.current.getState().status === 'connected';
      if (controllerRef.current && !controllerRef.current.isStub) {
        try {
          await controllerRef.current.softDisconnect();
        } catch {
          /* ignore */
        }
      }
      if (wasConnected) reconnectAfterSwitchRef.current = true;
      if (persistNetwork) setLastNetworkId(String(id));
      setNetworkId(id);
    },
    [persistNetwork, networkId],
  );

  const themeAttr = resolveThemeAttr(theme);
  const themeStyle = themeOverridesToStyle(theme);

  const networkCtx = useMemo(
    () => ({
      switchNetwork,
      allowPlaceholderNetworks,
      networkId,
    }),
    [switchNetwork, allowPlaceholderNetworks, networkId],
  );

  return (
    <ControllerContext.Provider value={controller}>
      <BrandContext.Provider value={labels ?? {}}>
        <NetworkSwitchContext.Provider value={networkCtx}>
          <div data-sqc-theme={themeAttr} style={themeStyle} className="sqc-root">
            {children}
          </div>
        </NetworkSwitchContext.Provider>
      </BrandContext.Provider>
    </ControllerContext.Provider>
  );
}

export function useConnectController(): ConnectController {
  const ctrl = useContext(ControllerContext);
  if (!ctrl) {
    throw new Error('useConnect* hooks require <SplendorConnectProvider>');
  }
  return ctrl;
}

export function useConnectState(): ConnectControllerState {
  const ctrl = useConnectController();
  return useControllerStore(ctrl);
}

export function useBrandLabels(): ConnectBrandLabels {
  return useContext(BrandContext);
}

export function useNetworkSwitch() {
  const ctx = useContext(NetworkSwitchContext);
  if (!ctx) {
    throw new Error('useNetworkSwitch requires <SplendorConnectProvider>');
  }
  return ctx;
}

export { ControllerContext, BrandContext, NetworkSwitchContext };
