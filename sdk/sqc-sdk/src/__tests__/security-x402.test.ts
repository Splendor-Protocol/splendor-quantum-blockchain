import { describe, expect, it, vi } from 'vitest';
import { createMemoryConsumptionStore, createX402Middleware, resolvePrice } from '../x402/client.js';
import type { Connection } from '../core/connection.js';

const PAY_TO = 'SQ:' + 'aa'.repeat(32);
const PAYER = 'SQ:' + 'bb'.repeat(32);

function connection(): Connection {
  return {
    chainId: 'splendor-quantum-1',
    x402Verify: vi.fn(async () => ({ valid: true })),
    x402Settle: vi.fn(async () => ({ success: true, status: 'confirmed', tx_hash: 'aa'.repeat(32) })),
    waitForConfirmation: vi.fn(async () => ({ confirmed: true as const, nonce: 1 })),
  } as unknown as Connection;
}

function header(overrides: Record<string, unknown> = {}) {
  return {
    'x-payment': JSON.stringify({
      chain_id: 'splendor-quantum-1',
      from: PAYER,
      to: PAY_TO,
      amount: '10000000000000000',
      valid_after: 0,
      valid_before: 9e9,
      nonce: 'ab'.repeat(32),
      signature: 'YQ==',
      settlement_tx: { type: 5, chain_id: 'splendor-quantum-1' },
      ...overrides,
    }),
  };
}

async function call(mw: ReturnType<typeof createX402Middleware>, path: string, headers: Record<string, string>) {
  const json = vi.fn();
  const res = { status: vi.fn(() => res), json };
  const next = vi.fn();
  await mw({ path, headers }, res as never, next);
  return { status: res.status, json, next };
}

describe('x402 middleware hardening', () => {
  it('rejects a replay of a consumed payment on another path (default in-memory store)', async () => {
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection: connection() });
    expect((await call(mw, '/a', header())).next).toHaveBeenCalled();
    const replay = await call(mw, '/b', header());
    expect(replay.next).not.toHaveBeenCalled();
    expect(replay.json).toHaveBeenCalledWith(expect.objectContaining({ error: 'Payment Already Consumed' }));
  });

  it('checks recipient, amount and chain locally before calling the node', async () => {
    const conn = connection();
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection: conn });
    const cases: [Record<string, unknown>, RegExp][] = [
      [{ to: PAYER }, /recipient/],
      [{ amount: '1' }, /below the price/],
      [{ amount: '1e18' }, /below the price/],
      [{ chain_id: 'other-chain' }, /chain_id/],
      [{ nonce: '' }, /from\/nonce/],
      [{ from: 'nope' }, /from\/nonce/],
    ];
    for (const [patch, reason] of cases) {
      const r = await call(mw, '/api', header(patch));
      expect(r.next).not.toHaveBeenCalled();
      expect(r.status).toHaveBeenCalledWith(402);
      expect(r.json).toHaveBeenCalledWith(expect.objectContaining({ reason: expect.stringMatching(reason) }));
    }
    expect(conn.x402Verify).not.toHaveBeenCalled();
  });

  it('refuses unpriced paths with 403', async () => {
    const mw = createX402Middleware({ payTo: PAY_TO, price: { '/paid': '0.01' }, connection: connection() });
    const r = await call(mw, '/free-for-all', header());
    expect(r.status).toHaveBeenCalledWith(403);
    expect(r.next).not.toHaveBeenCalled();
  });

  it('rejects an empty price map at construction', () => {
    expect(() => createX402Middleware({ payTo: PAY_TO, price: {}, connection: connection() })).toThrow();
  });
});

describe('resolvePrice', () => {
  const prices = { '/api': '0.01', '/api/premium': '1', '/api/premium/': '2' };

  it('prefers exact matches, then the longest segment prefix', () => {
    expect(resolvePrice(prices, '/api')).toBe('0.01');
    expect(resolvePrice(prices, '/api/x')).toBe('0.01');
    expect(resolvePrice(prices, '/api/premium')).toBe('1');
    expect(resolvePrice(prices, '/api/premium/report')).toBe('2');
  });

  it('does not match partial segments or unknown paths', () => {
    expect(resolvePrice(prices, '/apix')).toBeNull();
    expect(resolvePrice(prices, '/other')).toBeNull();
    expect(resolvePrice({ '/a': '1' }, 'constructor')).toBeNull();
    expect(resolvePrice('0.5', '/anything')).toBe('0.5');
  });
});

describe('createMemoryConsumptionStore', () => {
  it('putIfAbsent is first-writer-wins and the store stays bounded', async () => {
    const store = createMemoryConsumptionStore(2);
    expect(await store.putIfAbsent!('k', '/a')).toBe(true);
    expect(await store.putIfAbsent!('k', '/b')).toBe(false);
    expect(await store.get('k')).toBe('/a');
    await store.put('k2', 'x');
    await store.put('k3', 'y');
    expect(await store.get('k')).toBeUndefined();
  });
});
