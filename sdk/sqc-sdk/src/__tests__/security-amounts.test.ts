import { describe, expect, it } from 'vitest';
import { parseRawAmount } from '../units.js';
import { assertFeeAtLeastMin, MIN_FEE } from '../encoding.js';
import { getNetwork, isSafeExplorerUrl, registerNetwork, txExplorerUrl, type NetworkConfig } from '../networks.js';

const HASH = 'ab'.repeat(32);

describe('parseRawAmount', () => {
  it('accepts non-negative integers in base units', () => {
    expect(parseRawAmount('1000')).toBe('1000');
    expect(parseRawAmount(' 7 ')).toBe('7');
    expect(parseRawAmount(5)).toBe('5');
    expect(parseRawAmount(10n ** 30n)).toBe((10n ** 30n).toString());
  });

  it.each([['1.5'], ['-1'], ['1e18'], ['0x10'], ['01'], [''], [-1], [1.5], [2 ** 53], [-1n], [null], [{}]])(
    'rejects %s',
    (raw) => {
      expect(() => parseRawAmount(raw)).toThrow();
    },
  );
});

describe('assertFeeAtLeastMin', () => {
  it('defaults to MIN_FEE and enforces the floor', () => {
    expect(assertFeeAtLeastMin(undefined)).toBe(MIN_FEE);
    expect(() => assertFeeAtLeastMin((BigInt(MIN_FEE) - 1n).toString())).toThrow(/below minimum/);
    expect(() => assertFeeAtLeastMin('1.5')).toThrow(/decimal integer/);
  });

  it('enforces a caller fee cap', () => {
    const fee = (BigInt(MIN_FEE) * 10n).toString();
    expect(assertFeeAtLeastMin(fee, fee)).toBe(fee);
    expect(() => assertFeeAtLeastMin(fee, MIN_FEE)).toThrow();
    expect(() => assertFeeAtLeastMin(MIN_FEE, 'lots')).toThrow(/maxFee/);
  });
});

describe('explorer URLs', () => {
  const base = getNetwork('testnet')!;
  const net: NetworkConfig = { ...base, explorerUrl: 'https://explorer.example.org/', txUrlTemplate: undefined };

  it('only links well-formed transaction hashes', () => {
    expect(txExplorerUrl(net, HASH)).toBe(`https://explorer.example.org/tx/${HASH}`);
    expect(txExplorerUrl(net, '0x' + HASH)).toBe(`https://explorer.example.org/tx/0x${HASH}`);
    expect(txExplorerUrl(net, 'abc')).toBeNull();
    expect(txExplorerUrl(net, `${HASH}/../../evil`)).toBeNull();
    expect(txExplorerUrl(net, '')).toBeNull();
  });

  it('ignores unsafe explorer schemes', () => {
    expect(txExplorerUrl({ ...net, explorerUrl: 'javascript:alert(1)//' }, HASH)).toBeNull();
    expect(txExplorerUrl({ ...net, explorerUrl: 'http://explorer.example.org' }, HASH)).toBeNull();
    expect(txExplorerUrl({ ...net, explorerUrl: undefined, txUrlTemplate: 'data:text/html,{hash}' }, HASH)).toBeNull();
    expect(txExplorerUrl({ ...net, explorerUrl: 'http://localhost:3000' }, HASH)).toBe(`http://localhost:3000/tx/${HASH}`);
  });

  it('isSafeExplorerUrl allows https and loopback http only', () => {
    expect(isSafeExplorerUrl('https://x.org/tx/{hash}')).toBe(true);
    expect(isSafeExplorerUrl('http://127.0.0.1:4000')).toBe(true);
    expect(isSafeExplorerUrl('http://x.org')).toBe(false);
    expect(isSafeExplorerUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeExplorerUrl(undefined)).toBe(false);
  });

  it('registerNetwork rejects unsafe explorer URLs', () => {
    expect(() =>
      registerNetwork({ ...base, id: 'evil-explorer', explorerUrl: 'javascript:alert(1)' }),
    ).toThrow(/explorerUrl/);
    expect(() =>
      registerNetwork({ ...base, id: 'evil-template', explorerUrl: undefined, txUrlTemplate: 'http://evil.example/{hash}' }),
    ).toThrow(/txUrlTemplate/);
  });
});
