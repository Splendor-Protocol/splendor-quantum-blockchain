/**
 * SQDP-1 security: per-wallet sign/submit mutex (no double-spent nonce).
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MIN_FEE, SplendorPQBridge } from '../index.js';
import { CHAIN_ID } from '../derive.js';
import golden from '../../testdata/golden-vectors.json';

const GOLDEN_SEED = golden.seedHex;
const GOLDEN_ADDRESS = golden.address;
const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('security-mutex concurrent sendTransfer', () => {
  it('serializes parallel sends so nonces are unique', async () => {
    let nextNonce = 5;
    const submitted: number[] = [];

    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/status')) {
        return jsonResponse({ chain_id: CHAIN_ID });
      }
      if (url.includes('/nonce')) {
        await new Promise((r) => setTimeout(r, 40));
        return jsonResponse({ address: GOLDEN_ADDRESS, nonce: nextNonce });
      }
      if (url.includes('/tx/submit')) {
        const body = JSON.parse(String(init?.body || '{}')) as { nonce?: number };
        const n = Number(body.nonce);
        submitted.push(n);
        nextNonce = n + 1;
        return jsonResponse({ status: 'ok', hash: `h${n}` });
      }
      return jsonResponse({ error: `unexpected ${url}` }, 404);
    }) as typeof fetch;

    const b = new SplendorPQBridge({
      rpcUrl: 'http://rpc-mutex.test',
      chainId: CHAIN_ID,
      retries: 0,
    });
    await b.connectFromSeed(GOLDEN_SEED);

    await Promise.all([
      b.sendTransfer({ to: GOLDEN_ADDRESS, amount: '1', fee: MIN_FEE }),
      b.sendTransfer({ to: GOLDEN_ADDRESS, amount: '2', fee: MIN_FEE }),
    ]);

    expect(submitted).toEqual([5, 6]);
    b.disconnect();
  }, 60_000);
});
