/** Browser WASM loader (fetch only — no Node builtins). */
import {
  assertWasmSha256,
  bindWasmApi,
  waitForWasmReady,
  type LoadWasmOpts,
  type WasmSignerApi,
} from './wasm-types.js';
import { BridgeError } from './errors.js';

export async function loadWasmInBrowser(opts?: LoadWasmOpts): Promise<WasmSignerApi> {
  const g = globalThis as unknown as Record<string, unknown>;

  if (typeof (g as { sqCreateSession?: unknown }).sqCreateSession === 'function') {
    return bindWasmApi(g);
  }

  if (!(g as { Go?: unknown }).Go) {
    const src = opts?.execUrl || '/wasm_exec.js';
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement('script');
      script.src = src;
      script.onload = () => resolve();
      script.onerror = () => reject(BridgeError.wasm(`Failed to load ${src}`));
      document.head.appendChild(script);
    });
  }

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
  await assertWasmSha256(bytes, opts?.wasmSha256);
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance);
  await waitForWasmReady(g);
  return bindWasmApi(g);
}
