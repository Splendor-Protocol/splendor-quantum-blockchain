/**
 * Browser entry — loads WASM via fetch only (no Node fs).
 * Import: `import { Wallet } from '@splendor/sqc-sdk/browser'`
 */
export * from './public-api.js';
export { loadWasmInBrowser } from './wasm-loader.browser.js';
export {
  createSplendorProvider,
  installSplendorProvider,
  uninstallSplendorProvider,
  getSplendorProvider,
} from './injected-provider.js';
export { Wallet, SplendorPQBridge as default } from './splendor-wallet.js';
