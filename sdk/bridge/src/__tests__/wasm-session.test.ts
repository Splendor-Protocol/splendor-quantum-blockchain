import { describe, expect, it, afterEach } from 'vitest';
import { SplendorPQBridge } from '../index.js';

const GOLDEN_SEED = '01'.repeat(32);
const GOLDEN_ADDRESS =
  'SQ:c3ee306a4d69ecdaf2a43837ae475a54857578533bb919740a5e4dfe9579e44e';

const bridges: SplendorPQBridge[] = [];

afterEach(() => {
  for (const b of bridges) {
    try {
      b.disconnect();
    } catch {
      /* ignore */
    }
  }
  bridges.length = 0;
});

function track(b: SplendorPQBridge) {
  bridges.push(b);
  return b;
}

describe('WASM offline integration', () => {
  it('derives golden address from fixed seed', async () => {
    const b = track(new SplendorPQBridge());
    await b.init();
    const info = await b.connectFromSeed(GOLDEN_SEED);
    expect(info.address).toBe(GOLDEN_ADDRESS);
    expect(b.getConnectionState()).toBe('connected');
    expect(b.isUnlocked()).toBe(true);
  }, 30_000);

  it('signs transfer with wire-compatible from array and pubkey on nonce 0', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const signed = (await b.signTransferOnly({
      to: GOLDEN_ADDRESS,
      amount: '1000',
      fee: '1000',
      nonce: 0,
      assertChain: false,
    })) as {
      from: number[];
      public_key: string;
      signature: string;
    };
    expect(Array.isArray(signed.from)).toBe(true);
    expect(signed.from).toHaveLength(32);
    expect(signed.public_key).toBeTruthy();
    expect(signed.signature).toBeTruthy();
  }, 30_000);

  it('isolates sessions between two bridge instances', async () => {
    const a = track(new SplendorPQBridge());
    const b = track(new SplendorPQBridge());
    await a.connectFromSeed(GOLDEN_SEED);
    await b.connectFromSeed('02'.repeat(32));

    expect(a.getAddress()).toBe(GOLDEN_ADDRESS);
    expect(b.getAddress()).not.toBe(a.getAddress());

    const sa = (await a.signTransferOnly({
      to: a.getAddress(),
      amount: '1',
      fee: '1000',
      nonce: 0,
      assertChain: false,
    })) as { from: number[] };
    const sb = (await b.signTransferOnly({
      to: b.getAddress(),
      amount: '1',
      fee: '1000',
      nonce: 0,
      assertChain: false,
    })) as { from: number[] };

    expect(sa.from).not.toEqual(sb.from);
    // After signing, addresses must still be distinct (no clobber)
    expect(a.getAddress()).not.toBe(b.getAddress());
  }, 60_000);

  it('disconnect clears connection state', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    b.disconnect();
    expect(b.getConnectionState()).toBe('disconnected');
    expect(() => b.getAddress()).toThrow();
  }, 30_000);
});
