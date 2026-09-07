import {
  createContext,
  useContext,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import type { NetworkId } from '@splendor/sqc-sdk';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

export type ConnectBrandLabels = {
  connectTitle?: string;
  connectSubtitle?: string;
};

export const ControllerContext = createContext<ConnectController | null>(null);
export const BrandContext = createContext<ConnectBrandLabels>({});
export const NetworkSwitchContext = createContext<{
  switchNetwork: (id: NetworkId) => Promise<void>;
  allowPlaceholderNetworks: boolean;
  networkId: NetworkId;
  switching: boolean;
} | null>(null);

export function useControllerStore(controller: ConnectController): ConnectControllerState {
  return useSyncExternalStore(
    (onStoreChange) => controller.subscribe(onStoreChange),
    () => controller.getState(),
    () => controller.getState(),
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

export type { ReactNode };
