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

export function useControllerStore(controller: ConnectController): ConnectControllerState;
export function useControllerStore<T>(
  controller: ConnectController,
  selector: (s: ConnectControllerState) => T,
): T;
export function useControllerStore<T = ConnectControllerState>(
  controller: ConnectController,
  selector?: (s: ConnectControllerState) => T,
): T {
  const select = (selector ?? ((s: ConnectControllerState) => s as unknown as T)) as (
    s: ConnectControllerState,
  ) => T;
  return useSyncExternalStore(
    (onStoreChange) => controller.subscribe(onStoreChange),
    () => select(controller.getState()),
    () => select(controller.getState()),
  );
}

export function useConnectController(): ConnectController {
  const ctrl = useContext(ControllerContext);
  if (!ctrl) {
    throw new Error('useConnect* hooks require <SplendorConnectProvider>');
  }
  return ctrl;
}

export function useConnectState(): ConnectControllerState;
export function useConnectState<T>(selector: (s: ConnectControllerState) => T): T;
export function useConnectState<T = ConnectControllerState>(
  selector?: (s: ConnectControllerState) => T,
): T {
  const ctrl = useConnectController();
  if (selector) return useControllerStore(ctrl, selector);
  return useControllerStore(ctrl) as T;
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
