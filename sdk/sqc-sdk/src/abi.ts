/**
 * QSol ABI helpers — mirrors vm.SelectorFromSignature / EncodeCall.
 * Selector = SHA3-256(signature)[0:4]; args packed as 32-byte big-endian words.
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { bytesToHex, hexToBytes } from './encoding.js';
import { strip0x } from './derive.js';
import { isValidAddress } from './address.js';
import { BridgeError } from './errors.js';

export type AbiArg = string | number | bigint | Uint8Array;

/**
 * Canonical QSol ABI spelling is `address` (compiler / I2).
 * `sqaddress` is a legacy alias that hashes as-written to a different selector.
 * Both are accepted; encoding never rewrites the signature string.
 */
export function assertQSolSignature(signature: string): void {
  if (typeof signature !== 'string' || signature.length === 0) {
    throw BridgeError.rpc('QSol ABI signature must be a non-empty string');
  }
}

/**
 * Opt-in rewrite of the legacy `sqaddress` alias to compiler-canonical `address`.
 * encodeFunctionData does not call this — already-deployed `sqaddress` selectors
 * stay `14cde9ab` when hashed as written.
 */
export function canonicalizeAbiSignature(signature: string): string {
  assertQSolSignature(signature);
  return signature.replace(/\bsqaddress\b/g, 'address');
}

/** SHA3-256(signature) first 4 bytes — same as Go vm.SelectorFromSignature. */
export function functionSelector(signature: string): Uint8Array {
  const hash = sha3_256(new TextEncoder().encode(signature));
  return hash.slice(0, 4);
}

export function functionSelectorHex(signature: string): string {
  return bytesToHex(functionSelector(signature));
}

function pad32(bytes: Uint8Array): Uint8Array {
  if (bytes.length > 32) {
    throw BridgeError.invalidAmount(`ABI word exceeds 32 bytes (${bytes.length})`);
  }
  const out = new Uint8Array(32);
  out.set(bytes, 32 - bytes.length);
  return out;
}

function encodeArg(arg: AbiArg): Uint8Array {
  if (typeof arg === 'bigint' || typeof arg === 'number') {
    const n = typeof arg === 'number' ? BigInt(arg) : arg;
    if (n < 0n) throw BridgeError.invalidAmount('ABI args must be non-negative');
    let hex = n.toString(16);
    if (hex.length % 2) hex = '0' + hex;
    return pad32(hexToBytes(hex === '0' ? '00' : hex));
  }
  if (typeof arg === 'string') {
    if (isValidAddress(arg)) {
      return pad32(hexToBytes(arg.slice(3)));
    }
    if (/^0x[0-9a-fA-F]{40}$/i.test(arg)) {
      throw BridgeError.invalidAddress(
        `EVM 0x addresses are not valid on Splendor PQ chain — use SQ: format (${arg})`,
      );
    }
    if (/^[0-9a-fA-F]{64}$/.test(arg)) {
      throw BridgeError.invalidAddress(
        `PQ addresses must use SQ: prefix, not raw 64-hex (${arg.slice(0, 16)}…)`,
      );
    }
    if (/^(0x)?[0-9a-fA-F]+$/.test(arg)) {
      return pad32(hexToBytes(arg));
    }
    // Treat as UTF-8 bytes (rare) — pack right-aligned
    return pad32(new TextEncoder().encode(arg).slice(0, 32));
  }
  return pad32(arg);
}

/**
 * encodeFunctionData('transfer(address,uint256)', [toAddr, amount])
 * Selector is SHA3-256 of the signature as written:
 *   transfer(address,uint256)  → 4b40e901  (compiler-canonical)
 *   transfer(sqaddress,uint256) → 14cde9ab  (legacy compatibility)
 */
export function encodeFunctionData(signature: string, args: AbiArg[] = []): Uint8Array {
  assertQSolSignature(signature);
  const sel = functionSelector(signature);
  const words = args.map(encodeArg);
  const out = new Uint8Array(4 + words.length * 32);
  out.set(sel, 0);
  words.forEach((w, i) => out.set(w, 4 + i * 32));
  return out;
}

export function encodeFunctionDataHex(signature: string, args: AbiArg[] = []): string {
  return '0x' + bytesToHex(encodeFunctionData(signature, args));
}

export function decodeUint256(data: Uint8Array | string, offset = 0): bigint {
  const bytes = typeof data === 'string' ? hexToBytes(strip0x(data)) : data;
  if (offset + 32 > bytes.length) return 0n;
  const slice = bytes.slice(offset, offset + 32);
  let hex = bytesToHex(slice);
  return BigInt('0x' + (hex || '0'));
}

/** Decode 32-byte word as SQ: address. */
export function decodeAddress(data: Uint8Array | string, offset = 0): string {
  const bytes = typeof data === 'string' ? hexToBytes(strip0x(data)) : data;
  if (offset + 32 > bytes.length) {
    return 'SQ:' + '00'.repeat(32);
  }
  return 'SQ:' + bytesToHex(bytes.slice(offset, offset + 32));
}

/** Skip 4-byte selector then decode arg i. */
export function decodeArgUint256(calldata: Uint8Array | string, argIndex: number): bigint {
  return decodeUint256(calldata, 4 + argIndex * 32);
}

export function decodeArgAddress(calldata: Uint8Array | string, argIndex: number): string {
  return decodeAddress(calldata, 4 + argIndex * 32);
}
