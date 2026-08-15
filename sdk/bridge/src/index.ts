/**
 * @splendor/pq-bridge — Node.js-first EVM→PQ wallet bridge for Splendor Quantum Chain.
 *
 * Single config: src/config/sqc.networks.json
 * One-touch: connectSplendor({ network: 'local', connect: 'metamask' })
 * Protocol: SQDP-1 (docs/SQDP-1.md)
 *
 * Browser apps should prefer `@splendor/pq-bridge/browser` (or package `browser` condition).
 */
export * from './public-api.js';
export { SplendorPQBridge as default } from './wallet.js';
