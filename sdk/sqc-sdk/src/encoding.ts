/** Encoding helpers — browser-safe (no Buffer). Fee floor from sqc.networks.json. */
import { strip0x } from './derive.js';
import { getProtocolConfig } from './networks.js';
import { BridgeError } from './errors.js';

export const MIN_FEE = getProtocolConfig().minFee;

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Generate a 32-byte X402 payment nonce as hex.
 *
 * Single-use and tracked on-chain after settlement, so it must come from a CSPRNG:
 * a predictable nonce would let an observer pre-compute the replay-registry key
 * for a payment that has not happened yet.
 */
export function randomNonceHex(): string {
  const bytes = new Uint8Array(32);
  globalThis.crypto.getRandomValues(bytes);
  return bytesToHex(bytes);
}

export function hexToBytes(hex: string): Uint8Array {
  const s = strip0x(hex);
  if (s.length % 2 !== 0) {
    throw new BridgeError('UNKNOWN', 'hex string must have even length');
  }
  if (!/^[0-9a-fA-F]*$/.test(s)) {
    throw new BridgeError('UNKNOWN', 'invalid hex string');
  }
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

export function bytesToBase64(bytes: Uint8Array): string {
  if (typeof btoa !== 'undefined') {
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }
  // Node
  return Buffer.from(bytes).toString('base64');
}

export function isHexString(s: string): boolean {
  const h = strip0x(s);
  return h.length > 0 && h.length % 2 === 0 && /^[0-9a-fA-F]+$/.test(h);
}

export function isLikelyBase64(s: string): boolean {
  if (!s || typeof s !== 'string') return false;
  if (isHexString(s)) return false;
  return /^[A-Za-z0-9+/]+=*$/.test(s) && s.length % 4 === 0;
}

/** Classify calldata/bytecode for WASM sign params. */
export function encodePayload(
  data: Uint8Array | string,
): { calldata_hex?: string; calldata_b64?: string; bytecode_hex?: string; bytecode_b64?: string } {
  if (typeof data !== 'string') {
    return { calldata_b64: bytesToBase64(data), bytecode_b64: bytesToBase64(data) };
  }
  if (isHexString(data)) {
    return { calldata_hex: data, bytecode_hex: data };
  }
  return { calldata_b64: data, bytecode_b64: data };
}

/**
 * Validate a fee (base units) and return its canonical decimal string.
 * Only plain decimal integers are accepted; `maxFee` caps fat-fingered or injected fees.
 */
export function assertFeeAtLeastMin(fee: string | undefined, maxFee?: string): string {
  const f = fee == null || fee === '' ? MIN_FEE : String(fee).trim();
  if (!/^(0|[1-9]\d*)$/.test(f)) throw BridgeError.invalidFee('fee must be a decimal integer in base units');
  const value = BigInt(f);
  if (value < BigInt(MIN_FEE)) {
    throw BridgeError.invalidFee(`fee ${f} below minimum ${MIN_FEE}`);
  }
  if (maxFee != null && maxFee !== '') {
    let cap: bigint;
    try {
      cap = BigInt(maxFee);
    } catch {
      throw BridgeError.invalidFee('maxFee must be a decimal integer in base units');
    }
    if (value > cap) throw BridgeError.invalidFee(`fee ${f} exceeds maxFee ${cap}`);
  }
  return value.toString();
}
