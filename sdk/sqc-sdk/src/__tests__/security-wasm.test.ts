/**
 * SQDP-1 security: WASM session lock/disconnect, re-derive replaces keys, pin integrity, fee floor.
 * "Zeroize" here means lock prevents further signing and re-derive replaces the key binding —
 * tests assert unlock state / cannot-sign, not hardware memory forensics.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MIN_FEE, SplendorPQBridge, resetWasmLoader } from '../index.js';
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
        fee: MIN_FEE,
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
        fee: MIN_FEE,
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
      fee: MIN_FEE,
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
        fee: (BigInt(MIN_FEE) - 1n).toString(),
        nonce: 0,
        assertChain: false,
      }),
    ).rejects.toMatchObject({ code: 'INVALID_FEE' });
  }, 30_000);
});

describe('security-wasm raw signing', () => {
  it('rejects raw:true before touching the key unless allowRawSign is set', async () => {
    const b = track(new SplendorPQBridge());
    await expect(b.signMessage('raw-bytes', { raw: true })).rejects.toMatchObject({ code: 'USER_REJECTED' });
  });
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

  it('refuses a wasm_exec.js loaded by another script when only the .wasm is pinned', async () => {
    const { loadWasmInBrowser, resetBrowserWasmState } = await import('../wasm-loader.browser.js');
    const g = globalThis as unknown as { Go?: unknown; sqCreateSession?: unknown };
    const prev = { Go: g.Go, sqCreateSession: g.sqCreateSession };
    // Earlier tests in this file loaded the Node signer, which registers its functions globally.
    delete g.sqCreateSession;
    g.Go = function ForeignGo() {};
    resetBrowserWasmState();
    try {
      await expect(loadWasmInBrowser({ wasmSha256: 'ab'.repeat(32) })).rejects.toMatchObject({
        code: 'WASM',
        message: expect.stringContaining('execSha256'),
      });
    } finally {
      g.Go = prev.Go;
      if (prev.sqCreateSession !== undefined) g.sqCreateSession = prev.sqCreateSession;
      resetBrowserWasmState();
    }
  });

  it('turns a hex pin into a sha256 SRI token and rejects malformed pins', async () => {
    const { sriFromHex } = await import('../wasm-loader.browser.js');
    // SHA-256 of the empty string.
    expect(sriFromHex('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')).toBe(
      'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=',
    );
    expect(sriFromHex('0xE3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855')).toBe(
      'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=',
    );
    expect(() => sriFromHex('abc')).toThrow(/64-character/);
  });

  it('loads a pinned wasm_exec.js with a <script integrity> tag instead of eval', async () => {
    const { loadWasmInBrowser, resetBrowserWasmState } = await import('../wasm-loader.browser.js');
    const g = globalThis as unknown as { Go?: unknown; sqCreateSession?: unknown; document?: unknown };
    const prev = { Go: g.Go, sqCreateSession: g.sqCreateSession, document: g.document };
    delete g.sqCreateSession;
    delete g.Go;
    const appended: Record<string, unknown>[] = [];
    g.document = {
      head: {
        appendChild(el: Record<string, unknown> & { onload?: () => void }) {
          appended.push(el);
          // The browser ran the (integrity-checked) script: it defines Go.
          g.Go = function Go() {
            return { importObject: {}, run() {} };
          };
          el.onload?.();
        },
      },
      createElement: () => ({}),
    };
    resetBrowserWasmState();
    const fetchSpy = vi.fn(async () => new Response('x', { status: 404 }));
    vi.stubGlobal('fetch', fetchSpy);
    try {
      // The .wasm fetch fails afterwards; what matters is how wasm_exec.js was loaded.
      await expect(
        loadWasmInBrowser({ execSha256: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', wasmSha256: 'ab'.repeat(32) }),
      ).rejects.toMatchObject({ code: 'WASM', message: expect.stringContaining('Failed to fetch WASM') });
      expect(appended).toHaveLength(1);
      expect(appended[0]).toMatchObject({ src: '/wasm_exec.js', integrity: 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=' });
      // Only the .wasm was fetched; wasm_exec.js never went through fetch + new Function.
      expect(fetchSpy.mock.calls.map((c) => String((c as unknown[])[0]))).toEqual(['/sqc_signer.wasm']);
    } finally {
      vi.unstubAllGlobals();
      g.Go = prev.Go;
      g.document = prev.document;
      if (prev.document === undefined) delete g.document;
      if (prev.sqCreateSession !== undefined) g.sqCreateSession = prev.sqCreateSession;
      resetBrowserWasmState();
    }
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
