/**
 * Browser entry — loads WASM via fetch only (no Node fs).
 * Import: `import { SplendorPQBridge } from '@splendor/sqc-sdk/browser'`
 */
export * from './public-api.js';
export { loadWasmInBrowser } from './wasm-loader.browser.js';
export { SplendorPQBridge as default } from './splendor-wallet.js';
