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

/** `sha256-…` SRI token for a hex digest. */
export function sriFromHex(hex: string): string {
  const clean = normalizePin(hex);
  if (!/^[0-9a-f]{64}$/.test(clean)) throw BridgeError.wasm('execSha256 must be a 64-character hex SHA-256 digest');
  let bin = '';
  for (let i = 0; i < clean.length; i += 2) bin += String.fromCharCode(parseInt(clean.slice(i, i + 2), 16));
  return `sha256-${btoa(bin)}`;
}

function canInjectScript(): boolean {
  return typeof document !== 'undefined' && !!document.head && typeof document.createElement === 'function';
}

/**
 * Runs wasm_exec.js as a classic script whose bytes the browser checks against the pin before it
 * executes, then takes the `Go` class it defines.
 */
async function loadExecWithScript(g: Record<string, unknown>, src: string, execSha256: string): Promise<unknown> {
  const integrity = sriFromHex(execSha256);
  const before = (g as { Go?: unknown }).Go;
  await new Promise<void>((resolve, reject) => {
    const el = document.createElement('script');
    el.src = src;
    el.integrity = integrity;
    el.crossOrigin = 'anonymous';
    el.async = true;
    el.onload = () => resolve();
    el.onerror = () =>
      reject(BridgeError.wasm(`Failed to load ${src}: network error, blocked by CSP, or its SHA-256 does not match execSha256`));
    document.head.appendChild(el);
  });
  const GoCtor = (g as { Go?: unknown }).Go;
  if (typeof GoCtor !== 'function' || GoCtor === before) {
    throw BridgeError.wasm(`${src} loaded but did not define the Go runtime`);
  }
  return GoCtor;
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
  // Pinned: a <script> with Subresource Integrity, so a strict CSP (no 'unsafe-eval') still works.
  if (opts?.execSha256 && canInjectScript()) {
    const GoCtor = await loadExecWithScript(g, src, opts.execSha256);
    ownGo = GoCtor;
    boundExecHash = normalizePin(opts.execSha256);
    return GoCtor;
  }
  if (opts?.wasmSha256 && !opts.execSha256 && !opts.allowUnpinnedExec) {
    throw BridgeError.wasm(
      'execSha256 is required whenever wasmSha256 is set: an unverified wasm_exec.js could hand the signer any imports',
    );
  }
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
