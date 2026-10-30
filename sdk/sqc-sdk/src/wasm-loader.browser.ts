/** Browser WASM loader (fetch only — no Node builtins). */
import {
  assertWasmSha256,
  bindWasmApi,
  detachWasmExports,
  sha256Hex,
  waitForWasmReady,
  type LoadWasmOpts,
  type WasmSignerApi,
} from './wasm-types.js';
import { BridgeError } from './errors.js';

/**
 * Loader state lives in this module, not on globalThis: a script that runs first cannot
 * fake the recorded hashes or pre-register signer functions for us to bind.
 */
let boundApi: WasmSignerApi | null = null;
let boundWasmHash = '';
let boundExecHash = '';
let ownGo: unknown = null;

function normalizePin(hex: string): string {
  return hex.trim().toLowerCase().replace(/^0x/, '');
}

function assertPinMatches(recorded: string, pin: string | undefined, label: string): void {
  if (!pin) return;
  if (!recorded || recorded !== normalizePin(pin)) {
    throw BridgeError.wasm(
      `${label} already loaded but its integrity pin does not match. Reload the page with a matching pin.`,
    );
  }
}

async function loadAndPinExecJs(g: Record<string, unknown>, opts?: LoadWasmOpts): Promise<unknown> {
  const existing = (g as { Go?: unknown }).Go;
  if (existing && existing === ownGo) {
    assertPinMatches(boundExecHash, opts?.execSha256, 'wasm_exec.js');
    return existing;
  }
  if (existing && !opts?.execSha256) {
    // A pinned .wasm is only as safe as the glue that instantiates it: a foreign Go we cannot verify
    // could hand the signer any imports it likes.
    if (opts?.wasmSha256) {
      throw BridgeError.wasm(
        'wasm_exec.js was loaded by another script and cannot be verified; pass execSha256 alongside wasmSha256',
      );
    }
    // Unpinned (local development): an app may load wasm_exec.js with a <script> tag.
    return existing;
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
  const GoCtor = new Function(`${text}\n;return Go;`)();
  ownGo = GoCtor;
  boundExecHash = opts?.execSha256 ? normalizePin(opts.execSha256) : (await sha256Hex(bytes)).toLowerCase();
  return GoCtor;
}

export async function loadWasmInBrowser(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  const g = globalThis as unknown as Record<string, unknown>;

  if (boundApi) {
    assertPinMatches(boundWasmHash, opts?.wasmSha256, 'WASM');
    assertPinMatches(boundExecHash, opts?.execSha256, 'wasm_exec.js');
    return boundApi;
  }

  if (typeof (g as { sqCreateSession?: unknown }).sqCreateSession === 'function') {
    throw BridgeError.wasm(
      'Signer functions are already registered on globalThis by another script; refusing to bind them.',
    );
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const GoCtor = (await loadAndPinExecJs(g, opts)) as any;
  if (typeof GoCtor !== 'function') {
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
  const api = bindWasmApi(detachWasmExports(g));
  boundWasmHash = opts?.wasmSha256 ? normalizePin(opts.wasmSha256) : (await sha256Hex(bytes)).toLowerCase();
  boundApi = api;
  return api;
}

/** Test hook: forget the bound signer (the Go runtime stays resident). */
export function resetBrowserWasmState(): void {
  boundApi = null;
  boundWasmHash = '';
  boundExecHash = '';
  ownGo = null;
}
