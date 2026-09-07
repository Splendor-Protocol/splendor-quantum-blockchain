/**
 * SQDP-1 security: WASM session lock/disconnect, re-derive Zeroize, pin integrity, fee floor.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { SplendorPQBridge, resetWasmLoader } from '../index.js';
import { BridgeError, isBridgeError } from '../errors.js';
import { assertWasmSha256 } from '../wasm-types.js';
import golden from '../../testdata/golden-vectors.json';

const GOLDEN_SEED = golden.seedHex;
const GOLDEN_ADDRESS = golden.address;
const OTHER_SEED = '02'.repeat(32);

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

describe('security-wasm session lifecycle', () => {
  it('lock prevents signing; disconnect invalidates session', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    expect(b.getAddress()).toBe(GOLDEN_ADDRESS);
    b.lock();
    expect(b.isUnlocked()).toBe(false);
    expect(b.getConnectionState()).toBe('disconnected');
    await expect(
      b.signTransferOnly({
        to: GOLDEN_ADDRESS,
        amount: '1',
        fee: '1000',
        nonce: 0,
        assertChain: false,
      }),
    ).rejects.toThrow();

    await b.connectFromSeed(GOLDEN_SEED);
    b.disconnect();
    expect(b.getConnectionState()).toBe('disconnected');
    await expect(
      b.signTransferOnly({
        to: GOLDEN_ADDRESS,
        amount: '1',
        fee: '1000',
        nonce: 0,
        assertChain: false,
      }),
    ).rejects.toThrow();
  }, 30_000);

  it('re-derive on same session replaces key (old address cannot sign)', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    expect(b.getAddress()).toBe(GOLDEN_ADDRESS);

    await b.connectFromSeed(OTHER_SEED);
    const next = b.getAddress();
    expect(next).not.toBe(GOLDEN_ADDRESS);

    const signed = (await b.signTransferOnly({
      to: next,
      amount: '1',
      fee: '1000',
      nonce: 0,
      assertChain: false,
    })) as { from: number[] };
    // Wire `from` is address bytes — must match new wallet, not golden.
    const fromHex = signed.from.map((n) => n.toString(16).padStart(2, '0')).join('');
    expect(fromHex).toBe(next.slice(3).toLowerCase());
    expect(fromHex).not.toBe(GOLDEN_ADDRESS.slice(3).toLowerCase());
  }, 30_000);

  it('rejects fee below MIN_FEE on transfer path', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    await expect(
      b.signTransferOnly({
        to: GOLDEN_ADDRESS,
        amount: '1',
        fee: '999',
        nonce: 0,
        assertChain: false,
      }),
    ).rejects.toMatchObject({ code: 'INVALID_FEE' });
  }, 30_000);
});

describe('security-wasm integrity pin', () => {
  it('rejects invalid wasmSha256 pin format', async () => {
    await expect(assertWasmSha256(new ArrayBuffer(8), 'not-a-hash')).rejects.toMatchObject({
      code: 'WASM',
    });
  });

  it('fails closed on wasmSha256 mismatch', async () => {
    const buf = new TextEncoder().encode('not-the-real-wasm').buffer;
    const wrongPin = '00'.repeat(32);
    await expect(assertWasmSha256(buf, wrongPin)).rejects.toMatchObject({
      code: 'WASM',
      message: expect.stringContaining('integrity check failed'),
    });
  });

  it('SplendorPQBridge init fails closed on wrong pin', async () => {
    resetWasmLoader();
    const b = track(
      new SplendorPQBridge({
        wasmSha256: 'ff'.repeat(32),
      }),
    );
    try {
      await b.init();
      expect.fail('expected WASM integrity failure');
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('WASM');
    } finally {
      resetWasmLoader();
    }
  }, 30_000);
});
