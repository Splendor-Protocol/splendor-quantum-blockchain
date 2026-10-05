import { describe, expect, it } from 'vitest';
import { decodeAddress, decodeUint256, encodeFunctionData } from '../abi.js';

const SIG = 'set(uint256)';

function argWord(data: Uint8Array): bigint {
  return decodeUint256(data, 4);
}

describe('ABI argument encoding', () => {
  it('treats a bare digit string as base-10', () => {
    expect(argWord(encodeFunctionData(SIG, ['42']))).toBe(42n);
    expect(argWord(encodeFunctionData(SIG, ['1000']))).toBe(1000n);
  });

  it('treats a 0x-prefixed string as hex', () => {
    expect(argWord(encodeFunctionData(SIG, ['0x42']))).toBe(0x42n);
    expect(argWord(encodeFunctionData(SIG, ['0xabc']))).toBe(0xabcn);
  });

  it('rejects bare hex, free text and leading zeros instead of guessing', () => {
    expect(() => encodeFunctionData(SIG, ['ff'])).toThrow(/Unsupported ABI string/);
    expect(() => encodeFunctionData(SIG, ['hello'])).toThrow(/Unsupported ABI string/);
    expect(() => encodeFunctionData(SIG, ['042'])).toThrow(/Unsupported ABI string/);
    expect(() => encodeFunctionData(SIG, ['-1'])).toThrow();
  });

  it('rejects unsafe JS numbers and negatives', () => {
    expect(() => encodeFunctionData(SIG, [2 ** 53])).toThrow(/safe integers/);
    expect(() => encodeFunctionData(SIG, [1.5])).toThrow(/safe integers/);
    expect(() => encodeFunctionData(SIG, [-1n])).toThrow(/non-negative/);
    expect(argWord(encodeFunctionData(SIG, [2n ** 64n]))).toBe(2n ** 64n);
  });

  it('rejects EVM and unprefixed addresses', () => {
    expect(() => encodeFunctionData('f(address)', ['0x' + '11'.repeat(20)])).toThrow(/Invalid SQ address/);
    expect(() => encodeFunctionData('f(address)', ['ab'.repeat(32)])).toThrow(/Invalid SQ address/);
  });

  it('rejects words wider than 32 bytes', () => {
    expect(() => encodeFunctionData(SIG, ['0x' + 'ff'.repeat(33)])).toThrow(/exceeds 32 bytes/);
  });

  it('throws on short return data instead of decoding garbage', () => {
    expect(() => decodeUint256('0x' + '00'.repeat(31))).toThrow(/too short/);
    expect(() => decodeAddress(new Uint8Array(16))).toThrow(/too short/);
    expect(() => decodeUint256(new Uint8Array(32), 1)).toThrow(/too short/);
  });
});
