/**
 * SQDP-1 security: assertChainId fail-closed, submit chain_id, RPC failover/sticky.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SqRpc } from '../rpc.js';
import { BridgeError, isBridgeError } from '../errors.js';
import { CHAIN_ID } from '../derive.js';
import golden from '../../testdata/golden-vectors.json';

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

describe('security-rpc assertChainId', () => {
  it('fails closed when chain_id is missing', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({})) as typeof fetch;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc-a.test', chainId: CHAIN_ID, retries: 0 });
    await expect(rpc.assertChainId()).rejects.toMatchObject({
      code: 'CHAIN_MISMATCH',
    });
  });

  it('fails closed when chain_id is empty', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({ chain_id: '' })) as typeof fetch;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc-a.test', chainId: CHAIN_ID, retries: 0 });
    await expect(rpc.assertChainId()).rejects.toThrow(BridgeError);
    try {
      await rpc.assertChainId();
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('CHAIN_MISMATCH');
      expect((e as BridgeError).message).toContain('(missing)');
    }
  });

  it('throws on mismatch with expected vs actual', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResponse({ chain_id: 'other-chain' }),
    ) as typeof fetch;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc-a.test', chainId: CHAIN_ID, retries: 0 });
    await expect(rpc.assertChainId()).rejects.toMatchObject({
      code: 'CHAIN_MISMATCH',
      message: expect.stringContaining('other-chain'),
    });
  });

  it('passes when chain_id matches', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResponse({ chain_id: golden.chainId }),
    ) as typeof fetch;
    const rpc = new SqRpc({
      rpcUrl: 'http://rpc-a.test',
      chainId: golden.chainId,
      retries: 0,
    });
    await expect(rpc.assertChainId()).resolves.toBeUndefined();
  });
});

describe('security-rpc submitTransaction', () => {
  it('rejects foreign chain_id client-side before POST body accept', async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ status: 'ok', hash: 'h' }),
    ) as typeof fetch;
    globalThis.fetch = fetchMock;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc-a.test', chainId: CHAIN_ID, retries: 0 });
    await expect(
      rpc.submitTransaction({ chain_id: 'evil-chain', type: 'transfer' }),
    ).rejects.toMatchObject({ code: 'CHAIN_MISMATCH' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects missing chain_id fail-closed', async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ status: 'ok', hash: 'h' }),
    ) as typeof fetch;
    globalThis.fetch = fetchMock;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc-a.test', chainId: CHAIN_ID, retries: 0 });
    await expect(
      rpc.submitTransaction({ type: 'transfer', amount: '1' }),
    ).rejects.toMatchObject({ code: 'CHAIN_MISMATCH', message: expect.stringContaining('(missing)') });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('security-rpc failover and sticky submit', () => {
  it('fails over from 5xx primary to secondary URL', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('http://rpc-bad.test')) {
        return jsonResponse({ error: 'down' }, 503);
      }
      return jsonResponse({ chain_id: CHAIN_ID });
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const rpc = new SqRpc({
      rpcUrls: ['http://rpc-bad.test', 'http://rpc-good.test'],
      chainId: CHAIN_ID,
      retries: 0,
    });
    await rpc.assertChainId();
    expect(fetchMock).toHaveBeenCalled();
    const urls = (fetchMock as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.startsWith('http://rpc-good.test'))).toBe(true);
  });

  it('does not failover on 4xx from URL A to URL B', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('http://rpc-a.test')) {
        return jsonResponse({ error: 'bad request' }, 400);
      }
      return jsonResponse({ chain_id: CHAIN_ID });
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const rpc = new SqRpc({
      rpcUrls: ['http://rpc-a.test', 'http://rpc-b.test'],
      chainId: CHAIN_ID,
      retries: 0,
    });
    await expect(rpc.assertChainId()).rejects.toMatchObject({ code: 'RPC' });
    const urls = (fetchMock as ReturnType<typeof vi.fn>).mock.calls.map((c) => String(c[0]));
    expect(urls.every((u) => u.startsWith('http://rpc-a.test'))).toBe(true);
    expect(urls.some((u) => u.startsWith('http://rpc-b.test'))).toBe(false);
  });

  it('does not set sticky URL on successful read', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('http://rpc-a.test')) {
        return jsonResponse({ error: 'down' }, 503);
      }
      return jsonResponse({ chain_id: CHAIN_ID });
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const rpc = new SqRpc({
      rpcUrls: ['http://rpc-a.test', 'http://rpc-b.test'],
      chainId: CHAIN_ID,
      retries: 0,
    });
    await rpc.assertChainId();
    // Read failover succeeded on B, but sticky only applies after submit.
    expect(rpc.rpcUrl).toBe('http://rpc-a.test');
  });

  it('sticks submit to accepting URL', async () => {
    let submitHits = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/tx/submit')) {
        submitHits += 1;
        if (url.startsWith('http://rpc-a.test')) {
          return jsonResponse({ error: 'busy' }, 503);
        }
        return jsonResponse({ status: 'ok', hash: 'abc' });
      }
      if (url.startsWith('http://rpc-a.test')) {
        return jsonResponse({ error: 'down' }, 503);
      }
      return jsonResponse({ ok: true });
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const rpc = new SqRpc({
      rpcUrls: ['http://rpc-a.test', 'http://rpc-b.test'],
      chainId: CHAIN_ID,
      retries: 0,
    });
    const out = await rpc.submitTransaction({
      type: 'transfer',
      amount: '1',
      chain_id: CHAIN_ID,
    });
    expect(out.hash).toBe('abc');
    expect(submitHits).toBeGreaterThanOrEqual(1);
    expect(rpc.rpcUrl).toBe('http://rpc-b.test');

    await rpc.submitTransaction({ type: 'transfer', amount: '2', chain_id: CHAIN_ID });
    const submitUrls = (fetchMock as ReturnType<typeof vi.fn>).mock.calls
      .map((c) => String(c[0]))
      .filter((u) => u.endsWith('/tx/submit'));
    expect(submitUrls[submitUrls.length - 1].startsWith('http://rpc-b.test')).toBe(true);
  });
});
