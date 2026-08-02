/**
 * Domain-separated EVM → PQ seed derivation.
 * Must match Go: signer.DerivationDomainMessage + SeedFromEVMSignature.
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { keccak_256 } from '@noble/hashes/sha3';
import { secp256k1 } from '@noble/curves/secp256k1';
import { bytesToHex, hexToBytes } from '@noble/hashes/utils';

export const DERIVATION_DOMAIN_MESSAGE =
  'Splendor Quantum Chain\nKey derivation v1\nchain: splendor-quantum-1';

export const CHAIN_ID = 'splendor-quantum-1';

/** EIP-191 personal_sign hash of a UTF-8 message. */
export function personalSignHash(message: string): Uint8Array {
  const msgBytes = new TextEncoder().encode(message);
  const prefix = new TextEncoder().encode(
    `\x19Ethereum Signed Message:\n${msgBytes.length}`,
  );
  const joined = new Uint8Array(prefix.length + msgBytes.length);
  joined.set(prefix, 0);
  joined.set(msgBytes, prefix.length);
  return keccak_256(joined);
}

/**
 * Sign the derivation domain message with an EVM secp256k1 private key (hex).
 * Returns a 65-byte signature (r||s||v) as hex — same shape MetaMask personal_sign returns.
 */
export function signDerivationMessage(evmPrivateKeyHex: string): string {
  const key = hexToBytes(strip0x(evmPrivateKeyHex));
  if (key.length !== 32) {
    throw new Error('EVM private key must be 32 bytes');
  }
  const hash = personalSignHash(DERIVATION_DOMAIN_MESSAGE);
  const sig = secp256k1.sign(hash, key);
  const r = sig.r.toString(16).padStart(64, '0');
  const s = sig.s.toString(16).padStart(64, '0');
  // recovery bit + 27 (Ethereum convention)
  const v = (sig.recovery + 27).toString(16).padStart(2, '0');
  return r + s + v;
}

/** seed = SHA3-256(signatureBytes) — matches Go SeedFromEVMSignature. */
export function seedFromEvmSignature(sigHexOrBytes: string | Uint8Array): string {
  const bytes =
    typeof sigHexOrBytes === 'string'
      ? hexToBytes(strip0x(sigHexOrBytes))
      : sigHexOrBytes;
  return bytesToHex(sha3_256(bytes));
}

export function strip0x(s: string): string {
  return s.startsWith('0x') || s.startsWith('0X') ? s.slice(2) : s;
}

export function ensure0x(s: string): string {
  return s.startsWith('0x') || s.startsWith('0X') ? s : `0x${s}`;
}
