import { describe, expect, it } from 'vitest';
import {
  PROTOCOL,
  SQDP_VERSION,
  BRIDGE_CLIENT_VERSION,
  ADDRESS_PREFIX,
  MIN_FEE,
  assertAddress,
  canonicalizeAddress,
  isValidAddress,
  functionSelectorHex,
  SqRpc,
  BridgeError,
} from '../index.js';
import { DERIVATION_DOMAIN_MESSAGE, CHAIN_ID } from '../derive.js';

describe('SQDP-1 protocol constants', () => {
  it('exports stable protocol metadata', () => {
    expect(SQDP_VERSION).toBe('1');
    expect(PROTOCOL.version).toBe('1');
    expect(PROTOCOL.chainId).toBe(CHAIN_ID);
    expect(PROTOCOL.chainId).toBe('splendor-quantum-1');
    expect(PROTOCOL.minFee).toBe(MIN_FEE);
    // The chain's admission floor (core/mempool.MinFee, /status min_fee).
    expect(PROTOCOL.minFee).toBe('1000');
    expect(PROTOCOL.confirmation).toBe('nonce');
    expect(PROTOCOL.derivationDomainVersion).toBe('v1');
    expect(PROTOCOL.derivationDomainMessage).toBe(DERIVATION_DOMAIN_MESSAGE);
    expect(PROTOCOL.bridgeClientHeader).toBe(BRIDGE_CLIENT_VERSION);
    expect(PROTOCOL.addressPrefix).toBe(ADDRESS_PREFIX);
    expect(PROTOCOL.canonicalTransferSelector).toBe('4b40e901');
    expect(PROTOCOL.goldenTransferSelector).toBe('14cde9ab');
  });

  it('ABI golden matches PROTOCOL', () => {
    expect(functionSelectorHex('transfer(address,uint256)')).toBe(
      PROTOCOL.canonicalTransferSelector,
    );
    expect(functionSelectorHex('transfer(sqaddress,uint256)')).toBe(
      PROTOCOL.goldenTransferSelector,
    );
  });
});

describe('SQDP-1 address canonicalization', () => {
  it('lowercases hex after SQ:', () => {
    const mixed = 'SQ:' + 'AB'.repeat(32);
    const want = 'SQ:' + 'ab'.repeat(32);
    expect(isValidAddress(mixed)).toBe(true);
    expect(assertAddress(mixed)).toBe(want);
    expect(canonicalizeAddress(mixed)).toBe(want);
  });

  it('rejects 0x and raw hex without SQ:', () => {
    expect(isValidAddress('0x' + 'ab'.repeat(20))).toBe(false);
    expect(canonicalizeAddress('ab'.repeat(32))).toBeNull();
    expect(() => assertAddress('ab'.repeat(32))).toThrow(BridgeError);
  });
});

describe('SQDP-1 RPC options', () => {
  it('rejects empty rpcUrls', () => {
    expect(() => new SqRpc({ rpcUrls: [] })).toThrow(BridgeError);
    expect(() => new SqRpc({ rpcUrls: ['', '  '] })).toThrow(BridgeError);
  });

  it('accepts valid rpcUrls', () => {
    const rpc = new SqRpc({ rpcUrls: ['http://a.example/', 'http://b.example'] });
    expect(rpc.rpcUrls).toEqual(['http://a.example', 'http://b.example']);
  });
});
