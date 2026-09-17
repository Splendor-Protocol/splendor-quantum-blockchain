/**
 * Unsupported DeFi application adapters (DEX AMM + Exchange settlement).
 * These are not routed native modules. Import here or call
 * registerUnsupportedApplicationAdapters() before using modules.dex / modules.exchange.
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
