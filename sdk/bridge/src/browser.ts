/**
 * Browser entry — loads WASM via fetch only (no Node fs).
 * Import: `import { SplendorPQBridge } from '@splendor/pq-bridge/browser'`
 */
export * from './index.js';

// Re-export browser loader for advanced use
export { loadWasmInBrowser } from './wasm-loader.browser.js';
