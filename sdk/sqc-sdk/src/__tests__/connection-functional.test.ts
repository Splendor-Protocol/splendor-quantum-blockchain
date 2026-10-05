import { afterEach, describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { CHAIN_ID } from '../derive.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

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

const ADDR = golden.address;

describe('Connection functional (mocked RPC)', () => {
  it('getBalance / getNonce hit Splendor paths with SQ address', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/balance/')) {
        return jsonResponse({
          address: ADDR,
          balance: '1000',
          unit: 'atto',
        });
      }
      if (url.includes('/nonce/')) {
        return jsonResponse({ address: ADDR, nonce: 7 });
      }
      return jsonResponse({ error: 'unexpected ' + url }, 404);
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const conn = new Connection({
      rpcUrl: 'https://rpc.test',
      chainId: CHAIN_ID,
      retries: 0,
    });

    const bal = await conn.getBalance(ADDR);
    expect(bal.balance).toBe('1000');
    expect(bal.balance_spld).toBeTruthy();

    const nonce = await conn.getNonce(ADDR);
    expect(nonce.nonce).toBe(7);

    expect(String(fetchMock.mock.calls[0][0])).toContain('/balance/');
    expect(String(fetchMock.mock.calls[1][0])).toContain('/nonce/');
  });

  it('assertChainId delegates to RPC fail-closed', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({ chain_id: CHAIN_ID })) as typeof fetch;
    const conn = new Connection({ rpcUrl: 'https://rpc.test', chainId: CHAIN_ID, retries: 0 });
    await expect(conn.assertChainId()).resolves.toBeUndefined();
  });

  it('moduleCall POSTs /call with router envelope', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toContain('/call');
      const body = JSON.parse(String(init?.body ?? '{}'));
      expect(body.to).toMatch(/^SQ:/);
      expect(body.data || body.calldata_hex).toBeTruthy();
      return jsonResponse({
        decoded: { balance: '42' },
      });
    }) as typeof fetch;
    globalThis.fetch = fetchMock;

    const conn = new Connection({ rpcUrl: 'https://rpc.test', chainId: CHAIN_ID, retries: 0 });
    const res = await conn.moduleCall<{ balance: string }>({
      module: 'qrc20',
      action: 'balance_of',
      params: { token: ADDR, owner: ADDR },
    });
    expect(res.balance).toBe('42');
  });

  it('fromSqRpc shares rpc instance', () => {
    const conn = new Connection({ rpcUrl: 'https://rpc.test', chainId: CHAIN_ID });
    const again = Connection.fromSqRpc(conn.rpc);
    expect(again.rpc).toBe(conn.rpc);
    expect(again.chainId).toBe(CHAIN_ID);
  });
});
