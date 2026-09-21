/**
 * Regressions for four defects an independent audit of the reuse bound found.
 *
 * Each of them defeated the bound in a different way: by re-spelling the payment
 * so it looked like a new one, by letting a bystander hand the reservation back,
 * by outliving the record that counts the reuse, and by a thrown settle call
 * keeping a grant nobody was served.
 */
import { describe, expect, it, vi } from 'vitest';
import {
  createMemoryConsumptionStore,
  paymentIdentity,
  paymentOutlivesConsumption,
  X402_DEFAULT_MAX_GRANTS,
  type X402ClaimOutcome,
} from '../x402/consumption.js';

/** The memory store is synchronous; this keeps the types honest at call sites. */
function claimNow(
  s: ReturnType<typeof createMemoryConsumptionStore>,
  id: string,
  binding: string,
  opts: { now: number; expiresAt: number },
): Extract<X402ClaimOutcome, { status: string }> {
  return s.claim(id, binding, opts) as Extract<X402ClaimOutcome, { status: string }>;
}

const HEX = 'ab'.repeat(32);
const ADDR = 'SQ:' + 'cd'.repeat(32);
const T0 = 1_700_000_000_000;

describe('one payment has one identity', () => {
  it('is unchanged by the spellings the chain treats as equal', () => {
    const canonical = paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: HEX });

    // The chain hashes DECODED bytes, and hex decoding is case-insensitive on
    // both fields; the SDK's own hexToBytes also strips 0x. Every one of these
    // is the same payment on chain, and each used to get its own record — and
    // with it a fresh, full reuse allowance.
    for (const variant of [
      { chain_id: 'sq-1', from: ADDR, nonce: HEX.toUpperCase() },
      { chain_id: 'sq-1', from: ADDR, nonce: '0x' + HEX },
      { chain_id: 'sq-1', from: ADDR, nonce: '0X' + HEX.toUpperCase() },
      { chain_id: 'sq-1', from: ADDR, nonce: ' ' + HEX + ' ' },
      { chain_id: 'sq-1', from: 'SQ:' + 'CD'.repeat(32), nonce: HEX },
      { chain_id: ' sq-1 ', from: ADDR, nonce: HEX },
    ]) {
      expect(paymentIdentity(variant)).toBe(canonical);
    }

    // Genuinely different payments stay different.
    expect(paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: 'cd'.repeat(32) })).not.toBe(canonical);
    expect(paymentIdentity({ chain_id: 'sq-2', from: ADDR, nonce: HEX })).not.toBe(canonical);
    expect(paymentIdentity({ chain_id: 'sq-1', from: 'SQ:' + 'ef'.repeat(32), nonce: HEX })).not.toBe(canonical);
  });

  it('refuses a spelling it cannot canonicalise rather than inventing a payment', () => {
    expect(() => paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: 'ab' })).toThrow(/32 bytes of hex/);
    expect(() => paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: 'zz'.repeat(32) })).toThrow(/hex/);
    expect(() => paymentIdentity({ chain_id: 'sq-1', from: 'cd'.repeat(32), nonce: HEX })).toThrow(/SQ: address/);
    // The separator, which would let two payments agree on one identity.
    expect(() => paymentIdentity({ chain_id: 'sq|1', from: ADDR, nonce: HEX })).toThrow(/"\|"/);
  });
});

describe('only the holder of a reservation can give it back', () => {
  const PAYMENT = paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: HEX });

  it('ignores a release from a request that never reserved the payment', () => {
    const s = createMemoryConsumptionStore({});
    const opts = { now: T0, expiresAt: T0 + 3_600_000 };

    const first = claimNow(s, PAYMENT, 'GET /feed', opts);
    if (first.status !== 'claimed') throw new Error('expected a claim');
    expect(first.status).toBe('claimed');

    // A second request with the same binding, arriving while the first is still
    // settling: a replay with its own in-flight token.
    const second = claimNow(s, PAYMENT, 'GET /feed', opts);
    expect(second.status).toBe('replay');
    if (second.status !== 'replay') throw new Error('expected a replay');

    // Its own settle attempt fails and it tries to release. It must not be able
    // to drop the reservation the first request is still relying on — doing so
    // handed the payment to a different binding mid-flight.
    s.release(PAYMENT, undefined);
    s.release(PAYMENT, 'not-the-token');
    expect(claimNow(s, PAYMENT, 'GET /premium', opts).status).toBe('conflict');

    // Releasing one holder keeps the other reservation. The payment becomes
    // claimable elsewhere only after every in-flight attempt has failed.
    s.release(PAYMENT, first.token);
    expect(claimNow(s, PAYMENT, 'GET /premium', opts).status).toBe('conflict');
    s.release(PAYMENT, second.token);
    expect(claimNow(s, PAYMENT, 'GET /premium', opts).status).toBe('claimed');
  });

  it('does not let a stale token from an earlier claim release a later one', () => {
    const s = createMemoryConsumptionStore({});
    const opts = { now: T0, expiresAt: T0 + 3_600_000 };

    const first = claimNow(s, PAYMENT, 'GET /feed', opts);
    if (first.status !== 'claimed') throw new Error('expected a claim');
    s.release(PAYMENT, first.token);
    const second = claimNow(s, PAYMENT, 'GET /other', opts);
    if (second.status !== 'claimed') throw new Error('expected a claim');
    expect(second.status).toBe('claimed');
    expect(second.token).not.toBe(first.token);

    s.release(PAYMENT, first.token);
    expect(claimNow(s, PAYMENT, 'GET /third', opts).status).toBe('conflict');
  });
});

describe('a payment may not outlive the record that polices it', () => {
  it('spots a window longer than the retention cap', () => {
    const now = T0;
    const secs = (ms: number) => Math.floor(ms / 1000);

    // What the client actually signs: five minutes.
    expect(paymentOutlivesConsumption({ valid_before: secs(now) + 300 }, now)).toBe(false);
    // Comfortably inside a day.
    expect(paymentOutlivesConsumption({ valid_before: secs(now) + 23 * 3600 }, now)).toBe(false);
    // Past the cap: the record would expire while the payment was still good,
    // and the next presentation would buy another full allowance — repeating
    // every retention period for as long as the signature window ran.
    expect(paymentOutlivesConsumption({ valid_before: secs(now) + 25 * 3600 }, now)).toBe(true);
    expect(paymentOutlivesConsumption({ valid_before: 9e9 }, now)).toBe(true);
    // No window at all is not a long window.
    expect(paymentOutlivesConsumption({}, now)).toBe(false);
    expect(paymentOutlivesConsumption({ valid_before: 0 }, now)).toBe(false);
  });

  it('is refused by the middleware, not silently accepted', async () => {
    const { createX402Middleware } = await import('../x402/client.js');
    const PAY_TO = 'SQ:' + 'aa'.repeat(32);
    const connection = {
      chainId: 'splendor-quantum-1',
      x402Verify: vi.fn(async () => ({ valid: true })),
      x402Settle: vi.fn(async () => ({ success: true, status: 'confirmed', tx_hash: 'aa'.repeat(32) })),
      waitForConfirmation: vi.fn(async () => ({ confirmed: true as const, nonce: 1 })),
    } as never;
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    const res = {
      statusCode: 0,
      body: undefined as unknown,
      status(code: number) {
        this.statusCode = code;
        return this;
      },
      json(b: unknown) {
        this.body = b;
        return this;
      },
    };
    const next = vi.fn();
    const header = {
      'x-payment': JSON.stringify({
        chain_id: 'splendor-quantum-1',
        from: PAY_TO,
        to: PAY_TO,
        amount: '10000000000000000',
        valid_after: 0,
        valid_before: 9e9, // valid until the year 2255
        nonce: HEX,
        signature: 'YQ==',
        settlement_tx: { type: 5, chain_id: 'splendor-quantum-1' },
      }),
    };
    await mw({ path: '/feed', method: 'GET', headers: header } as never, res as never, next);

    expect(next).not.toHaveBeenCalled();
    expect(res.statusCode).toBe(402);
    expect(res.body).toMatchObject({ error: 'Payment Window Too Long' });
    // And nothing was settled for it: the refusal is at the door.
    expect((connection as unknown as { x402Settle: { mock: { calls: unknown[] } } }).x402Settle.mock.calls)
      .toHaveLength(0);
  });

  it('agrees with the retention the store is actually given', () => {
    // The two numbers are the same policy seen from two sides, so a caller that
    // raises one must raise the other: a window accepted here has to be one the
    // record outlives.
    const now = T0;
    const secs = Math.floor(now / 1000);
    const maxMs = 2 * 3_600_000;
    expect(paymentOutlivesConsumption({ valid_before: secs + 3 * 3600 }, now, { maxMs })).toBe(true);
    expect(paymentOutlivesConsumption({ valid_before: secs + 3600 }, now, { maxMs })).toBe(false);
  });
});

describe('a settle call that throws costs the payer nothing', () => {
  const PAY_TO = 'SQ:' + 'aa'.repeat(32);

  function harness(settle: () => Promise<unknown>) {
    const res = {
      statusCode: 0,
      body: undefined as unknown,
      status(code: number) {
        this.statusCode = code;
        return this;
      },
      json(b: unknown) {
        this.body = b;
        return this;
      },
    };
    return { res, next: vi.fn(), settle };
  }

  it('releases the reservation and says what went wrong', async () => {
    const { createX402Middleware } = await import('../x402/client.js');
    const boom = new Error('connect ECONNREFUSED 127.0.0.1:8545');
    const connection = {
      chainId: 'splendor-quantum-1',
      x402Verify: vi.fn(async () => ({ valid: true })),
      x402Settle: vi.fn(async () => {
        throw boom;
      }),
      waitForConfirmation: vi.fn(async () => ({ confirmed: true as const, nonce: 1 })),
    } as never;

    const store = createMemoryConsumptionStore({});
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      consumptionStore: store,
    });

    const now = Date.now();
    const header = {
      'x-payment': JSON.stringify({
        chain_id: 'splendor-quantum-1',
        from: PAY_TO,
        to: PAY_TO,
        amount: '10000000000000000',
        valid_after: 0,
        valid_before: Math.floor(now / 1000) + 300,
        nonce: HEX,
        signature: 'YQ==',
        settlement_tx: { type: 5, chain_id: 'splendor-quantum-1' },
      }),
    };

    const h = harness(async () => ({}));
    await mw({ path: '/feed', method: 'GET', headers: header } as never, h.res as never, h.next);

    // Not served, and not blamed on the caller's header.
    expect(h.next).not.toHaveBeenCalled();
    expect(h.res.statusCode).toBe(502);
    expect(h.res.body).toMatchObject({ error: 'Settlement Unavailable' });
    expect(JSON.stringify(h.res.body)).not.toContain('Malformed');

    // And the allowance is intact: the payment is spendable on a different
    // binding, which it would not be if the claim had survived.
    const opts = { now: Date.now(), expiresAt: Date.now() + 600_000 };
    const id = paymentIdentity({ chain_id: 'splendor-quantum-1', from: PAY_TO, nonce: HEX });
    expect(claimNow(store, id, 'GET /other', opts).status).toBe('claimed');
  });

  it('still bounds reuse after a recovered failure', () => {
    // A released claim starts the allowance over, which is correct — the payer
    // was never served — but it must still be an allowance.
    const s = createMemoryConsumptionStore({});
    const opts = { now: T0, expiresAt: T0 + 600_000 };
    const id = paymentIdentity({ chain_id: 'sq-1', from: ADDR, nonce: HEX });
    const first = claimNow(s, id, 'GET /feed', opts);
    if (first.status !== 'claimed') throw new Error('expected a claim');
    s.release(id, first.token);

    let served = 0;
    for (let i = 0; i < X402_DEFAULT_MAX_GRANTS + 4; i++) {
      const o = claimNow(s, id, 'GET /feed', opts);
      if (o.status === 'claimed' || o.status === 'replay') served++;
    }
    expect(served).toBe(X402_DEFAULT_MAX_GRANTS);
  });
});
