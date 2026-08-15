/**
 * Browser entry — loads WASM via fetch only (no Node fs).
 * Import: `import { SplendorPQBridge } from '@splendor/pq-bridge/browser'`
 */
export * from './public-api.js';
export { loadWasmInBrowser } from './wasm-loader.browser.js';
export { SplendorPQBridge as default } from './wallet.js';
