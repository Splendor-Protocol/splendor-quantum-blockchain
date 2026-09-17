import { describe, expect, it, vi } from 'vitest';
import {
  encodeFunctionData,
  encodeFunctionDataHex,
  functionSelectorHex,
  decodeAddress,
  decodeUint256,
  decodeArgAddress,
  decodeArgUint256,
  canonicalizeAbiSignature,
} from '../abi.js';
import { BridgeEmitter } from '../events.js';
import { SqcProvider } from '../provider.js';
import { SqRpc } from '../rpc.js';
import { bytesToHex } from '../encoding.js';

describe('QSol ABI', () => {
  it('selector is SHA3-256 first 4 bytes', () => {
    // Hash-as-written: compiler-canonical address vs legacy sqaddress alias.
    expect(functionSelectorHex('transfer(address,uint256)')).toBe('4b40e901');
    expect(functionSelectorHex('transfer(sqaddress,uint256)')).toBe('14cde9ab');
    expect(canonicalizeAbiSignature('transfer(sqaddress,uint256)')).toBe(
      'transfer(address,uint256)',
    );
  });

  it('encodes selector + 32-byte words', () => {
    const to = 'SQ:' + 'ab'.repeat(32);
    const data = encodeFunctionData('transfer(sqaddress,uint256)', [to, 1000n]);
    expect(data.length).toBe(4 + 64);
    expect(bytesToHex(data.slice(0, 4))).toBe(functionSelectorHex('transfer(sqaddress,uint256)'));
    expect(decodeArgAddress(data, 0)).toBe(to);
    expect(decodeArgUint256(data, 1)).toBe(1000n);
  });

  it('decode helpers handle hex strings', () => {
    const hex = encodeFunctionDataHex('foo()', []);
    expect(hex.startsWith('0x')).toBe(true);
    expect(decodeUint256(new Uint8Array(32))).toBe(0n);
    expect(decodeAddress(new Uint8Array(32))).toBe('SQ:' + '00'.repeat(32));
  });
});

describe('BridgeEmitter', () => {
  it('on/off/emit', () => {
    const e = new BridgeEmitter();
    const fn = vi.fn();
    const off = e.on('connect', fn);
    e.emit('connect', { address: 'SQ:x' });
    expect(fn).toHaveBeenCalledWith({ address: 'SQ:x' });
    off();
    e.emit('connect', { address: 'SQ:y' });
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe('SqcProvider', () => {
  it('exposes read API and toBridgeOptions', () => {
    const p = new SqcProvider({
      rpcUrls: ['http://a.example', 'http://b.example'],
      chainId: 'splendor-quantum-1',
      retries: 3,
    });
    expect(p.rpcUrls).toEqual(['http://a.example', 'http://b.example']);
    expect(p.toBridgeOptions().rpcUrls).toHaveLength(2);
    expect(p.toBridgeOptions().retries).toBe(3);
  });

  it('can share SqRpc instance', () => {
    const rpc = new SqRpc({ rpcUrl: 'http://shared.example' });
    const p = new SqcProvider({}, rpc);
    expect(p.rpc).toBe(rpc);
  });
});

describe('SqRpc multi-url options', () => {
  it('normalizes rpcUrls', () => {
    const rpc = new SqRpc({ rpcUrls: ['http://x/', 'http://y/'] });
    expect(rpc.rpcUrls).toEqual(['http://x', 'http://y']);
    expect(rpc.rpcUrl).toBe('http://x');
  });
});
