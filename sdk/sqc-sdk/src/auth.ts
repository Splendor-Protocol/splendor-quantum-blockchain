/**
 * Server sign-in kit (`@splendor/sqc-sdk/auth`).
 *
 * Flow: the server calls `issueNonce` and stores it, the client signs a Connect v2 message
 * carrying that nonce, then the server calls `verifySignIn`, which checks the message,
 * binds the address to the public key, verifies the ML-DSA signature and burns the nonce.
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { bytesToHex } from './encoding.js';
import { isValidAddress } from './address.js';
import { loadWasmSigner, unwrap, type LoadWasmOpts } from './wasm-loader.js';
import { checkAuthMessage, type AuthCheckReason, type ParsedAuthMessage } from './auth-message.js';

export {
  AUTH_PREFIX,
  createAuthMessage,
  parseAuthMessage,
  checkAuthMessage,
  authCheckMessage,
  type AuthMessageParams,
  type ParsedAuthMessage,
  type AuthCheckReason,
  type AuthCheckResult,
  type AuthCheckOptions,
} from './auth-message.js';

/** 128-bit random nonce (hex) from the platform CSPRNG. */
export function issueNonce(bytes = 16): string {
  if (!Number.isSafeInteger(bytes) || bytes < 16) throw new RangeError('issueNonce: use at least 16 bytes');
  const buf = new Uint8Array(bytes);
  globalThis.crypto.getRandomValues(buf);
  return bytesToHex(buf);
}

/**
 * Single-use nonce storage. `consume` must be atomic: it returns true only for the first
 * caller holding an unexpired nonce. Back it with Redis `SET NX` / `GETDEL` or a DB row in production.
 */
export interface NonceStore {
  put(nonce: string, expiresAtSec: number): void | Promise<void>;
  consume(nonce: string, nowSec: number): boolean | Promise<boolean>;
}

export type MemoryNonceStore = NonceStore & {
  /** Issue, store and return a new nonce valid for `ttlSec`. */
  issue(ttlSec?: number): string;
  readonly size: number;
};

/** In-memory `NonceStore` for a single server process (tests, demos, small apps). */
export function createMemoryNonceStore(opts?: { ttlSec?: number; maxEntries?: number }): MemoryNonceStore {
  const ttl = opts?.ttlSec ?? 600;
  const max = opts?.maxEntries ?? 10_000;
  const entries = new Map<string, number>();
  const prune = (now: number) => {
    for (const [k, exp] of entries) if (exp <= now) entries.delete(k);
    while (entries.size > max) entries.delete(entries.keys().next().value as string);
  };
  const store: MemoryNonceStore = {
    put(nonce, expiresAtSec) {
      prune(Math.floor(Date.now() / 1000));
      entries.set(nonce, expiresAtSec);
    },
    consume(nonce, nowSec) {
      const exp = entries.get(nonce);
      entries.delete(nonce);
      return exp != null && exp > nowSec;
    },
    issue(ttlSec = ttl) {
      const nonce = issueNonce();
      store.put(nonce, Math.floor(Date.now() / 1000) + ttlSec);
      return nonce;
    },
    get size() {
      return entries.size;
    },
  };
  return store;
}

function base64ToBytes(b64: string): Uint8Array | null {
  if (typeof b64 !== 'string' || !/^[A-Za-z0-9+/]+={0,2}$/.test(b64) || b64.length % 4 !== 0) return null;
  if (typeof atob === 'function') {
    const bin = atob(b64);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i += 1) out[i] = bin.charCodeAt(i);
    return out;
  }
  return new Uint8Array(Buffer.from(b64, 'base64'));
}

/** `SQ:` + hex(SHA3-256(public key bytes)) — the chain's address rule for ML-DSA keys. */
export function addressFromPublicKey(publicKeyB64: string): string | null {
  const bytes = base64ToBytes(publicKeyB64);
  if (!bytes?.length) return null;
  return `SQ:${bytesToHex(sha3_256(bytes))}`;
}

export type SignatureVerifier = (publicKeyB64: string, message: string, signatureB64: string) => Promise<boolean>;

/** Default verifier: the pinned WASM signer's pure ML-DSA-65 verify (no session, no key). */
export function wasmSignatureVerifier(wasm?: LoadWasmOpts): SignatureVerifier {
  return async (publicKeyB64, message, signatureB64) => {
    const api = await loadWasmSigner(wasm);
    const r = unwrap(api.sqVerifyMessage(publicKeyB64, message, signatureB64)) as { valid: boolean };
    return Boolean(r.valid);
  };
}

export type VerifySignInOptions = {
  message: string;
  signatureB64: string;
  publicKeyB64: string;
  /** The address the client claims to be. Must derive from `publicKeyB64`. */
  address: string;
  /** This server's origin, e.g. https://app.example.com */
  domain: string;
  /** Required: the nonce in the message must be issued by and consumed from this store. */
  nonceStore: NonceStore;
  /** Expected chain id; when set, the message must carry a `Chain ID` line that matches. */
  chainId?: string;
  now?: number;
  maxAgeSec?: number;
  maxSkewSec?: number;
  /** Signature check; defaults to the WASM ML-DSA verifier. */
  verifier?: SignatureVerifier;
  wasm?: LoadWasmOpts;
};

export type SignInFailure = AuthCheckReason | 'missing-nonce' | 'bad-address' | 'key-mismatch' | 'bad-signature' | 'nonce-used';

export type VerifySignInResult =
  | { ok: true; address: string; parsed: ParsedAuthMessage }
  | { ok: false; reason: SignInFailure };

/**
 * Verify a Connect v2 sign-in end to end. Returns `{ ok: true, address }` only when the message
 * is well-formed for this domain and time window, names this address (if it names one), the
 * address derives from the public key, the signature is valid, and the nonce was unused.
 */
export async function verifySignIn(opts: VerifySignInOptions): Promise<VerifySignInResult> {
  const now = opts.now ?? Math.floor(Date.now() / 1000);
  if (!isValidAddress(opts.address)) return { ok: false, reason: 'bad-address' };
  const check = checkAuthMessage(opts.message, {
    domain: opts.domain,
    now,
    maxAgeSec: opts.maxAgeSec,
    maxSkewSec: opts.maxSkewSec,
    expectedAddress: opts.address,
    expectedChainId: opts.chainId,
  });
  if (!check.ok) return check;
  if (!check.parsed.nonce) return { ok: false, reason: 'missing-nonce' };

  const derived = addressFromPublicKey(opts.publicKeyB64);
  if (!derived || derived.toLowerCase() !== opts.address.toLowerCase()) {
    return { ok: false, reason: 'key-mismatch' };
  }

  const verify = opts.verifier ?? wasmSignatureVerifier(opts.wasm);
  let valid = false;
  try {
    valid = await verify(opts.publicKeyB64, opts.message, opts.signatureB64);
  } catch {
    valid = false;
  }
  if (!valid) return { ok: false, reason: 'bad-signature' };

  if (!(await opts.nonceStore.consume(check.parsed.nonce, now))) return { ok: false, reason: 'nonce-used' };
  return { ok: true, address: derived, parsed: check.parsed };
}
