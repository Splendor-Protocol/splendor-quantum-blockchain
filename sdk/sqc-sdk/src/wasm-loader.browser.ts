/** Browser WASM loader (fetch only — no Node builtins). */
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

function recordedExecHash(g: Record<string, unknown>): string {
  const v = g.__sqcExecSha256;
  return typeof v === 'string' ? normalizePin(v) : '';
}

async function loadAndPinExecJs(
  g: Record<string, unknown>,
  opts?: LoadWasmOpts,
): Promise<void> {
  if ((g as { Go?: unknown }).Go) {
    if (opts?.execSha256) {
      const pin = normalizePin(opts.execSha256);
      const recorded = recordedExecHash(g);
      if (!recorded || recorded !== pin) {
        throw BridgeError.wasm(
          'wasm_exec.js already loaded but execSha256 pin does not match (or was never recorded)',
        );
      }
    }
    return;
  }
  const src = opts?.execUrl || '/wasm_exec.js';
  const resp = await fetch(src);
  if (!resp.ok) {
    throw BridgeError.wasm(`Failed to fetch ${src}: ${resp.status}`);
  }
  const text = await resp.text();
  const bytes = new TextEncoder().encode(text).buffer;
  await assertWasmSha256(bytes, opts?.execSha256, 'wasm_exec.js');
  // eslint-disable-next-line no-new-func
  const run = new Function(`${text}\n;globalThis.Go = Go;`);
  run();
  if (opts?.execSha256) {
    g.__sqcExecSha256 = normalizePin(opts.execSha256);
  } else {
    g.__sqcExecSha256 = (await sha256Hex(bytes)).toLowerCase();
  }
}

export async function loadWasmInBrowser(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  const g = globalThis as unknown as Record<string, unknown>;

  if (typeof (g as { sqCreateSession?: unknown }).sqCreateSession === 'function') {
    if (opts?.wasmSha256) {
      const pin = normalizePin(opts.wasmSha256);
      const recorded = recordedWasmHash(g);
      if (!recorded || recorded !== pin) {
        throw BridgeError.wasm(
          'WASM already loaded but integrity pin does not match (or was never recorded). ' +
            'Reload the page with a matching wasmSha256, or clear the prior module.',
        );
      }
    }
    if (opts?.execSha256) {
      const pin = normalizePin(opts.execSha256);
      const recorded = recordedExecHash(g);
      if (!recorded || recorded !== pin) {
        throw BridgeError.wasm(
          'wasm_exec.js pin does not match recorded hash for already-loaded module',
        );
      }
    }
    return bindWasmApi(g);
  }

  await loadAndPinExecJs(g, opts);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const GoCtor = (g as any).Go;
  if (!GoCtor) {
    throw BridgeError.wasm('Go constructor missing after loading wasm_exec.js');
  }
  const go = new GoCtor();
  const url = opts?.wasmUrl || '/sqc_signer.wasm';
  const resp = await fetch(url);
  if (!resp.ok) {
    throw BridgeError.wasm(`Failed to fetch WASM: ${resp.status}`);
  }
  const bytes = await resp.arrayBuffer();
  await assertWasmSha256(bytes, opts?.wasmSha256, 'WASM');
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance);
  await waitForWasmReady(g);
  const pin = opts?.wasmSha256 ? normalizePin(opts.wasmSha256) : (await sha256Hex(bytes)).toLowerCase();
  g.__sqcWasmSha256 = pin;
  return bindWasmApi(g);
}
