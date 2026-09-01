import { describe, expect, it } from 'vitest';
import { fromSPLD, toSPLD } from '../rpc.js';
import { isValidAddress, assertAddress } from '../address.js';
import { BridgeError, isBridgeError } from '../errors.js';
import {
  DERIVATION_DOMAIN_MESSAGE,
  seedFromEvmSignature,
  signDerivationMessage,
  recoverPersonalSignAddress,
  assertSignatureMatchesAddress,
  evmAddressFromPrivateKey,
  eip712DerivationTypedData,
  eip712Hash,
  recoverEip712Address,
} from '../derive.js';
import { MIN_FEE, assertFeeAtLeastMin, isHexString, encodePayload } from '../encoding.js';
import { listNetworks, resolveNetwork, getNetwork } from '../networks.js';

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
  it('accepts valid SQ address and canonicalizes case', () => {
    const a = 'SQ:' + 'AB'.repeat(32);
    expect(isValidAddress(a)).toBe(true);
    expect(assertAddress(a)).toBe('SQ:' + 'ab'.repeat(32));
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

  it('ecrecover binds signature to EVM address', () => {
    const key = '11'.repeat(32);
    const addr = evmAddressFromPrivateKey(key);
    const sig = signDerivationMessage(key);
    expect(recoverPersonalSignAddress(sig).toLowerCase()).toBe(addr.toLowerCase());
    expect(assertSignatureMatchesAddress(sig, addr).toLowerCase()).toBe(addr.toLowerCase());
    expect(() => assertSignatureMatchesAddress(sig, '0x' + '00'.repeat(20))).toThrow(BridgeError);
  });

  it('eip712 derivation recovers stably', () => {
    const key = '22'.repeat(32);
    const sig = signDerivationMessage(key, 'eip712-v1');
    const typed = eip712DerivationTypedData();
    expect(eip712Hash(typed)).toHaveLength(32);
    const recovered = recoverEip712Address(sig, typed);
    expect(recovered.toLowerCase()).toBe(evmAddressFromPrivateKey(key).toLowerCase());
  });
});

describe('networks', () => {
  it('gives distinct networkId per env while sharing wire chainId', () => {
    const nets = listNetworks();
    const ids = new Set(nets.map((n) => n.networkId));
    expect(ids.size).toBe(nets.length);
    expect(resolveNetwork('local').chainId).toBe('splendor-quantum-1');
    expect(getNetwork('testnet')?.networkId).toBe('splendor-testnet');
    expect(getNetwork('mainnet')?.networkId).toBe('splendor-mainnet');
    expect(getNetwork('testnet')?.chainId).toBe('splendor-quantum-1');
    expect(() => resolveNetwork('testnet')).toThrow(/placeholder RPC/);
    expect(() => resolveNetwork('mainnet')).toThrow(/placeholder RPC/);
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
