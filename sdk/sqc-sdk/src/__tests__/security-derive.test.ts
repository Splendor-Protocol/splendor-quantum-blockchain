/**
 * SQDP-1 security: derivation phishing, ecrecover bind, EIP-191 vs EIP-712, malformed sigs.
 */
import { describe, expect, it } from 'vitest';
import { sha3_256 } from '@noble/hashes/sha3';
import { bytesToHex, hexToBytes } from '@noble/hashes/utils';
import { secp256k1 } from '@noble/curves/secp256k1';
import {
  DERIVATION_DOMAIN_MESSAGE,
  assertSignatureMatchesAddress,
  evmAddressFromPrivateKey,
  personalSignHash,
  recoverAddressFromHash,
  recoverPersonalSignAddress,
  seedFromEvmSignature,
  signDerivationMessage,
  strip0x,
} from '../derive.js';
import { BridgeError, isBridgeError } from '../errors.js';
import golden from '../../testdata/golden-vectors.json';

describe('security-derive', () => {
  it('locks golden domain message (phishing surface)', () => {
    expect(DERIVATION_DOMAIN_MESSAGE).toBe(golden.domainMessage);
  });

  it('domain-message byte flip changes seed and recovered address binding', () => {
    const key = '33'.repeat(32);
    const goodSig = signDerivationMessage(key);
    const goodSeed = seedFromEvmSignature(goodSig);
    const goodAddr = recoverPersonalSignAddress(goodSig);

    const flipped = DERIVATION_DOMAIN_MESSAGE.replace('v1', 'v2');
    expect(flipped).not.toBe(DERIVATION_DOMAIN_MESSAGE);
    // Sign over flipped message hash with same key (simulate phishing domain).
    const hash = personalSignHash(flipped);
    const sig = secp256k1.sign(hash, hexToBytes(key));
    const r = sig.r.toString(16).padStart(64, '0');
    const s = sig.s.toString(16).padStart(64, '0');
    const v = (sig.recovery + 27).toString(16).padStart(2, '0');
    const phishSig = r + s + v;

    const phishSeed = seedFromEvmSignature(phishSig);
    expect(phishSeed).not.toBe(goodSeed);
    // Recovering with the real domain message must NOT yield the signer address.
    expect(() => assertSignatureMatchesAddress(phishSig, goodAddr)).toThrow(BridgeError);
  });

  it('assertSignatureMatchesAddress rejects mismatched claimed address', () => {
    const key = '11'.repeat(32);
    const addr = evmAddressFromPrivateKey(key);
    const sig = signDerivationMessage(key);
    expect(() =>
      assertSignatureMatchesAddress(sig, '0x' + '00'.repeat(20)),
    ).toThrow(BridgeError);
    expect(assertSignatureMatchesAddress(sig, addr).toLowerCase()).toBe(addr.toLowerCase());
  });

  it('EIP-191 vs EIP-712 same EVM key yields different SQ seeds', () => {
    const key = '44'.repeat(32);
    const s191 = seedFromEvmSignature(signDerivationMessage(key, 'eip191-v1'));
    const s712 = seedFromEvmSignature(signDerivationMessage(key, 'eip712-v1'));
    expect(s191).not.toBe(s712);
    expect(s191).toHaveLength(64);
    expect(s712).toHaveLength(64);
  });

  it('rejects malformed signature lengths', () => {
    expect(() => recoverAddressFromHash('aa'.repeat(32), personalSignHash('x'))).toThrow(
      BridgeError,
    );
    expect(() => recoverAddressFromHash('aa'.repeat(66), personalSignHash('x'))).toThrow(
      BridgeError,
    );
    try {
      recoverAddressFromHash('bb'.repeat(32), new Uint8Array(32));
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('USER_REJECTED');
    }
  });

  it('seedFromEvmSignature is SHA3-256 of raw sig bytes', () => {
    const sig = 'ab'.repeat(65);
    const expected = bytesToHex(sha3_256(hexToBytes(strip0x(sig))));
    expect(seedFromEvmSignature(sig)).toBe(expected);
  });
});
