import { describe, expect, it, afterEach } from 'vitest';
import {
  clearRegisteredNetworks,
  getNetwork,
  isPlaceholderNetwork,
  listSelectableNetworks,
  registerNetwork,
  resolveNetwork,
  txExplorerUrl,
  unregisterNetwork,
  type NetworkConfig,
} from '../networks.js';

const sample: NetworkConfig = {
  id: 'custom-rpc',
  name: 'Custom',
  networkId: 'splendor-custom',
  chainId: 'splendor-quantum-1',
  rpcUrls: ['https://rpc.custom.example.test'],
  gatewayUrl: 'https://gw.custom.example.test',
  gatewayPort: 443,
  wasmUrl: '/sqc_signer.wasm',
  execUrl: '/wasm_exec.js',
  wasmSha256: '',
  execSha256: '',
  timeoutMs: 30_000,
  retries: 2,
  explorerUrl: 'https://explorer.custom.example.test',
  txUrlTemplate: 'https://explorer.custom.example.test/tx/{hash}',
};

describe('registerNetwork / resolveNetwork overrides', () => {
  afterEach(() => {
    clearRegisteredNetworks();
  });

  it('resolveNetwork accepts rpcUrls override without gatewayUrl', () => {
    const net = resolveNetwork('testnet', {
      rpcUrls: ['https://rpc.testnet.real'],
    });
    expect(net.rpcUrls).toEqual(['https://rpc.testnet.real']);
    expect(net.id).toBe('testnet');
  });

  it('resolveNetwork accepts rpcUrls + gatewayUrl overrides over placeholders', () => {
    const net = resolveNetwork('testnet', {
      rpcUrls: ['https://rpc.testnet.real'],
      gatewayUrl: 'https://gateway.testnet.real',
    });
    expect(net.rpcUrls).toEqual(['https://rpc.testnet.real']);
    expect(net.gatewayUrl).toBe('https://gateway.testnet.real');
    expect(net.id).toBe('testnet');
  });

  it('registerNetwork makes custom id resolvable and selectable', () => {
    registerNetwork(sample);
    expect(getNetwork('custom-rpc')?.name).toBe('Custom');
    expect(isPlaceholderNetwork(sample)).toBe(false);
    const resolved = resolveNetwork('custom-rpc');
    expect(resolved.rpcUrls[0]).toBe('https://rpc.custom.example.test');
    const selectable = listSelectableNetworks();
    expect(selectable.some((n) => n.id === 'custom-rpc')).toBe(true);
  });

  it('unregisterNetwork removes runtime entry', () => {
    registerNetwork(sample);
    unregisterNetwork('custom-rpc');
    expect(getNetwork('custom-rpc')).toBeUndefined();
    expect(() => resolveNetwork('custom-rpc')).toThrow(/Unknown network/);
  });

  it('txExplorerUrl prefers txUrlTemplate then explorerUrl', () => {
    const h = '0x' + 'ab'.repeat(32);
    expect(txExplorerUrl(sample, h)).toBe(`https://explorer.custom.example.test/tx/${h}`);
    const noTpl = { ...sample, txUrlTemplate: undefined };
    expect(txExplorerUrl(noTpl, h)).toBe(`https://explorer.custom.example.test/tx/${h}`);
    expect(txExplorerUrl(sample, '0xabc')).toBeNull();
    expect(txExplorerUrl(sample, h + '/../evil')).toBeNull();
    expect(txExplorerUrl({ ...sample, txUrlTemplate: 'javascript:alert({hash})' }, h)).toBe(
      `https://explorer.custom.example.test/tx/${h}`,
    );
    expect(txExplorerUrl({ ...sample, explorerUrl: undefined, txUrlTemplate: undefined }, '0x')).toBeNull();
  });
});
