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
import { defaultBrowserConnectors } from '../connectors/defaultBrowserConnectors.js';
import {
  createConnectController,
  createStubConnectController,
  type ConnectController,
  type CreateConnectControllerOptions,
} from '../core/controller.js';
import type { SignApprover } from '../core/signApproval.js';
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
  HostModalsContext,
  NetworkSwitchContext,
  type ConnectBrandLabels,
} from './context.js';

export type { ConnectBrandLabels };

export type SplendorConnectProviderProps = Omit<CreateClientOptions, 'network'> & {
  network?: NetworkId;
  /** Explicit connectors, or `'auto'` for extension + native seed. */
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
   * Sensitive methods require approval — pass `injectedOnApprove` or they fail closed.
   */
  exposeInjectedProvider?: boolean;
  /**
   * Approval gate for injected `splendor_sign*` / `send*` / disconnect / switchNetwork.
   * When `exposeInjectedProvider` is true and this is omitted, sensitive methods reject
   * (no silent page-script signing).
   */
  injectedOnApprove?: (args: { method: string; params?: unknown }) => boolean | Promise<boolean>;
  /**
   * Approve each signature made with an in-page (seed) key. Default: the built-in
   * `SignApprovalModal` asks the user. Return false to reject (`USER_REJECTED`).
   * `() => true` signs without asking — only for tests or trusted kiosk apps.
   * Extension sessions are not affected; the extension prompts itself.
   */
  onBeforeSign?: SignApprover;
};

const DEFAULT_IDLE_MS = 15 * 60 * 1000;

function isProductionBuild(): boolean {
  try {
    return typeof process !== 'undefined' && process.env?.NODE_ENV === 'production';
  } catch {
    return false;
  }
}

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
    autoReconnect = true,
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
    injectedOnApprove,
    onBeforeSign,
    ...clientOpts
  } = props;

  syncRegisterCustomNetworks(customNetworks);

  const [networkId, setNetworkId] = useState<NetworkId>(() =>
    // SSR-safe: never read localStorage during first paint (avoids hydration mismatch).
    networkProp && isUsableNetworkId(String(networkProp), allowPlaceholderNetworks)
      ? networkProp
      : 'local',
  );
  const [autoConnectors, setAutoConnectors] = useState<Connector[] | null>(null);
  const [switching, setSwitching] = useState(false);

  const controllerRef = useRef<ConnectController | null>(null);
  const networkIdRef = useRef(networkId);
  networkIdRef.current = networkId;
  const reconnectGenRef = useRef(0);
  const mountedRef = useRef(false);

  const softSwitchTo = useCallback(
    async (id: NetworkId) => {
      if (id === networkIdRef.current) return;
      setSwitching(true);
      try {
        const ctrl = controllerRef.current;
        if (ctrl && !ctrl.isStub && typeof ctrl.rebindNetwork === 'function') {
          try {
            await ctrl.rebindNetwork(id);
          } catch (e) {
            // The controller still talks to the old network, so the UI must keep saying so.
            onErrorRef.current?.(e instanceof Error ? e : new Error(String(e)));
            return;
          }
        }
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

  // After mount: restore persisted network (SSR-safe).
  useEffect(() => {
    if (!persistNetwork || networkProp) return;
    const last = getLastNetworkId();
    if (last && isUsableNetworkId(last, allowPlaceholderNetworks) && last !== networkIdRef.current) {
      void softSwitchTo(last as NetworkId);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

  // Idle policy is fixed for the controller lifetime (avoid rebuild on soft network switch).
  const effectiveIdle =
    sessionIdleMs !== undefined ? sessionIdleMs : DEFAULT_IDLE_MS;

  const connectorsKey = useMemo(
    () => connectors.map((c) => c.id).join(','),
    [connectors],
  );
  const customNetsKey = useMemo(
    () => (customNetworks ?? []).map((n) => n.id).join(','),
    [customNetworks],
  );
  const optsKey = JSON.stringify({
    // Network is soft-rebound via controller.rebindNetwork — do not rebuild (keeps WASM/extension).
    wasmUrl: clientOpts.wasmUrl,
    execUrl: clientOpts.execUrl,
    wasmSha256: clientOpts.wasmSha256,
    execSha256: clientOpts.execSha256,
    autoReconnect,
    connectorsKey,
    customNetsKey,
    // Sort arrays so referentially unstable but equal props do not rebuild the controller.
    rpcUrls: clientOpts.rpcUrls ? [...clientOpts.rpcUrls].sort() : undefined,
    gatewayUrl: clientOpts.gatewayUrl,
    chainId: clientOpts.chainId,
    timeoutMs: clientOpts.timeoutMs,
    retries: clientOpts.retries,
    sessionIdleMs: effectiveIdle,
    lockOnHidden: clientOpts.lockOnHidden === true,
    allowUnpinnedWasm: clientOpts.allowUnpinnedWasm === true,
    allowInsecureHttp: clientOpts.allowInsecureHttp === true,
    connectorsReady,
  });

  const connectorsRef = useRef(connectors);
  connectorsRef.current = connectors;
  const clientOptsRef = useRef(clientOpts);
  clientOptsRef.current = clientOpts;
  const customNetworksRef = useRef(customNetworks);
  customNetworksRef.current = customNetworks;
  const injectedOnApproveRef = useRef(injectedOnApprove);
  injectedOnApproveRef.current = injectedOnApprove;
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;
  const onBeforeSignRef = useRef(onBeforeSign);
  onBeforeSignRef.current = onBeforeSign;
  const hasCustomApprover = onBeforeSign != null;

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
      wasmUrl?: string;
      execUrl?: string;
      wasmSha256?: string;
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
    };
    try {
      const createOpts: CreateConnectControllerOptions = {
        ...clientOptsRef.current,
        network: networkIdRef.current,
        networks: customNetworksRef.current,
        wasmUrl: parsed.wasmUrl,
        execUrl: parsed.execUrl,
        wasmSha256: parsed.wasmSha256,
        allowUnpinnedWasm: parsed.allowUnpinnedWasm,
        allowInsecureHttp: parsed.allowInsecureHttp,
        rpcUrls: parsed.rpcUrls,
        gatewayUrl: parsed.gatewayUrl,
        chainId: parsed.chainId,
        timeoutMs: parsed.timeoutMs,
        retries: parsed.retries,
        sessionIdleMs: parsed.sessionIdleMs,
        lockOnHidden: parsed.lockOnHidden,
        connectors: connectorsRef.current,
        autoReconnect: parsed.autoReconnect,
        onError: (err) => onErrorRef.current?.(err),
        onBeforeSign: onBeforeSignRef.current
          ? (req) => (onBeforeSignRef.current ? onBeforeSignRef.current(req) : false)
          : 'modal',
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
    controller.setSignApprover?.(
      hasCustomApprover
        ? (req) => (onBeforeSignRef.current ? onBeforeSignRef.current(req) : false)
        : 'modal',
    );
  }, [controller, hasCustomApprover]);

  useEffect(() => {
    if (controller.isStub) return;
    const shouldReconnect = autoReconnect;

    const gen = ++reconnectGenRef.current;
    const isStale = () => gen !== reconnectGenRef.current;
    if (shouldReconnect) {
      void controller.tryAutoReconnect({ isStale });
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
      // Extension sessions already own window.splendor — never overlay an in-page inject.
      const isWasmWallet = typeof (wallet as { connectFromSeed?: unknown }).connectFromSeed === 'function';
      if (isWasmWallet) {
        installSplendorProvider({
          wallet: wallet as import('@splendor/sqc-sdk').SplendorPQBridge,
          connection: wallet.connection,
          networkId: net.id,
          onSwitchNetwork: async (id) => {
            await softSwitchTo(id as NetworkId);
          },
          onApprove: async (args) => {
            const cb = injectedOnApproveRef.current;
            if (!cb) return false;
            return cb(args);
          },
        });
      }
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

  useEffect(() => {
    if (clientOpts.allowUnpinnedWasm && networkId !== 'local' && isProductionBuild()) {
      console.warn(
        `@splendor/connect-react: allowUnpinnedWasm is set on network "${String(networkId)}" in a production build. ` +
          'The signer WASM is not integrity-checked — pass wasmSha256 and execSha256 (sqcWasmPlugin prints them).',
      );
    }
  }, [clientOpts.allowUnpinnedWasm, networkId]);

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
        <HostModalsContext.Provider value={hostModals}>
          <NetworkSwitchContext.Provider value={networkCtx}>
            <div data-sqc-theme={themeAttr} style={themeStyle} className="sqc-root">
              {children}
              {hostModals ? <ModalHost /> : null}
            </div>
          </NetworkSwitchContext.Provider>
        </HostModalsContext.Provider>
      </BrandContext.Provider>
    </ControllerContext.Provider>
  );
}

export {
  useConnectController,
  useConnectState,
  useBrandLabels,
  useHostModals,
  useNetworkSwitch,
  ControllerContext,
  BrandContext,
  HostModalsContext,
  NetworkSwitchContext,
} from './context.js';
