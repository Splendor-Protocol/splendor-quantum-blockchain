/**
 * SQDP-1 security: HTTPS-only non-local RPC, browser WASM pin on wallet.init.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { resolveNetwork } from '../networks.js';
import { SplendorPQBridge } from '../splendor-wallet.js';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('security-networks HTTPS', () => {
  it('allows local http endpoints', () => {
    const net = resolveNetwork('local');
    expect(net.rpcUrls[0].startsWith('http://')).toBe(true);
  });

  it('rejects non-local http RPC without allowInsecureHttp', () => {
    expect(() =>
      resolveNetwork('testnet', { rpcUrls: ['http://10.0.0.5:8080'] }),
    ).toThrow(/https:\/\//i);
  });

  it('does not require gatewayUrl for SDK resolve (rpcUrls only)', () => {
    const net = resolveNetwork('testnet', {
      rpcUrls: ['https://rpc.ok'],
      // gateway left as the baked-in empty value — must not throw
    });
    expect(net.rpcUrls[0]).toBe('https://rpc.ok');
  });

  it('requireGateway enforces https gateway when asked', () => {
    expect(() =>
      resolveNetwork('testnet', {
        rpcUrls: ['https://rpc.ok'],
        gatewayUrl: 'http://10.0.0.5:3090',
        requireGateway: true,
      }),
    ).toThrow(/gatewayUrl must use https/i);
  });

  it('allowInsecureHttp opts out of HTTPS requirement', () => {
    const resolved = resolveNetwork('testnet', {
      rpcUrls: ['http://10.0.0.5:8080'],
      allowInsecureHttp: true,
    });
    expect(resolved.rpcUrls[0]).toBe('http://10.0.0.5:8080');
  });

  it('still rejects placeholder .example even with allowInsecureHttp', () => {
    expect(() =>
      resolveNetwork('testnet', {
        rpcUrls: ['https://rpc.splendor.example'],
        allowInsecureHttp: true,
      }),
    ).toThrow(/placeholder/i);
  });
});

describe('security-networks browser pin policy', () => {
  it('wallet.init requires wasmSha256 for non-local browser unless allowUnpinnedWasm', async () => {
    vi.stubGlobal('document', {});
    const b = new SplendorPQBridge({
      networkId: 'testnet',
      rpcUrls: ['https://rpc.test'],
      chainId: 'splendor-quantum-1',
    });
    await expect(b.init()).rejects.toMatchObject({
      code: 'WASM',
      message: expect.stringContaining('wasmSha256'),
    });
  });

  it('allowUnpinnedWasm skips the pin-required gate', async () => {
    vi.stubGlobal('document', {});
    const b = new SplendorPQBridge({
      networkId: 'testnet',
      rpcUrls: ['https://rpc.test'],
      chainId: 'splendor-quantum-1',
      allowUnpinnedWasm: true,
      wasmUrl: 'https://invalid.example/sqc_signer.wasm',
      execUrl: 'https://invalid.example/wasm_exec.js',
    });
    try {
      await b.init();
      expect.fail('expected load failure after pin gate');
    } catch (e) {
      expect(String((e as Error).message)).not.toMatch(/wasmSha256 is required/);
    }
  });
});
