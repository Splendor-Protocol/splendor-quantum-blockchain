/**
 * @splendor/sqc-sdk — Splendor Quantum Chain SDK.
 *
 * Beginner: connect(), deployQRC20(), sendSPLD()
 * Advanced: @splendor/sqc-sdk/core, /wallet, /tokens, /contracts, /x402
 */
export * from './public-api.js';

// v2 primary exports
export { SplendorClient } from './client.js';
export { connect } from './wallet/connect.js';
export type { ConnectOptions, ConnectResult } from './wallet/connect.js';
export { Connection } from './core/connection.js';
export { Wallet, SplendorPQBridge } from './splendor-wallet.js';
export { QRC20, deployQRC20, getToken } from './tokens/qrc20.js';
export type { QRC20DeployParams } from './tokens/qrc20.js';
export { Keypair } from './wallet/keypair.js';
export { Contract, deployBytecode, getContract } from './contracts/contract.js';
export { X402Client, createX402Middleware } from './x402/client.js';
export type { PaymentRequirements, PaymentPayload } from './x402/client.js';
export {
  MODULE_ROUTER_ADDRESS,
  qrc20Address,
  deriveModuleRouterAddress,
  bytecodeContractAddress,
} from './addresses/index.js';
export { subscribe } from './core/subscribe.js';

export { SplendorPQBridge as default } from './splendor-wallet.js';
