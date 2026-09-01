/**
 * Deterministic address derivation for native modules and contracts.
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { assertAddress } from '../address.js';

const ADDRESS_PREFIX = 'SQ:';

function hashToAddress(hash: Uint8Array): string {
  const hex = Array.from(hash)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
  return ADDRESS_PREFIX + hex;
}

/** SHA3-256("splendor:module-router:v1") — pinned in golden-vectors.json */
export const MODULE_ROUTER_ADDRESS =
  'SQ:b07043f4ef749951893985d66494d0f4e44c8812053361c07ea24bc70a444dcd';

export function deriveModuleRouterAddress(): string {
  return hashToAddress(sha3_256(new TextEncoder().encode('splendor:module-router:v1')));
}

/**
 * QRC-20 token address: SHA3(deployer ‖ decimal(nonce) ‖ "qrc20")
 */
export function qrc20Address(deployer: string, nonce: number): string {
  const dep = assertAddress(deployer);
  const depHex = dep.slice(ADDRESS_PREFIX.length);
  const depBytes = hexToBytes(depHex);
  const nonceStr = new TextEncoder().encode(String(nonce));
  const tag = new TextEncoder().encode('qrc20');
  const input = new Uint8Array(depBytes.length + nonceStr.length + tag.length);
  input.set(depBytes, 0);
  input.set(nonceStr, depBytes.length);
  input.set(tag, depBytes.length + nonceStr.length);
  return hashToAddress(sha3_256(input));
}

/** Bytecode contract address: SHA3(deployer ‖ u64be(nonce)). */
export function bytecodeContractAddress(deployer: string, nonce: number): string {
  const dep = assertAddress(deployer);
  const depHex = dep.slice(ADDRESS_PREFIX.length);
  const depBytes = hexToBytes(depHex);
  const nonceBuf = new Uint8Array(8);
  const view = new DataView(nonceBuf.buffer);
  view.setBigUint64(0, BigInt(nonce), false);
  const input = new Uint8Array(depBytes.length + 8);
  input.set(depBytes, 0);
  input.set(nonceBuf, depBytes.length);
  return hashToAddress(sha3_256(input));
}

function hexToBytes(hex: string): Uint8Array {
  const h = hex.replace(/^0x/i, '');
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(h.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}
