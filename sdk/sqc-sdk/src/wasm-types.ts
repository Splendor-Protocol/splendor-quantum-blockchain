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
  /** Sign the on-chain TxX402 that settles a payment through consensus. */
  sqSignX402Settlement: (sessionId: string, paramsJson: string) => string | { error: string };
  /** Sign an arbitrary message with the session key (Connect v2 challenge). */
  sqSignMessage: (
    sessionId: string,
    message: string,
  ) => { address: string; publicKeyB64: string; signatureB64: string } | { error: string };
  /** Verify an ML-DSA-65 signature over a message — pure, no session. */
  sqVerifyMessage: (
    publicKeyB64: string,
    message: string,
    signatureB64: string,
  ) => { valid: boolean } | { error: string };
  sqLock: (sessionId: string) => { ok: boolean };
  sqIsUnlocked: (sessionId: string) => boolean;
  sqDomainMessage: () => string;
  sqSignPermit: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignOrder: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignCancel: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignIncrementNonce: (sessionId: string, paramsJson: string) => string | { error: string };
  sqSignBridgeClaim: (sessionId: string, paramsJson: string) => string | { error: string };
  sqGetModuleRouterAddress: () => string | { error: string };
  sqSetChainID: (chainId: string) => { ok: boolean } | { error: string };
  /** SLH-DSA-SHA2-128s public key (hex) of the session, derived from its seed. */
  sqGetSLHPublicKeyHex: (sessionId: string) => string | { error: string };
  /** Dual-sign (ML-DSA-65 + SLH-DSA) an SQVL instruction under CtxVerify. */
  sqSignVerifyInstruction: (sessionId: string, instructionJson: string) => string | { error: string };
  /** Dual-sign an operator attestation bound to one instance + instruction digest. */
  sqSignVerifyAttestation: (sessionId: string, paramsJson: string) => string | { error: string };
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
  /** Optional SHA-256 hex of wasm_exec.js — fail closed on mismatch when set. */
  execSha256?: string;
};

/**
 * Every global the signer registers — the single source of truth for what a
 * loaded binary must provide. The binder below checked exactly one of these,
 * sqCreateSession, and wrapped the other twenty-two blind, so a binary built
 * before an export existed loaded happily and threw "not a function" at the
 * call site instead of at load. That is how a signer with no sqSignBridgeClaim
 * reached main while every test passed.
 */
export const WASM_EXPORTS = [
  'sqCreateSession',
  'sqDestroySession',
  'sqDeriveFromSeedHex',
  'sqDeriveFromSigHex',
  'sqGetAddress',
  'sqGetPublicKeyB64',
  'sqSignTransfer',
  'sqSignCall',
  'sqSignDeploy',
  'sqSignX402',
  'sqSignX402Settlement',
  'sqSignMessage',
  'sqVerifyMessage',
  'sqLock',
  'sqIsUnlocked',
  'sqDomainMessage',
  'sqSignPermit',
  'sqSignOrder',
  'sqSignCancel',
  'sqSignIncrementNonce',
  'sqSignBridgeClaim',
  'sqGetModuleRouterAddress',
  'sqSetChainID',
  'sqGetSLHPublicKeyHex',
  'sqSignVerifyInstruction',
  'sqSignVerifyAttestation',
] as const satisfies readonly (keyof WasmSignerApi)[];

// Adding a method to WasmSignerApi without listing it above fails to compile,
// so the list cannot fall behind the interface the way the binary fell behind
// the source.
type UnlistedExport = Exclude<keyof WasmSignerApi, (typeof WASM_EXPORTS)[number]>;
const _everyExportIsListed: UnlistedExport extends never ? true : never = true;
void _everyExportIsListed;

/** Bind session-aware wrappers from global Go exports. */
export function bindWasmApi(g: Record<string, unknown>): WasmSignerApi {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const x = g as any;
  const missing = WASM_EXPORTS.filter((name) => typeof x[name] !== 'function');
  if (missing.length > 0) {
    throw BridgeError.wasm(
      `WASM signer is missing ${missing.length} of ${WASM_EXPORTS.length} exports ` +
        `(${missing.join(', ')}). The binary does not match the source it claims to ` +
        'be built from: rebuild it with sdk/sqc-sdk/wasm/build.sh and re-pin with ' +
        'npm run sync:config.',
    );
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
    sqSignX402Settlement: (id, j) => x.sqSignX402Settlement(id, j),
    sqSignMessage: (id, m) => x.sqSignMessage(id, m),
    sqVerifyMessage: (p, m, s) => x.sqVerifyMessage(p, m, s),
    sqLock: (id) => x.sqLock(id),
    sqIsUnlocked: (id) => x.sqIsUnlocked(id),
    sqDomainMessage: () => x.sqDomainMessage(),
    sqSignPermit: (id, j) => x.sqSignPermit(id, j),
    sqSignOrder: (id, j) => x.sqSignOrder(id, j),
    sqSignCancel: (id, j) => x.sqSignCancel(id, j),
    sqSignIncrementNonce: (id, j) => x.sqSignIncrementNonce(id, j),
    sqSignBridgeClaim: (id, j) => x.sqSignBridgeClaim(id, j),
    sqGetModuleRouterAddress: () => x.sqGetModuleRouterAddress(),
    sqSetChainID: (c) => x.sqSetChainID(c),
    sqGetSLHPublicKeyHex: (id) => x.sqGetSLHPublicKeyHex(id),
    sqSignVerifyInstruction: (id, j) => x.sqSignVerifyInstruction(id, j),
    sqSignVerifyAttestation: (id, j) => x.sqSignVerifyAttestation(id, j),
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
  label = 'WASM',
): Promise<void> {
  if (!expectedHex) return;
  const want = expectedHex.trim().toLowerCase().replace(/^0x/, '');
  if (!/^[0-9a-f]{64}$/.test(want)) {
    throw BridgeError.wasm(`invalid ${label} pin (want 64 hex chars): ${expectedHex}`);
  }
  const got = (await sha256Hex(bytes)).toLowerCase();
  if (got !== want) {
    throw BridgeError.wasm(`${label} integrity check failed: expected ${want}, got ${got}`);
  }
}
