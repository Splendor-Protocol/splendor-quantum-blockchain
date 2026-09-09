/**
 * 30-point audit — highest-ROI scenario coverage.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  MIN_FEE,
  SplendorPQBridge,
  Wallet,
  SqRpc,
  assertSafeRpcUrl,
  resetWasmLoader,
  loadWasmSigner,
  createSplendorProvider,
  fromUnits,
  WalletError,
  CHAIN_ID,
} from '../index.js';
import golden from '../../testdata/golden-vectors.json';

const GOLDEN_SEED = golden.seedHex;
const GOLDEN_ADDRESS = golden.address;
const originalFetch = globalThis.fetch;

const wallets: Array<SplendorPQBridge | Wallet> = [];

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
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

function track<T extends SplendorPQBridge | Wallet>(w: T): T {
  wallets.push(w);
  return w;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('audit scenarios', () => {
  it('1: confirmation does not hold sign lock — signMessage proceeds during wait', async () => {
    let confirmPolls = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/status')) return jsonResponse({ chain_id: CHAIN_ID });
      if (url.includes('/nonce')) return jsonResponse({ address: GOLDEN_ADDRESS, nonce: 1 });
      if (url.includes('/tx/submit')) {
        return jsonResponse({ status: 'ok', hash: 'aa'.repeat(32) });
      }
      if (url.includes('/receipt')) {
        confirmPolls += 1;
        if (confirmPolls < 4) {
          return jsonResponse({ status: 'pending' });
        }
        return jsonResponse({ status: 1 });
      }
      return jsonResponse({ error: `unexpected ${url}` }, 404);
    }) as typeof fetch;

    const w = track(
      new SplendorPQBridge({
        rpcUrl: 'http://rpc-confirm.test',
        chainId: CHAIN_ID,
        retries: 0,
        allowUnpinnedWasm: true,
      }),
    );
    await w.connectFromSeed(GOLDEN_SEED);

    const transferP = w.sendTransfer({
      to: GOLDEN_ADDRESS,
      amount: '1',
      fee: MIN_FEE,
      waitForConfirmation: true,
      confirmTimeoutMs: 5_000,
    });

    // While confirmation is polling, signMessage must not be blocked by the lock.
    await new Promise((r) => setTimeout(r, 30));
    const signed = await w.signMessage('during-confirm');
    expect(signed.signatureB64).toBeTruthy();

    await transferP;
  }, 60_000);

  it('2: parallel signTransaction transfers get distinct nonces', async () => {
    const nextNonce = 10;

    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/status')) return jsonResponse({ chain_id: CHAIN_ID });
      if (url.includes('/nonce')) {
        await new Promise((r) => setTimeout(r, 10));
        return jsonResponse({ address: GOLDEN_ADDRESS, nonce: nextNonce });
      }
      return jsonResponse({ error: `unexpected ${url}` }, 404);
    }) as typeof fetch;

    const w = track(
      new SplendorPQBridge({
        rpcUrl: 'http://rpc-nonce.test',
        chainId: CHAIN_ID,
        retries: 0,
        allowUnpinnedWasm: true,
      }),
    );
    await w.connectFromSeed(GOLDEN_SEED);

    const [a, b] = await Promise.all([
      w.signTransaction({
        type: 'transfer',
        to: GOLDEN_ADDRESS,
        amount: '1',
        fee: MIN_FEE,
      }),
      w.signTransaction({
        type: 'transfer',
        to: GOLDEN_ADDRESS,
        amount: '2',
        fee: MIN_FEE,
      }),
    ]);

    expect(a.nonce).not.toBe(b.nonce);
    expect(new Set([a.nonce, b.nonce])).toEqual(new Set([10, 11]));
  }, 60_000);

  it('3: second WASM load with different wasmSha256 rejects', async () => {
    resetWasmLoader();
    const first = track(new SplendorPQBridge({ allowUnpinnedWasm: true }));
    await first.init();

    await expect(loadWasmSigner({ wasmSha256: 'ff'.repeat(32) })).rejects.toMatchObject({
      code: 'WASM',
      message: expect.stringMatching(/different url\/pin/i),
    });
  }, 60_000);

  it('7: receipt status "failed" → TX_FAILED', async () => {
    const from = GOLDEN_ADDRESS;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/receipt')) {
        return jsonResponse({ status: 'failed', error: 'reverted' });
      }
      return jsonResponse({ address: from, nonce: 1 });
    }) as typeof fetch;

    const rpc = new SqRpc({ rpcUrl: 'http://rpc-fail.test', chainId: CHAIN_ID, retries: 0 });
    await expect(
      rpc.waitForConfirmation({
        from,
        nonceBefore: 0,
        hash: 'bb'.repeat(32),
        timeoutMs: 200,
        pollMs: 10,
      }),
    ).rejects.toMatchObject({ code: 'TX_FAILED' });
  });

  it('8: deploy defaults — human supply → raw via fromUnits(..., 18)', () => {
    expect(fromUnits('1000', 18)).toBe('1000' + '0'.repeat(18));
    expect(fromUnits('1.5', 18)).toBe('15' + '0'.repeat(17));
  });

  it('9: locked wallet — account null, sign → NOT_CONNECTED', async () => {
    const w = track(new Wallet({ networkId: 'local', allowUnpinnedWasm: true }));
    await w.connect({ mode: 'seed', seed: GOLDEN_SEED });
    expect(w.account?.address).toMatch(/^SQ:/);
    w.lock();
    expect(w.account).toBeNull();
    expect(w.isUnlocked()).toBe(false);
    await expect(w.signMessage('after-lock')).rejects.toMatchObject({
      code: 'NOT_CONNECTED',
    });
  }, 30_000);

  it('11: injected rejects seed connect and raw signMessage', async () => {
    const w = track(createWalletClientish());
    const provider = createSplendorProvider({
      wallet: w,
      networkId: 'local',
      onApprove: async () => true,
    });

    await expect(
      provider.request({
        method: 'splendor_connect',
        params: { mode: 'seed', seed: GOLDEN_SEED },
      }),
    ).rejects.toMatchObject({ code: 'USER_REJECTED' });

    await w.connect({ mode: 'seed', seed: GOLDEN_SEED });
    await expect(
      provider.request({
        method: 'splendor_signMessage',
        params: { message: 'x', raw: true },
      }),
    ).rejects.toMatchObject({ code: 'USER_REJECTED' });
  }, 30_000);

  it('6: splendor_switchNetwork without callback throws', async () => {
    const w = track(createWalletClientish());
    const provider = createSplendorProvider({
      wallet: w,
      networkId: 'local',
      onApprove: async () => true,
    });
    await expect(
      provider.request({
        method: 'splendor_switchNetwork',
        params: { networkId: 'testnet' },
      }),
    ).rejects.toThrow(/onSwitchNetwork/);
  });

  it('12: Node SqRpc rejects 169.254.169.254', () => {
    expect(() => assertSafeRpcUrl('http://169.254.169.254/latest/meta-data')).toThrow(
      /blocked|metadata/i,
    );
    expect(() => new SqRpc({ rpcUrl: 'http://169.254.169.254/', retries: 0 })).toThrow(
      /blocked|metadata/i,
    );
  });

  it('14/15: SqRpc GET coalescing + SignedTx networkId fail-closed', async () => {
    let hits = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/balance/')) {
        hits += 1;
        await new Promise((r) => setTimeout(r, 30));
        return jsonResponse({
          address: GOLDEN_ADDRESS,
          balance: '1',
          unit: 'wei',
        });
      }
      if (url.includes('/status')) return jsonResponse({ chain_id: CHAIN_ID });
      if (url.includes('/nonce')) return jsonResponse({ address: GOLDEN_ADDRESS, nonce: 0 });
      return jsonResponse({ error: `unexpected ${url}` }, 404);
    }) as typeof fetch;

    const rpc = new SqRpc({ rpcUrl: 'http://rpc-coal.test', chainId: CHAIN_ID, retries: 0 });
    const [a, b] = await Promise.all([
      rpc.getBalance(GOLDEN_ADDRESS),
      rpc.getBalance(GOLDEN_ADDRESS),
    ]);
    expect(a.balance).toBe('1');
    expect(b.balance).toBe('1');
    expect(hits).toBe(1);

    const w = track(
      new SplendorPQBridge({
        rpcUrl: 'http://rpc-chain.test',
        chainId: CHAIN_ID,
        networkId: 'local',
        retries: 0,
        allowUnpinnedWasm: true,
      }),
    );
    await w.connectFromSeed(GOLDEN_SEED);
    const signed = await w.signTransaction({
      type: 'transfer',
      to: GOLDEN_ADDRESS,
      amount: '1',
      fee: MIN_FEE,
    });
    expect(signed.networkId).toBe('local');
    await expect(
      w.sendTransaction({ ...signed, networkId: 'testnet' }),
    ).rejects.toMatchObject({ code: 'CHAIN_MISMATCH' });
    expect(WalletError.txFailed('x').name).toBe('WalletError');
  }, 60_000);
});

function createWalletClientish(): Wallet {
  return new Wallet({ networkId: 'local', allowUnpinnedWasm: true });
}
