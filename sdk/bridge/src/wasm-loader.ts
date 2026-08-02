/**
 * Load Go WASM Dilithium signer in Node.js or browser.
 */
import { createRequire } from 'module';
import { fileURLToPath } from 'url';
import path from 'path';
import fs from 'fs';
import { pathToFileURL } from 'url';

export type WasmSignerApi = {
  sqDeriveFromSeedHex: (seedHex: string) => string | { error: string };
  sqDeriveFromSigHex: (sigHex: string) => string | { error: string };
  sqGetAddress: () => string | { error: string };
  sqGetPublicKeyB64: () => string | { error: string };
  sqSignTransfer: (paramsJson: string) => string | { error: string };
  sqSignCall: (paramsJson: string) => string | { error: string };
  sqSignDeploy: (paramsJson: string) => string | { error: string };
  sqSignX402: (paramsJson: string) => string | { error: string };
  sqLock: () => { ok: boolean };
  sqIsUnlocked: () => boolean;
  sqDomainMessage: () => string;
};

let loaded = false;
let loadPromise: Promise<WasmSignerApi> | null = null;

function unwrap<T>(v: T | { error: string }): T {
  if (v && typeof v === 'object' && 'error' in (v as object)) {
    throw new Error((v as { error: string }).error);
  }
  return v as T;
}

function resolveDistDir(): string {
  // Prefer package-relative dist (built wasm)
  const candidates: string[] = [];
  try {
    const here = path.dirname(fileURLToPath(import.meta.url));
    candidates.push(path.resolve(here, '../wasm/dist'));
    candidates.push(path.resolve(here, '../../wasm/dist'));
    candidates.push(path.resolve(here, '../dist/wasm'));
  } catch {
    /* CJS */
  }
  candidates.push(path.resolve(process.cwd(), 'sdk/bridge/wasm/dist'));
  candidates.push(path.resolve(process.cwd(), 'wasm/dist'));
  for (const c of candidates) {
    if (fs.existsSync(path.join(c, 'sqc_signer.wasm'))) return c;
  }
  throw new Error(
    'sqc_signer.wasm not found. Run sdk/bridge/wasm/build.ps1 (or build.sh) first.',
  );
}

async function loadInNode(): Promise<WasmSignerApi> {
  const dist = resolveDistDir();
  const wasmPath = path.join(dist, 'sqc_signer.wasm');
  const execPath = path.join(dist, 'wasm_exec.js');

  // Go's wasm_exec expects a global `Go` constructor and WebAssembly
  const require = createRequire(import.meta.url);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g: any = globalThis as any;

  if (!g.crypto?.getRandomValues) {
    const nodeCrypto = await import('crypto');
    g.crypto = {
      getRandomValues: (arr: Uint8Array) => nodeCrypto.webcrypto.getRandomValues(arr),
    };
  }

  // Load wasm_exec.js which defines global Go
  // Use dynamic import of file URL for ESM, or read+eval for CommonJS-style script
  const execCode = fs.readFileSync(execPath, 'utf8');
  // wasm_exec.js is a classic script assigning `globalThis.Go` or `global.Go`
  const scriptModule = { exports: {} as Record<string, unknown> };
  // Provide minimal CommonJS/browser shims
  const fn = new Function(
    'require',
    'module',
    'exports',
    'global',
    'globalThis',
    execCode + '\n;module.exports.Go = (typeof Go !== "undefined" ? Go : globalThis.Go);',
  );
  fn(require, scriptModule, scriptModule.exports, g, g);
  const GoCtor = (scriptModule.exports.Go || g.Go) as new () => {
    importObject: WebAssembly.Imports;
    run: (inst: WebAssembly.Instance) => Promise<void>;
  };
  if (!GoCtor) {
    throw new Error('Failed to load Go class from wasm_exec.js');
  }

  const go = new GoCtor();
  const wasmBytes = fs.readFileSync(wasmPath);
  const result = await WebAssembly.instantiate(wasmBytes, go.importObject);
  // Don't await forever — run in background; Go main blocks on select{}
  go.run(result.instance);

  // Wait briefly for JS callbacks to register
  await new Promise((r) => setTimeout(r, 50));

  const api: WasmSignerApi = {
    sqDeriveFromSeedHex: (s) => g.sqDeriveFromSeedHex(s),
    sqDeriveFromSigHex: (s) => g.sqDeriveFromSigHex(s),
    sqGetAddress: () => g.sqGetAddress(),
    sqGetPublicKeyB64: () => g.sqGetPublicKeyB64(),
    sqSignTransfer: (j) => g.sqSignTransfer(j),
    sqSignCall: (j) => g.sqSignCall(j),
    sqSignDeploy: (j) => g.sqSignDeploy(j),
    sqSignX402: (j) => g.sqSignX402(j),
    sqLock: () => g.sqLock(),
    sqIsUnlocked: () => g.sqIsUnlocked(),
    sqDomainMessage: () => g.sqDomainMessage(),
  };
  return api;
}

async function loadInBrowser(wasmUrl?: string, execUrl?: string): Promise<WasmSignerApi> {
  const g = globalThis as unknown as Record<string, unknown>;
  if (!g.Go) {
    const src = execUrl || '/wasm_exec.js';
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement('script');
      script.src = src;
      script.onload = () => resolve();
      script.onerror = () => reject(new Error(`Failed to load ${src}`));
      document.head.appendChild(script);
    });
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const GoCtor = (g as any).Go;
  const go = new GoCtor();
  const url = wasmUrl || '/sqc_signer.wasm';
  const resp = await fetch(url);
  const bytes = await resp.arrayBuffer();
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance);
  await new Promise((r) => setTimeout(r, 50));

  return {
    sqDeriveFromSeedHex: (s) => (g as any).sqDeriveFromSeedHex(s),
    sqDeriveFromSigHex: (s) => (g as any).sqDeriveFromSigHex(s),
    sqGetAddress: () => (g as any).sqGetAddress(),
    sqGetPublicKeyB64: () => (g as any).sqGetPublicKeyB64(),
    sqSignTransfer: (j) => (g as any).sqSignTransfer(j),
    sqSignCall: (j) => (g as any).sqSignCall(j),
    sqSignDeploy: (j) => (g as any).sqSignDeploy(j),
    sqSignX402: (j) => (g as any).sqSignX402(j),
    sqLock: () => (g as any).sqLock(),
    sqIsUnlocked: () => (g as any).sqIsUnlocked(),
    sqDomainMessage: () => (g as any).sqDomainMessage(),
  };
}

export async function loadWasmSigner(opts?: {
  wasmUrl?: string;
  execUrl?: string;
}): Promise<WasmSignerApi> {
  if (loaded && loadPromise) return loadPromise;
  if (loadPromise) return loadPromise;

  const isNode =
    typeof process !== 'undefined' &&
    process.versions != null &&
    process.versions.node != null;

  loadPromise = (isNode ? loadInNode() : loadInBrowser(opts?.wasmUrl, opts?.execUrl)).then(
    (api) => {
      loaded = true;
      return api;
    },
  );
  return loadPromise;
}

export function parseWasmJson<T>(result: string | { error: string }): T {
  const s = unwrap(result);
  if (typeof s !== 'string') {
    throw new Error('unexpected WASM result type');
  }
  return JSON.parse(s) as T;
}

export { unwrap };

// silence unused import in some bundlers
void pathToFileURL;
