/**
 * Extension provider aliases + ExtensionSigner (mock provider, no Chrome).
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { CHAIN_ID } from '../derive.js';
import {
  toCanonicalMethod,
  toExtensionMethod,
  requestWithAlias,
  isExtensionProvider,
} from '../wallet/extension-provider.js';
import { ExtensionSigner } from '../wallet/extension-signer.js';
import { createSplendorProvider, uninstallSplendorProvider } from '../injected-provider.js';
import { createWalletClient } from '../clients.js';
import { resetWasmLoader } from '../wasm-loader.js';
import { BridgeError } from '../errors.js';

describe('extension method aliases', () => {
  it('maps sqc_* to splendor_* canonical', () => {
    expect(toCanonicalMethod('sqc_accounts')).toBe('splendor_accounts');
    expect(toCanonicalMethod('sqc_requestAccounts')).toBe('splendor_connect');
    expect(toCanonicalMethod('sqc_sign')).toBe('splendor_signMessage');
  });

  it('maps splendor_* to sqc_* for extension calls', () => {
    expect(toExtensionMethod('splendor_accounts')).toBe('sqc_accounts');
    expect(toExtensionMethod('splendor_signMessage')).toBe('sqc_sign');
  });

  it('requestWithAlias falls back on unsupported', async () => {
    const provider = {
      request: vi.fn(async ({ method }: { method: string }) => {
        if (method === 'sqc_accounts') return ['SQ:' + 'ab'.repeat(32)];
        throw new Error('unsupported method');
      }),
    };
    const out = await requestWithAlias(provider, 'splendor_accounts');
    expect(out).toEqual(['SQ:' + 'ab'.repeat(32)]);
    expect(provider.request).toHaveBeenCalled();
  });

  it('isExtensionProvider accepts isExtension or non-in-page isSplendor', () => {
    const req = async () => null;
    expect(isExtensionProvider({ isSplendor: true, request: req })).toBe(true);
    expect(isExtensionProvider({ isSplendor: true, isInPage: true, request: req })).toBe(false);
    expect(isExtensionProvider({ isExtension: true, request: req })).toBe(true);
    expect(
      isExtensionProvider({ isExtension: true, isInPage: true, isSplendor: true, request: req }),
    ).toBe(true);
  });
});

describe('ExtensionSigner', () => {
  const addr = 'SQ:' + 'cd'.repeat(32);

  function mockProvider(
    handlers: Record<string, (params?: unknown) => unknown> = {},
  ) {
    return {
      isSplendor: true as const,
      request: vi.fn(async ({ method, params }: { method: string; params?: unknown }) => {
        if (handlers[method]) return handlers[method]!(params);
        if (method === 'sqc_requestAccounts' || method === 'splendor_connect') return [addr];
        if (method === 'sqc_accounts' || method === 'splendor_accounts') return [addr];
        if (method === 'sqc_sign' || method === 'splendor_signMessage') {
          return { address: addr, signatureB64: 'c2ln', publicKeyB64: 'cGs=', message: 'hi' };
        }
        throw new Error(`unexpected ${method}`);
      }),
      on() {},
      removeListener() {},
    };
  }

  function connection() {
    return new Connection({
      rpcUrl: 'http://127.0.0.1:9',
      chainId: CHAIN_ID,
      retries: 0,
    });
  }

  it('connects via mock extension provider', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    const info = await wallet.connect();
    expect(info.address).toBe(addr);
    expect(info.postQuantumSecure).toBe(true);
    const signed = await wallet.signMessage('hello');
    expect(signed.signatureB64).toBeTruthy();
    wallet.disconnect();
  });

  it('rejects signMessage with raw:true (same as inject)', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const callsBefore = provider.request.mock.calls.length;
    await expect(wallet.signMessage('x', { raw: true })).rejects.toMatchObject({
      code: 'USER_REJECTED',
    });
    await expect(wallet.signMessage('x', { raw: true })).rejects.toBeInstanceOf(BridgeError);
    expect(provider.request.mock.calls.length).toBe(callsBefore);
    wallet.disconnect();
  });

  it('waitForConfirmation delegates to connection with from default', async () => {
    const provider = mockProvider();
    const conn = connection();
    const wait = vi.spyOn(conn, 'waitForConfirmation').mockResolvedValue({
      confirmed: true as const,
      nonce: 2,
    });
    const wallet = new ExtensionSigner({ connection: conn, provider, networkId: 'local' });
    await wallet.connect();
    const out = await wallet.waitForConfirmation({ nonceBefore: 1, hash: '0xabc' });
    expect(out).toEqual({ confirmed: true, nonce: 2 });
    expect(wait).toHaveBeenCalledWith({
      nonceBefore: 1,
      hash: '0xabc',
      from: addr,
    });
    wallet.disconnect();
  });
});

describe('in-page inject dual methods', () => {
  afterEach(() => {
    uninstallSplendorProvider();
    resetWasmLoader();
  });

  it('accepts sqc_accounts alias when wallet connected', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    // Avoid WASM if missing — only test alias routing with unlocked mock-like path:
    // Use createSplendorProvider after manually setting getAddresses via seed if WASM available.
    try {
      await wallet.connect({ mode: 'seed', seed: '01'.repeat(32) });
    } catch {
      // WASM missing in this environment — skip
      wallet.disconnect();
      return;
    }
    const provider = createSplendorProvider({
      wallet,
      networkId: 'local',
      onApprove: async () => true,
    });
    const accounts = await provider.request({ method: 'sqc_accounts' });
    expect(Array.isArray(accounts)).toBe(true);
    expect((accounts as string[])[0]).toMatch(/^SQ:/);
    wallet.disconnect();
  }, 30_000);
});
