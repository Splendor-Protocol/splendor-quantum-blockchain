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

function hexToBytes(hex: string): Uint8Array {
  const h = hex.replace(/^0x/i, '');
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(h.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function addressBytes(addr: string): Uint8Array {
  const dep = assertAddress(addr);
  return hexToBytes(dep.slice(ADDRESS_PREFIX.length));
}

function u64be(n: number): Uint8Array {
  const buf = new Uint8Array(8);
  new DataView(buf.buffer).setBigUint64(0, BigInt(n), false);
  return buf;
}

function concatBytes(...parts: Uint8Array[]): Uint8Array {
  const len = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(len);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

/** Compare two 32-byte addresses lexicographically (unsigned). */
function addrLess(a: Uint8Array, b: Uint8Array): boolean {
  for (let i = 0; i < 32; i++) {
    if (a[i] < b[i]) return true;
    if (a[i] > b[i]) return false;
  }
  return false;
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
  const depBytes = addressBytes(deployer);
  const nonceStr = new TextEncoder().encode(String(nonce));
  const tag = new TextEncoder().encode('qrc20');
  return hashToAddress(sha3_256(concatBytes(depBytes, nonceStr, tag)));
}

/**
 * QRC-1155 collection address: SHA3(deployer ‖ decimal(nonce) ‖ "qrc1155")
 */
export function qrc1155Address(deployer: string, nonce: number): string {
  const depBytes = addressBytes(deployer);
  const nonceStr = new TextEncoder().encode(String(nonce));
  const tag = new TextEncoder().encode('qrc1155');
  return hashToAddress(sha3_256(concatBytes(depBytes, nonceStr, tag)));
}

/** Bytecode contract address: SHA3(deployer ‖ u64be(nonce)). */
export function bytecodeContractAddress(deployer: string, nonce: number): string {
  const depBytes = addressBytes(deployer);
  return hashToAddress(sha3_256(concatBytes(depBytes, u64be(nonce))));
}

/**
 * Exchange instance address helper — leftover adapter (not node-routed). Prefer @splendor/sqc-sdk/defi.
 * SHA3(deployer ‖ decimal(nonce) ‖ u64be(height) ‖ "exchange")
 */
export function exchangeAddress(deployer: string, nonce: number, height: number): string {
  const depBytes = addressBytes(deployer);
  const nonceStr = new TextEncoder().encode(String(nonce));
  const tag = new TextEncoder().encode('exchange');
  return hashToAddress(sha3_256(concatBytes(depBytes, nonceStr, u64be(height), tag)));
}

/**
 * Bridge instance: SHA3(deployer ‖ decimal(nonce) ‖ u64be(height) ‖ "bridge")
 */
export function bridgeAddress(deployer: string, nonce: number, height: number): string {
  const depBytes = addressBytes(deployer);
  const nonceStr = new TextEncoder().encode(String(nonce));
  const tag = new TextEncoder().encode('bridge');
  return hashToAddress(sha3_256(concatBytes(depBytes, nonceStr, u64be(height), tag)));
}

/** Module-managed SPLV token for a bridge: SHA3(bridge ‖ "bridge:splv") */
export function bridgeSplvAddress(bridge: string): string {
  const b = addressBytes(bridge);
  const tag = new TextEncoder().encode('bridge:splv');
  return hashToAddress(sha3_256(concatBytes(b, tag)));
}

/**
 * DEX pool address helper — leftover adapter (not node-routed). Prefer @splendor/sqc-sdk/defi.
 * DEX pool: SHA3(token0 ‖ token1 ‖ "pool") with canonical token order
 * (smaller address bytes first).
 */
export function dexPoolAddress(tokenA: string, tokenB: string): string {
  let t0 = addressBytes(tokenA);
  let t1 = addressBytes(tokenB);
  if (addrLess(t1, t0)) {
    const tmp = t0;
    t0 = t1;
    t1 = tmp;
  }
  const tag = new TextEncoder().encode('pool');
  return hashToAddress(sha3_256(concatBytes(t0, t1, tag)));
}
