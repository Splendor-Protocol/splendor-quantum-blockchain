/**
 * Unsupported leftover DeFi application adapters (DEX AMM + Exchange settlement).
 *
 * NOT for production dapps — the node does not route `dex` / `exchange` via
 * DispatchModuleCall. Kept for experiments and future chain support.
 *
 * Prefer: `import { DEX, Exchange } from '@splendor/sqc-sdk/defi'`
 * Optional registry: `registerUnsupportedApplicationAdapters()` then
 * `createModuleClient('dex', connection, addr)`.
 *
 * Do not use the root package or Session / modules.bind for these clients.
 */
export { DEX, deployPool, getPool } from './dex.js';
export { Exchange, deployExchange, getExchange } from './exchange.js';
export type { ExchangeDeployParams } from './exchange.js';
export type {
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
} from '../types.js';
