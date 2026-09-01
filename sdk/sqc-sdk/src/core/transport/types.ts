/**
 * Transport adapter — how the SDK talks to the chain (RPC, gateway, mock).
 */
import type { ModuleCall } from '../module-call.js';

export interface Transport {
  readonly chainId: string;
  readonly rpcUrls: string[];
  readonly rpcUrl: string;
  get<T>(path: string): Promise<T>;
  post<T>(path: string, body: unknown, stickyOnSuccess?: boolean): Promise<T>;
  moduleCall<T>(envelope: ModuleCall): Promise<T>;
}
