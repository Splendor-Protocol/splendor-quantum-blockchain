/**
 * On-chain bridge module (`bridge_v1`) — distinct from `@splendor/pq-bridge` SDK shim.
 */
export {
  BridgeModule,
  deployBridge,
  getBridge,
} from './module.js';
export type { BridgeDeployParams, BridgeClaimParams } from './module.js';
