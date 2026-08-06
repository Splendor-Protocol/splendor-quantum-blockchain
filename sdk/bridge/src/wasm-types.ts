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
  /** Optional SHA-256 hex of sqc_signer.wasm — fail closed on mismatch when set. */
  wasmSha256?: string;
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
  while (typeof (g as { sqCreateSession?: unknown }).sqCreateSession !== 'function') {
    if (Date.now() - start > maxMs) {
      throw BridgeError.wasm('WASM signer failed to register JS callbacks in time');
    }
    await new Promise((r) => setTimeout(r, 20));
  }
}

/** SHA-256 hex of ArrayBuffer (Web Crypto). */
export async function sha256Hex(buf: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', buf);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/** Fail closed when expected hash is set and does not match. */
export async function assertWasmSha256(
  bytes: ArrayBuffer,
  expectedHex?: string,
): Promise<void> {
  if (!expectedHex) return;
  const want = expectedHex.trim().toLowerCase().replace(/^0x/, '');
  if (!/^[0-9a-f]{64}$/.test(want)) {
    throw BridgeError.wasm(`invalid wasmSha256 pin (want 64 hex chars): ${expectedHex}`);
  }
  const got = (await sha256Hex(bytes)).toLowerCase();
  if (got !== want) {
    throw BridgeError.wasm(`WASM integrity check failed: expected ${want}, got ${got}`);
  }
}
