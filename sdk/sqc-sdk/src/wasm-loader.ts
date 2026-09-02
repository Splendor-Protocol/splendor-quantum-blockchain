/**
 * Environment-aware WASM loader facade.
 * Node path dynamically imports fs-based loader so browser bundlers can tree-shake
 * when using the `./browser` package entry instead.
 */
import type { LoadWasmOpts, WasmSignerApi } from './wasm-types.js';

export type { WasmSignerApi, LoadWasmOpts } from './wasm-types.js';
export { unwrap, parseWasmJson, assertWasmSha256, sha256Hex } from './wasm-types.js';

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
    try {
      if (isNode()) {
        // Variable + @vite-ignore: keep Node fs loader out of browser bundles
        // (Rollup otherwise statically follows both dynamic import branches).
        const nodeLoader = './wasm-loader.node.js';
        const { loadWasmInNode } = await import(/* @vite-ignore */ nodeLoader);
        return await loadWasmInNode(opts);
      }
      const { loadWasmInBrowser } = await import('./wasm-loader.browser.js');
      return await loadWasmInBrowser(opts);
    } catch (e) {
      loadPromise = null;
      throw e;
    }
  })();

  return loadPromise;
}

/** Reset singleton (tests / failed init recovery). */
export function resetWasmLoader(): void {
  loadPromise = null;
}
