/**
 * Transport adapter — how the SDK talks to the chain.
 *
 * Shipped implementations: RpcTransport, MockTransport.
 * Gateway HTTP service lives under sdk/sqc-sdk/gateway/ — there is no
 * GatewayTransport class in this SDK pass; talk to the gateway via its HTTP API
 * or use RpcTransport against a node RPC.
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
