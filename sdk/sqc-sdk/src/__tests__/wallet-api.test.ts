import { describe, expect, it, afterEach, vi } from 'vitest';
import {
  Wallet,
  createPublicClient,
  createWalletClient,
  prepareSignMessage,
  SIGN_MESSAGE_DOMAIN,
  WalletError,
  isWalletError,
  resetWasmLoader,
  Connection,
} from '../index.js';
import * as connectMod from '../connect.js';
import { SplendorPQBridge } from '../splendor-wallet.js';

const GOLDEN_SEED = '01'.repeat(32);

const wallets: Wallet[] = [];
afterEach(() => {
  for (const w of wallets) {
    try {
      w.disconnect();
    } catch {
      /* ignore */
    }
  }
  wallets.length = 0;
  resetWasmLoader();
});

function track(w: Wallet) {
  wallets.push(w);
  return w;
}

describe('Phase 1 — canonical Wallet API', () => {
  it('createPublicClient returns a Connection', async () => {
    const c = createPublicClient({ network: 'local' });
    expect(c.chainId).toBeTruthy();
    expect(typeof c.getBalance).toBe('function');
  });

  it('createPublicClient does not allocate a Wallet', () => {
    const spy = vi.spyOn(connectMod, 'createSplendorClient');
    const c = createPublicClient({ network: 'local' });
    expect(c).toBeInstanceOf(Connection);
    expect(c instanceof SplendorPQBridge).toBe(false);
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it('createWalletClient + connect({ mode: seed }) unlocks account', async () => {
    const wallet = track(createWalletClient({ network: 'local', allowUnpinnedWasm: true }));
    expect(wallet.account).toBeNull();
    expect(wallet.getAddresses()).toEqual([]);

    const info = await wallet.connect({ mode: 'seed', seed: GOLDEN_SEED });
    expect(info.address).toMatch(/^SQ:/);
    expect(wallet.account?.address).toBe(info.address);
    expect(wallet.account?.postQuantumSecure).toBe(true);
    expect(wallet.getAddresses()).toEqual([info.address]);
  }, 30_000);

  it('notifyChainChanged emits chainChanged', async () => {
    const wallet = track(createWalletClient({ network: 'local', allowUnpinnedWasm: true }));
    const seen: Array<{ chainId: number; networkId: string }> = [];
    wallet.on('chainChanged', (p) => seen.push(p));
    wallet.notifyChainChanged({ chainId: 1, networkId: 'testnet' });
    expect(seen).toEqual([{ chainId: 1, networkId: 'testnet' }]);
  });

  it('signMessage domain-separates unstructured messages', async () => {
    const wallet = track(new Wallet({ networkId: 'local', allowUnpinnedWasm: true }));
    await wallet.connect({ mode: 'seed', seed: GOLDEN_SEED });

    const signed = await wallet.signMessage('hello dapp');
    expect(signed.message).toBe(`${SIGN_MESSAGE_DOMAIN}\nhello dapp`);
    const ok = await wallet.verifyMessage(signed.publicKeyB64, 'hello dapp', signed.signatureB64);
    expect(ok).toBe(true);

    await expect(wallet.signMessage('raw-bytes', { raw: true })).rejects.toThrow(/allowRawSign/);

    const rawWallet = track(new Wallet({ networkId: 'local', allowUnpinnedWasm: true, allowRawSign: true }));
    await rawWallet.connect({ mode: 'seed', seed: GOLDEN_SEED });
    const raw = await rawWallet.signMessage('raw-bytes', { raw: true });
    expect(raw.message).toBe('raw-bytes');
  }, 30_000);

  it('prepareSignMessage leaves only complete Connect v2 challenges untouched', () => {
    const challenge = 'Splendor Connect v2\nSign in to https://example.com\nIssued At: 1700000000';
    expect(prepareSignMessage(challenge)).toBe(challenge);
    const lookalike = 'Splendor Connect v2\nSign in to https://example.com';
    expect(prepareSignMessage(lookalike)).toBe(`${SIGN_MESSAGE_DOMAIN}\n${lookalike}`);
    expect(prepareSignMessage('Splendor Connect v1 anything')).toBe(
      `${SIGN_MESSAGE_DOMAIN}\nSplendor Connect v1 anything`,
    );
  });

  it('WalletError subclasses BridgeError with name WalletError', () => {
    const err = WalletError.notConnected();
    expect(isWalletError(err)).toBe(true);
    expect(err.name).toBe('WalletError');
    expect(err).toBeInstanceOf(WalletError);
  });
});
