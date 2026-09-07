/**
 * Domain-separated EVM → PQ seed derivation.
 * Chain id + domain message come from src/config/sqc.networks.json (via networks.ts).
 *
 * IMPORTANT (SQDP-1): the EIP-191 / EIP-712 signature IS key material —
 * seed = SHA3-256(signature_bytes). Never log or post signatures to public gateways.
 */
import { sha3_256 } from '@noble/hashes/sha3';
import { keccak_256 } from '@noble/hashes/sha3';
import { secp256k1 } from '@noble/curves/secp256k1';
import { bytesToHex, hexToBytes } from '@noble/hashes/utils';
import { derivationDomainMessage, getDefaultNetwork, getProtocolConfig } from './networks.js';
import { BridgeError } from './errors.js';

export const CHAIN_ID = getDefaultNetwork().chainId;

export const DERIVATION_DOMAIN_MESSAGE = derivationDomainMessage(CHAIN_ID);

/** Derivation scheme: EIP-191 personal_sign (default) or EIP-712 typed data. */
export type DerivationScheme = 'eip191-v1' | 'eip712-v1';

export type Eip712DeriveTypes = {
  EIP712Domain: Array<{ name: string; type: string }>;
  SplendorPQDerive: Array<{ name: string; type: string }>;
};

export type Eip712DeriveTypedData = {
  types: Eip712DeriveTypes;
  primaryType: 'SplendorPQDerive';
  domain: {
    name: string;
    version: string;
    // chainId omitted — derivation is not bound to an EVM chain id (SQDP-1 §2.3)
  };
  message: {
    purpose: string;
    protocol: string;
    chain: string;
  };
};

/** EIP-712 typed data for SQDP-1.1 optional path (same seed rule: SHA3-256(sig)). */
export function eip712DerivationTypedData(
  chainId: string = CHAIN_ID,
): Eip712DeriveTypedData {
  const v = getProtocolConfig().derivationDomainVersion;
  return {
    types: {
      EIP712Domain: [
        { name: 'name', type: 'string' },
        { name: 'version', type: 'string' },
      ],
      SplendorPQDerive: [
        { name: 'purpose', type: 'string' },
        { name: 'protocol', type: 'string' },
        { name: 'chain', type: 'string' },
      ],
    },
    primaryType: 'SplendorPQDerive',
    domain: {
      name: 'Splendor Quantum Chain',
      version: v.replace(/^v/, '') || '1',
    },
    message: {
      purpose: 'Key derivation',
      protocol: `SQDP-${v}`,
      chain: chainId,
    },
  };
}

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

function encodeType(primaryType: string, types: Record<string, Array<{ name: string; type: string }>>): string {
  let deps = findTypeDependencies(primaryType, types);
  deps = deps.filter((t) => t !== primaryType);
  deps.sort();
  return [primaryType, ...deps]
    .map((type) => {
      const fields = types[type];
      return `${type}(${fields.map((f) => `${f.type} ${f.name}`).join(',')})`;
    })
    .join('');
}

function findTypeDependencies(
  primaryType: string,
  types: Record<string, Array<{ name: string; type: string }>>,
  results: string[] = [],
): string[] {
  if (results.includes(primaryType) || !types[primaryType]) return results;
  results.push(primaryType);
  for (const field of types[primaryType]) {
    const base = field.type.replace(/\[\]$/, '');
    if (types[base]) findTypeDependencies(base, types, results);
  }
  return results;
}

function typeHash(primaryType: string, types: Record<string, Array<{ name: string; type: string }>>): Uint8Array {
  return keccak_256(new TextEncoder().encode(encodeType(primaryType, types)));
}

function encodeField(
  type: string,
  value: unknown,
  types: Record<string, Array<{ name: string; type: string }>>,
): Uint8Array {
  if (types[type]) {
    return keccak_256(encodeData(type, value as Record<string, unknown>, types));
  }
  if (type === 'string') {
    return keccak_256(new TextEncoder().encode(String(value)));
  }
  if (type === 'bytes') {
    return keccak_256(value as Uint8Array);
  }
  throw new Error(`unsupported EIP-712 type: ${type}`);
}

function encodeData(
  primaryType: string,
  data: Record<string, unknown>,
  types: Record<string, Array<{ name: string; type: string }>>,
): Uint8Array {
  const fields = types[primaryType];
  const parts: Uint8Array[] = [typeHash(primaryType, types)];
  for (const field of fields) {
    parts.push(encodeField(field.type, data[field.name], types));
  }
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

/** EIP-712 struct hash → digest for signing / recovery. */
export function eip712Hash(typedData: Eip712DeriveTypedData): Uint8Array {
  const types = typedData.types as unknown as Record<
    string,
    Array<{ name: string; type: string }>
  >;
  const domainSep = keccak_256(encodeData('EIP712Domain', typedData.domain as unknown as Record<string, unknown>, types));
  const messageHash = keccak_256(
    encodeData(typedData.primaryType, typedData.message as unknown as Record<string, unknown>, types),
  );
  const prefix = new Uint8Array([0x19, 0x01]);
  const joined = new Uint8Array(2 + 32 + 32);
  joined.set(prefix, 0);
  joined.set(domainSep, 2);
  joined.set(messageHash, 34);
  return keccak_256(joined);
}

function normalizeEvmAddress(addr: string): string {
  const a = addr.startsWith('0x') || addr.startsWith('0X') ? addr : `0x${addr}`;
  return a.toLowerCase();
}

/** secp256k1 public key (uncompressed, no 0x04) → Ethereum address. */
export function publicKeyToEvmAddress(uncompressedNoPrefix: Uint8Array): string {
  if (uncompressedNoPrefix.length !== 64) {
    throw BridgeError.userRejected('public key must be 64 bytes (uncompressed without prefix)');
  }
  return '0x' + bytesToHex(keccak_256(uncompressedNoPrefix).slice(-20));
}

/** Derive EVM address from a 32-byte private key hex. */
export function evmAddressFromPrivateKey(evmPrivateKeyHex: string): string {
  const key = hexToBytes(strip0x(evmPrivateKeyHex));
  if (key.length !== 32) {
    throw BridgeError.userRejected('EVM private key must be 32 bytes');
  }
  const pub = secp256k1.getPublicKey(key, false);
  return publicKeyToEvmAddress(pub.slice(1));
}

/**
 * Recover the EVM address that produced a 65-byte signature over `messageHash`.
 */
export function recoverAddressFromHash(signatureHex: string, messageHash: Uint8Array): string {
  const sigBytes = hexToBytes(strip0x(signatureHex));
  if (sigBytes.length !== 65) {
    throw BridgeError.userRejected('signature must be 65 bytes (r||s||v)');
  }
  let recovery = sigBytes[64];
  if (recovery >= 27) recovery -= 27;
  if (recovery !== 0 && recovery !== 1) {
    throw BridgeError.userRejected('invalid signature recovery id');
  }
  const sig = secp256k1.Signature.fromCompact(sigBytes.slice(0, 64)).addRecoveryBit(recovery);
  const pub = sig.recoverPublicKey(messageHash);
  return publicKeyToEvmAddress(pub.toRawBytes(false).slice(1));
}

/** Recover address from EIP-191 personal_sign of the derivation domain message. */
export function recoverPersonalSignAddress(
  signatureHex: string,
  message: string = DERIVATION_DOMAIN_MESSAGE,
): string {
  return recoverAddressFromHash(signatureHex, personalSignHash(message));
}

/** Recover address from EIP-712 SplendorPQDerive signature. */
export function recoverEip712Address(
  signatureHex: string,
  typedData: Eip712DeriveTypedData = eip712DerivationTypedData(),
): string {
  return recoverAddressFromHash(signatureHex, eip712Hash(typedData));
}

/**
 * Assert signature recovers to claimed address (when provided).
 * Returns the recovered checksum-less lowercase 0x address.
 */
export function assertSignatureMatchesAddress(
  signatureHex: string,
  claimedAddress: string | undefined,
  scheme: DerivationScheme = 'eip191-v1',
): string {
  const recovered =
    scheme === 'eip712-v1'
      ? recoverEip712Address(signatureHex)
      : recoverPersonalSignAddress(signatureHex);
  if (claimedAddress) {
    if (normalizeEvmAddress(recovered) !== normalizeEvmAddress(claimedAddress)) {
      throw BridgeError.userRejected(
        `signature does not recover to claimed address ${claimedAddress} (got ${recovered})`,
      );
    }
  }
  return recovered;
}

/**
 * Sign the derivation domain message with an EVM secp256k1 private key (hex).
 * Returns a 65-byte signature (r||s||v) as hex — same shape MetaMask personal_sign returns.
 */
export function signDerivationMessage(
  evmPrivateKeyHex: string,
  scheme: DerivationScheme = 'eip191-v1',
): string {
  const key = hexToBytes(strip0x(evmPrivateKeyHex));
  if (key.length !== 32) {
    throw new Error('EVM private key must be 32 bytes');
  }
  const hash =
    scheme === 'eip712-v1'
      ? eip712Hash(eip712DerivationTypedData())
      : personalSignHash(DERIVATION_DOMAIN_MESSAGE);
  const sig = secp256k1.sign(hash, key);
  const r = sig.r.toString(16).padStart(64, '0');
  const s = sig.s.toString(16).padStart(64, '0');
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
