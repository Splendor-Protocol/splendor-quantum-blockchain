import { describe, expect, it, afterEach } from 'vitest';
import {
  createWalletClient,
  createSplendorProvider,
  installSplendorProvider,
  uninstallSplendorProvider,
} from '../index.js';

const GOLDEN_SEED = '01'.repeat(32);

describe('injected SQC provider', () => {
  afterEach(() => {
    uninstallSplendorProvider();
    if (typeof window !== 'undefined') {
      delete (window as { splendor?: unknown }).splendor;
    }
  });

  it('createSplendorProvider answers splendor_accounts / connect / signMessage', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    const provider = createSplendorProvider({ wallet, networkId: 'local' });

    expect(await provider.request({ method: 'splendor_accounts' })).toEqual([]);

    const accounts = (await provider.request({
      method: 'splendor_connect',
      params: { mode: 'seed', seed: GOLDEN_SEED },
    })) as string[];
    expect(accounts[0]).toMatch(/^SQ:/);

    const chain = (await provider.request({ method: 'splendor_chainId' })) as {
      networkId: string;
    };
    expect(chain.networkId).toBe('local');

    const signed = (await provider.request({
      method: 'splendor_signMessage',
      params: { message: 'ping' },
    })) as { signatureB64: string; message: string };
    expect(signed.signatureB64).toBeTruthy();
    expect(signed.message).toContain('SPLENDOR-QC-MSG-v1');

    await provider.request({ method: 'splendor_disconnect' });
    expect(await provider.request({ method: 'splendor_accounts' })).toEqual([]);
    wallet.disconnect();
  }, 30_000);

  it('installSplendorProvider does not overwrite existing inject', () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    const existing = {
      isSplendor: true as const,
      request: async () => [],
      on() {},
      removeListener() {},
      once() {},
    };
    (globalThis as { window?: Window }).window = globalThis as unknown as Window;
    window.splendor = existing;

    const installed = installSplendorProvider({ wallet, networkId: 'local' });
    expect(installed).toBe(existing);

    const forced = installSplendorProvider({ wallet, networkId: 'local', force: true });
    expect(forced).not.toBe(existing);
    expect(window.splendor?.isInPage).toBe(true);
    wallet.disconnect();
  });

  it('rejects eth_* methods', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    const provider = createSplendorProvider({ wallet, networkId: 'local' });
    await expect(provider.request({ method: 'eth_chainId' })).rejects.toThrow(/splendor_/);
    wallet.disconnect();
  });
});
