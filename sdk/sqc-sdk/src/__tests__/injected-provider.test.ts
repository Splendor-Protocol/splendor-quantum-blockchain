import { describe, expect, it, afterEach } from 'vitest';
import {
  createWalletClient,
  createSplendorProvider,
  installSplendorProvider,
  uninstallSplendorProvider,
} from '../index.js';

const GOLDEN_SEED = '01'.repeat(32);
const allow = async () => true;

describe('injected SQC provider', () => {
  afterEach(() => {
    uninstallSplendorProvider();
    if (typeof window !== 'undefined') {
      delete (window as { splendor?: unknown }).splendor;
    }
  });

  it('createSplendorProvider answers accounts / signMessage after wallet connect', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    await wallet.connect({ mode: 'seed', seed: GOLDEN_SEED });
    const provider = createSplendorProvider({
      wallet,
      networkId: 'local',
      onApprove: allow,
    });

    const accounts = (await provider.request({ method: 'splendor_accounts' })) as string[];
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

  it('rejects seed/evmSignature connect, raw signMessage, and unsigned sensitive calls', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    const provider = createSplendorProvider({ wallet, networkId: 'local' });

    await expect(
      provider.request({
        method: 'splendor_connect',
        params: { mode: 'seed', seed: GOLDEN_SEED },
      }),
    ).rejects.toThrow(/refuses seed|privateKey|evm|ConnectKit/i);

    await expect(
      provider.request({
        method: 'splendor_connect',
        params: { mode: 'evmSignature', signature: '0xab' },
      }),
    ).rejects.toThrow(/refuses seed|evm|ConnectKit/i);

    await wallet.connect({ mode: 'seed', seed: GOLDEN_SEED });

    // No onApprove → fail closed on sensitive methods
    await expect(
      provider.request({
        method: 'splendor_signMessage',
        params: { message: 'ping' },
      }),
    ).rejects.toThrow(/no onApprove|rejected/i);

    const approved = createSplendorProvider({
      wallet,
      networkId: 'local',
      onApprove: allow,
    });
    await expect(
      approved.request({
        method: 'splendor_signMessage',
        params: { message: 'x', raw: true },
      }),
    ).rejects.toThrow(/raw:true|domain separation/i);
    wallet.disconnect();
  }, 30_000);

  it('installSplendorProvider never overwrites extension inject (force ignored)', () => {
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
    expect(forced).toBe(existing);
    expect(window.splendor).toBe(existing);
    wallet.disconnect();
  });

  it('rejects eth_* methods', async () => {
    const wallet = createWalletClient({ network: 'local', allowUnpinnedWasm: true });
    const provider = createSplendorProvider({ wallet, networkId: 'local' });
    await expect(provider.request({ method: 'eth_chainId' })).rejects.toThrow(/splendor_/);
    wallet.disconnect();
  });
});
