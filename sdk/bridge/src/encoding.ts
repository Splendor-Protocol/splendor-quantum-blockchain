/** Encoding helpers — browser-safe (no Buffer). */
import { strip0x } from './derive.js';

export const MIN_FEE = '1000';

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

export function hexToBytes(hex: string): Uint8Array {
  const s = strip0x(hex);
  if (s.length % 2 !== 0) {
    throw new Error('hex string must have even length');
  }
  if (!/^[0-9a-fA-F]*$/.test(s)) {
    throw new Error('invalid hex string');
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

export function assertFeeAtLeastMin(fee: string | undefined): string {
  const f = fee || MIN_FEE;
  try {
    if (BigInt(f) < BigInt(MIN_FEE)) {
      throw new Error(`fee ${f} below minimum ${MIN_FEE}`);
    }
  } catch (e) {
    if (e instanceof Error && e.message.includes('below minimum')) throw e;
    throw new Error(`invalid fee: ${f}`);
  }
  return f;
}
