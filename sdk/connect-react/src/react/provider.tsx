import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import type { CreateClientOptions, NetworkId } from '@splendor/pq-bridge';
import type { Connector } from '../connectors/types.js';
import {
  createConnectController,
  type ConnectController,
  type CreateConnectControllerOptions,
} from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';
import {
  resolveThemeAttr,
  themeOverridesToStyle,
  type ThemeProp,
} from '../theme/themes.js';

export type SplendorConnectProviderProps = Omit<CreateClientOptions, 'network'> & {
  network?: NetworkId;
  connectors: Connector[];
  autoReconnect?: boolean;
  theme?: ThemeProp;
  children: ReactNode;
};

const ControllerContext = createContext<ConnectController | null>(null);

function useControllerStore(controller: ConnectController): ConnectControllerState {
  return useSyncExternalStore(
    (onStoreChange) => controller.subscribe(onStoreChange),
    () => controller.getState(),
    () => controller.getState(),
  );
}

export function SplendorConnectProvider(props: SplendorConnectProviderProps) {
  const {
    children,
    connectors,
    autoReconnect = false,
    theme = 'splendor',
    ...clientOpts
  } = props;

  const connectorsKey = useMemo(
    () => connectors.map((c) => c.id).join(','),
    [connectors],
  );
  const optsKey = JSON.stringify({
    network: clientOpts.network || 'local',
    wasmUrl: clientOpts.wasmUrl,
    execUrl: clientOpts.execUrl,
    wasmSha256: clientOpts.wasmSha256,
    execSha256: clientOpts.execSha256,
    derivation: clientOpts.derivation || 'eip191-v1',
    autoReconnect,
    connectorsKey,
    rpcUrls: clientOpts.rpcUrls,
    chainId: clientOpts.chainId,
    timeoutMs: clientOpts.timeoutMs,
    retries: clientOpts.retries,
    sessionIdleMs: clientOpts.sessionIdleMs,
    allowUnpinnedWasm: clientOpts.allowUnpinnedWasm === true,
    allowInsecureHttp: clientOpts.allowInsecureHttp === true,
    watchMetaMaskAccounts: clientOpts.watchMetaMaskAccounts,
  });

  const connectorsRef = useRef(connectors);
  connectorsRef.current = connectors;
  const clientOptsRef = useRef(clientOpts);
  clientOptsRef.current = clientOpts;

  const controller = useMemo(() => {
    if (typeof window === 'undefined') return null;
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
      chainId: parsed.chainId,
      timeoutMs: parsed.timeoutMs,
      retries: parsed.retries,
      sessionIdleMs: parsed.sessionIdleMs,
      watchMetaMaskAccounts: parsed.watchMetaMaskAccounts,
      connectors: connectorsRef.current,
      autoReconnect: parsed.autoReconnect,
    };
    return createConnectController(createOpts);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [optsKey]);

  useEffect(() => {
    if (!controller) return;
    void controller.tryAutoReconnect();
    return () => {
      controller.destroy();
    };
  }, [controller]);

  const themeAttr = resolveThemeAttr(theme);
  const themeStyle = themeOverridesToStyle(theme);

  if (!controller) {
    return (
      <div data-sqc-theme={themeAttr} style={themeStyle} className="sqc-root">
        {children}
      </div>
    );
  }

  return (
    <ControllerContext.Provider value={controller}>
      <div data-sqc-theme={themeAttr} style={themeStyle} className="sqc-root">
        {children}
      </div>
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

export { ControllerContext };
