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
  /** Skip importing CSS when the host already loads @splendor/connect-react/style.css */
  skipCssImport?: boolean;
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
    skipCssImport: _skip,
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
    derivation: clientOpts.derivation || 'eip191-v1',
    autoReconnect,
    connectorsKey,
    rpcUrls: clientOpts.rpcUrls,
    allowUnpinnedWasm: clientOpts.allowUnpinnedWasm === true,
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
      rpcUrls?: string[];
    };
    const createOpts: CreateConnectControllerOptions = {
      ...clientOptsRef.current,
      network: parsed.network,
      wasmUrl: parsed.wasmUrl,
      execUrl: parsed.execUrl,
      wasmSha256: parsed.wasmSha256,
      derivation: parsed.derivation,
      allowUnpinnedWasm: parsed.allowUnpinnedWasm,
      rpcUrls: parsed.rpcUrls,
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
