/**
 * Environment-aware WASM loader facade.
 * Node path dynamically imports fs-based loader so browser bundlers can tree-shake
 * when using the `./browser` package entry instead.
 */
import type { LoadWasmOpts, WasmSignerApi } from './wasm-types.js';

export type { WasmSignerApi, LoadWasmOpts } from './wasm-types.js';
export { unwrap, parseWasmJson } from './wasm-types.js';

let loadPromise: Promise<WasmSignerApi> | null = null;

function isNode(): boolean {
  return (
    typeof process !== 'undefined' &&
    process.versions != null &&
    process.versions.node != null
  );
}

export async function loadWasmSigner(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  if (loadPromise) return loadPromise;

  loadPromise = (async () => {
    if (isNode()) {
      const { loadWasmInNode } = await import('./wasm-loader.node.js');
      return loadWasmInNode(opts);
    }
    const { loadWasmInBrowser } = await import('./wasm-loader.browser.js');
    return loadWasmInBrowser(opts);
  })();

  return loadPromise;
}
