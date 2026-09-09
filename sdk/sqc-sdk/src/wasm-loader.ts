/**
 * Environment-aware WASM loader facade.
 * Node path dynamically imports fs-based loader so browser bundlers can tree-shake
 * when using the `./browser` package entry instead.
 */
import type { LoadWasmOpts, WasmSignerApi } from './wasm-types.js';

export type { WasmSignerApi, LoadWasmOpts } from './wasm-types.js';
export { unwrap, parseWasmJson, assertWasmSha256, sha256Hex } from './wasm-types.js';
import { BridgeError } from './errors.js';

type CacheEntry = { key: string; promise: Promise<WasmSignerApi> };
let cache: CacheEntry | null = null;

function isNode(): boolean {
  return (
    typeof process !== 'undefined' &&
    process.versions != null &&
    process.versions.node != null
  );
}

function cacheKey(opts?: LoadWasmOpts): string {
  return JSON.stringify({
    wasmUrl: opts?.wasmUrl ?? '',
    execUrl: opts?.execUrl ?? '',
    wasmSha256: (opts?.wasmSha256 ?? '').toLowerCase(),
    execSha256: (opts?.execSha256 ?? '').toLowerCase(),
  });
}

export async function loadWasmSigner(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  const key = cacheKey(opts);
  if (cache) {
    if (cache.key !== key) {
      throw BridgeError.wasm(
        'WASM already loaded with different url/pin options — resetWasmLoader() or use matching pins',
      );
    }
    return cache.promise;
  }

  const promise = (async () => {
    try {
      if (isNode()) {
        const nodeLoader = './wasm-loader.node.js';
        const { loadWasmInNode } = await import(/* @vite-ignore */ nodeLoader);
        return await loadWasmInNode(opts);
      }
      const { loadWasmInBrowser } = await import('./wasm-loader.browser.js');
      return await loadWasmInBrowser(opts);
    } catch (e) {
      cache = null;
      throw e;
    }
  })();

  cache = { key, promise };
  return promise;
}

/** Reset singleton (tests / failed init recovery). */
export function resetWasmLoader(): void {
  cache = null;
}
