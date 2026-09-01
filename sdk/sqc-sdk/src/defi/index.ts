/**
 * DeFi module clients (DEX, exchange on Splendor PQ chain).
 * Use wallet.signOrder / signCancel / signIncrementNonce for exchange auth,
 * then submit via sendModuleCall({ module: 'exchange', ... }).
 */
export type {
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
} from '../types.js';
