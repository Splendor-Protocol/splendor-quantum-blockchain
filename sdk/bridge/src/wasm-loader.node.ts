/** Node.js WASM loader (uses fs). */
import { createRequire } from 'module';
import { fileURLToPath } from 'url';
import path from 'path';
import fs from 'fs';
import {
  assertWasmSha256,
  bindWasmApi,
  waitForWasmReady,
  type LoadWasmOpts,
  type WasmSignerApi,
} from './wasm-types.js';
import { BridgeError } from './errors.js';

function resolveDistDir(): string {
  const candidates: string[] = [];
  try {
    const here = path.dirname(fileURLToPath(import.meta.url));
    candidates.push(path.resolve(here, '../wasm/dist'));
    candidates.push(path.resolve(here, '../../wasm/dist'));
    candidates.push(path.resolve(here, '../dist/wasm'));
  } catch {
    /* ignore */
  }
  candidates.push(path.resolve(process.cwd(), 'sdk/bridge/wasm/dist'));
  candidates.push(path.resolve(process.cwd(), 'wasm/dist'));
  for (const c of candidates) {
    if (fs.existsSync(path.join(c, 'sqc_signer.wasm'))) return c;
  }
  throw BridgeError.wasm(
    'sqc_signer.wasm not found. Run sdk/bridge/wasm/build.ps1 (or build.sh) first.',
  );
}

export async function loadWasmInNode(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  const dist = resolveDistDir();
  const wasmPath =
    opts?.wasmUrl && !opts.wasmUrl.startsWith('http') && !opts.wasmUrl.startsWith('/')
      ? path.resolve(opts.wasmUrl)
      : path.join(dist, 'sqc_signer.wasm');
  const execPath =
    opts?.execUrl && !opts.execUrl.startsWith('http') && !opts.execUrl.startsWith('/')
      ? path.resolve(opts.execUrl)
      : path.join(dist, 'wasm_exec.js');

  const require = createRequire(import.meta.url);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g: any = globalThis as any;

  if (!g.crypto?.getRandomValues) {
    const nodeCrypto = await import('crypto');
    g.crypto = {
      getRandomValues: (arr: Uint8Array) => nodeCrypto.webcrypto.getRandomValues(arr),
      subtle: nodeCrypto.webcrypto.subtle,
    };
  } else if (!g.crypto.subtle) {
    const nodeCrypto = await import('crypto');
    g.crypto.subtle = nodeCrypto.webcrypto.subtle;
  }

  if (typeof g.sqCreateSession === 'function') {
    if (opts?.wasmSha256) {
      const wasmBytes = fs.readFileSync(wasmPath);
      const ab = wasmBytes.buffer.slice(
        wasmBytes.byteOffset,
        wasmBytes.byteOffset + wasmBytes.byteLength,
      );
      await assertWasmSha256(ab, opts.wasmSha256);
    }
    return bindWasmApi(g);
  }

  const execCode = fs.readFileSync(execPath, 'utf8');
  const scriptModule = { exports: {} as Record<string, unknown> };
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
    throw BridgeError.wasm('Failed to load Go class from wasm_exec.js');
  }

  const go = new GoCtor();
  const wasmBytes = fs.readFileSync(wasmPath);
  const ab = wasmBytes.buffer.slice(
    wasmBytes.byteOffset,
    wasmBytes.byteOffset + wasmBytes.byteLength,
  );
  await assertWasmSha256(ab, opts?.wasmSha256);
  const result = await WebAssembly.instantiate(wasmBytes, go.importObject);
  go.run(result.instance);
  await waitForWasmReady(g);
  return bindWasmApi(g);
}
