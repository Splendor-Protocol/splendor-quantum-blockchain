import { describe, expect, it } from 'vitest';
import { fromSPLD, toSPLD } from '../rpc.js';
import { isValidAddress, assertAddress } from '../address.js';
import { BridgeError, isBridgeError } from '../errors.js';
import {
  DERIVATION_DOMAIN_MESSAGE,
  seedFromEvmSignature,
  signDerivationMessage,
} from '../derive.js';
import { MIN_FEE, assertFeeAtLeastMin, isHexString, encodePayload } from '../encoding.js';

describe('units', () => {
  it('round-trips whole SPLD', () => {
    expect(fromSPLD('1')).toBe('1000000000000000000');
    expect(toSPLD('1000000000000000000')).toBe('1.0');
  });

  it('handles zero and 1 wei', () => {
    expect(fromSPLD('0')).toBe('0');
    expect(toSPLD('0')).toBe('0.0');
    expect(toSPLD('1')).toBe('0.000000');
  });

  it('handles fractions', () => {
    expect(fromSPLD('0.5')).toBe('500000000000000000');
    expect(fromSPLD('1.25')).toBe('1250000000000000000');
  });
});

describe('address', () => {
  it('accepts valid SQ address', () => {
    const a = 'SQ:' + 'ab'.repeat(32);
    expect(isValidAddress(a)).toBe(true);
    expect(assertAddress(a)).toBe(a);
  });

  it('rejects invalid addresses', () => {
    expect(isValidAddress('0xabc')).toBe(false);
    expect(isValidAddress('SQ:short')).toBe(false);
    expect(() => assertAddress('bad')).toThrow(BridgeError);
  });
});

describe('derive', () => {
  it('keeps domain message aligned with Go', () => {
    expect(DERIVATION_DOMAIN_MESSAGE).toBe(
      'Splendor Quantum Chain\nKey derivation v1\nchain: splendor-quantum-1',
    );
  });

  it('produces stable seed from EVM key', () => {
    const key = '11'.repeat(32);
    const sig1 = signDerivationMessage(key);
    const sig2 = signDerivationMessage(key);
    expect(sig1).toBe(sig2);
    const seed1 = seedFromEvmSignature(sig1);
    const seed2 = seedFromEvmSignature(sig2);
    expect(seed1).toBe(seed2);
    expect(seed1).toHaveLength(64);
  });
});

describe('fee and encoding', () => {
  it('enforces MIN_FEE', () => {
    expect(assertFeeAtLeastMin(undefined)).toBe(MIN_FEE);
    expect(assertFeeAtLeastMin('1000')).toBe('1000');
    expect(() => assertFeeAtLeastMin('999')).toThrow();
  });

  it('classifies hex vs base64 payloads', () => {
    expect(isHexString('0xdead')).toBe(true);
    const hexEnc = encodePayload('0xabcd');
    expect(hexEnc.calldata_hex).toBe('0xabcd');
    const b64 = encodePayload(new Uint8Array([1, 2, 3]));
    expect(b64.calldata_b64).toBeTruthy();
  });
});

describe('BridgeError', () => {
  it('is identifiable', () => {
    const e = BridgeError.rpc('fail', { status: 400 });
    expect(isBridgeError(e)).toBe(true);
    expect(e.code).toBe('RPC');
  });
});
