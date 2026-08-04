/**
 * Shared WASM signer API types and helpers (no Node/browser I/O).
 */
import { BridgeError } from './errors.js';

export type WasmSignerApi = {
  sqCreateSession: () => string;
  sqDestroySession: (sessionId: string) => { ok: boolean };
  sqDeriveFromSeedHex: (sessionId: string, seedHex: string) => string | { error: string };
  sqDeriveFromSigHex: (sessionId: string, sigHex: string) => string | { error: string };
  sqGetAddress: (sessionId: string) => string | { error: string };
  sqGetPublicKeyB64: (sessionId: string) => string | { error: string };
  sqSignTransfer: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignCall: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignDeploy: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignX402: (sessionId: string, paramsJson: string) => string | { error: string };
  sqLock: (sessionId: string) => { ok: boolean };
  sqIsUnlocked: (sessionId: string) => boolean;
  sqDomainMessage: () => string;
};

export function unwrap<T>(v: T | { error: string }): T {
  if (v && typeof v === 'object' && 'error' in (v as object)) {
    throw BridgeError.wasm((v as { error: string }).error);
  }
  return v as T;
}

export function parseWasmJson<T>(result: string | { error: string }): T {
  const s = unwrap(result);
  if (typeof s !== 'string') {
    throw BridgeError.wasm('unexpected WASM result type');
  }
  return JSON.parse(s) as T;
}

export type LoadWasmOpts = {
  wasmUrl?: string;
  execUrl?: string;
};

/** Bind session-aware wrappers from global Go exports. */
export function bindWasmApi(g: Record<string, unknown>): WasmSignerApi {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const x = g as any;
  if (typeof x.sqCreateSession !== 'function') {
    throw BridgeError.wasm('WASM signer not ready (sqCreateSession missing)');
  }
  return {
    sqCreateSession: () => x.sqCreateSession(),
    sqDestroySession: (id) => x.sqDestroySession(id),
    sqDeriveFromSeedHex: (id, s) => x.sqDeriveFromSeedHex(id, s),
    sqDeriveFromSigHex: (id, s) => x.sqDeriveFromSigHex(id, s),
    sqGetAddress: (id) => x.sqGetAddress(id),
    sqGetPublicKeyB64: (id) => x.sqGetPublicKeyB64(id),
    sqSignTransfer: (id, j) => x.sqSignTransfer(id, j),
    sqSignCall: (id, j) => x.sqSignCall(id, j),
    sqSignDeploy: (id, j) => x.sqSignDeploy(id, j),
    sqSignX402: (id, j) => x.sqSignX402(id, j),
    sqLock: (id) => x.sqLock(id),
    sqIsUnlocked: (id) => x.sqIsUnlocked(id),
    sqDomainMessage: () => x.sqDomainMessage(),
  };
}

export async function waitForWasmReady(
  g: Record<string, unknown>,
  maxMs = 5000,
): Promise<void> {
  const start = Date.now();
  while (typeof (g as any).sqCreateSession !== 'function') {
    if (Date.now() - start > maxMs) {
      throw BridgeError.wasm('WASM signer failed to register JS callbacks in time');
    }
    await new Promise((r) => setTimeout(r, 20));
  }
}
