/** Node.js WASM loader (uses fs). */
import { createRequire } from 'module';
import { fileURLToPath } from 'url';
import path from 'path';
import fs from 'fs';
import {
  assertWasmSha256,
  bindWasmApi,
  sha256Hex,
  waitForWasmReady,
  type LoadWasmOpts,
  type WasmSignerApi,
} from './wasm-types.js';
import { BridgeError } from './errors.js';

function normalizePin(hex: string): string {
  return hex.trim().toLowerCase().replace(/^0x/, '');
}

function recordedWasmHash(g: Record<string, unknown>): string {
  const v = g.__sqcWasmSha256;
  return typeof v === 'string' ? normalizePin(v) : '';
}

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
  candidates.push(path.resolve(process.cwd(), 'sdk/sqc-sdk/wasm/dist'));
  candidates.push(path.resolve(process.cwd(), 'wasm/dist'));
  for (const c of candidates) {
    if (fs.existsSync(path.join(c, 'sqc_signer.wasm'))) return c;
  }
  throw BridgeError.wasm(
    'sqc_signer.wasm not found. Run sdk/sqc-sdk/wasm/build.ps1 (or build.sh) first.',
  );
}

/**
 * When WASM is already in-process and a pin is set:
 * - if `__sqcWasmSha256` matches pin → bind
 * - if recorded hash is missing → re-read file bytes, verify pin, then set recorded hash
 *   (assumes in-memory module matches the on-disk file for this process)
 * - if recorded hash mismatches pin → refuse
 */
async function assertAlreadyLoadedPin(
  g: Record<string, unknown>,
  wasmPath: string,
  pinRaw: string,
): Promise<void> {
  const pin = normalizePin(pinRaw);
  const recorded = recordedWasmHash(g);
  if (recorded) {
    if (recorded !== pin) {
      throw BridgeError.wasm(
        `WASM already loaded with hash ${recorded}, which does not match pin ${pin}`,
      );
    }
    return;
  }
  const wasmBytes = fs.readFileSync(wasmPath);
  const ab = wasmBytes.buffer.slice(
    wasmBytes.byteOffset,
    wasmBytes.byteOffset + wasmBytes.byteLength,
  );
  await assertWasmSha256(ab, pinRaw);
  g.__sqcWasmSha256 = pin;
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
      await assertAlreadyLoadedPin(g, wasmPath, opts.wasmSha256);
    }
    if (opts?.execSha256) {
      const pin = normalizePin(opts.execSha256);
      const recorded =
        typeof g.__sqcExecSha256 === 'string' ? normalizePin(g.__sqcExecSha256) : '';
      if (recorded && recorded !== pin) {
        throw BridgeError.wasm(
          `wasm_exec.js already loaded with hash ${recorded}, which does not match pin ${pin}`,
        );
      }
      if (!recorded) {
        const execBytes = fs.readFileSync(execPath);
        const ab = execBytes.buffer.slice(
          execBytes.byteOffset,
          execBytes.byteOffset + execBytes.byteLength,
        );
        await assertWasmSha256(ab, opts.execSha256, 'wasm_exec.js');
        g.__sqcExecSha256 = pin;
      }
    }
    return bindWasmApi(g);
  }

  const execCode = fs.readFileSync(execPath, 'utf8');
  const execAb = new TextEncoder().encode(execCode).buffer;
  await assertWasmSha256(execAb, opts?.execSha256, 'wasm_exec.js');
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
  await assertWasmSha256(ab, opts?.wasmSha256, 'WASM');
  const result = await WebAssembly.instantiate(wasmBytes, go.importObject);
  go.run(result.instance);
  await waitForWasmReady(g);
  const pin = opts?.wasmSha256
    ? normalizePin(opts.wasmSha256)
    : (await sha256Hex(ab)).toLowerCase();
  g.__sqcWasmSha256 = pin;
  g.__sqcExecSha256 = opts?.execSha256
    ? normalizePin(opts.execSha256)
    : (await sha256Hex(execAb)).toLowerCase();
  return bindWasmApi(g);
}
