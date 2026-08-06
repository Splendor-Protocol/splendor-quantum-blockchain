/**
 * Domain-separated EVM → PQ seed derivation.
 * Chain id + domain message come from src/config/sqc.networks.json (via networks.ts).
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { keccak_256 } from '@noble/hashes/sha3';
import { secp256k1 } from '@noble/curves/secp256k1';
import { bytesToHex, hexToBytes } from '@noble/hashes/utils';
import { derivationDomainMessage, getDefaultNetwork } from './networks.js';

export const CHAIN_ID = getDefaultNetwork().chainId;

export const DERIVATION_DOMAIN_MESSAGE = derivationDomainMessage(CHAIN_ID);

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

/** seed = SHA3-256(evmSignatureBytes) as hex (64 chars). */
export function seedFromEvmSignature(signatureHex: string): string {
  const sig = hexToBytes(strip0x(signatureHex));
  return bytesToHex(sha3_256(sig));
}

export function strip0x(hex: string): string {
  return hex.startsWith('0x') || hex.startsWith('0X') ? hex.slice(2) : hex;
}
