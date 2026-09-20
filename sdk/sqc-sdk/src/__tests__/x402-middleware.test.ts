import { afterEach, describe, expect, it, vi } from 'vitest';
import { createX402Middleware } from '../x402/client.js';
import {
  createMemoryConsumptionStore,
  X402_DEFAULT_MAX_GRANTS,
} from '../x402/consumption.js';
import type { Connection } from '../core/connection.js';

const PAY_TO = 'SQ:' + 'aa'.repeat(32);

function mockConnection(opts: {
  verifyValid?: boolean;
  nonceUsed?: boolean;
  settleStatus?: 'submitted' | 'confirmed' | 'failed';
  settleSuccess?: boolean;
  wait?: boolean | Error;
}): Connection {
  const settleStatus =
    opts.settleStatus ?? (opts.settleSuccess === false ? 'failed' : 'submitted');
  const settleSuccess = settleStatus === 'confirmed';
  return {
    chainId: 'splendor-quantum-1',
    x402Verify: vi.fn(async () => ({
      valid: opts.verifyValid !== false && !opts.nonceUsed,
      reason: opts.nonceUsed ? 'payment nonce already used' : undefined,
      nonce_used: opts.nonceUsed === true,
    })),
    x402Settle: vi.fn(async () =>
      settleStatus === 'failed'
        ? { success: false, status: 'failed', error: 'settle failed' }
        : { success: settleSuccess, status: settleStatus, tx_hash: 'aa'.repeat(32) },
    ),
    waitForConfirmation:
      opts.wait instanceof Error
        ? vi.fn(async () => {
            throw opts.wait;
          })
        : opts.wait === false
          ? undefined
          : vi.fn(async () => ({ confirmed: true as const, nonce: 1 })),
  } as unknown as Connection;
}

// A payment whose window sits INSIDE the server's retention cap, so the
// middleware can police its reuse for the whole of its life. A payload valid for
// longer than that is refused outright — its record would expire while the
// payment was still good, and the next presentation would buy a fresh allowance.
// The real client signs 300 seconds; 23 hours is the generous end of accepted.
const validBeforeAt = (nowMs: number) => Math.floor(nowMs / 1000) + 23 * 3600;

const paidHeaderAt = (nowMs: number) => ({
  'x-payment': JSON.stringify({
    chain_id: 'splendor-quantum-1',
    from: PAY_TO,
    to: PAY_TO,
    amount: '10000000000000000',
    valid_after: 0,
    valid_before: validBeforeAt(nowMs),
    nonce: 'ab'.repeat(32),
    signature: 'YQ==',
    settlement_tx: { type: 5, chain_id: 'splendor-quantum-1' },
  }),
});

const paidHeader = paidHeaderAt(Date.now());

describe('createX402Middleware', () => {
  it('rejects verify-only payments when settlement is required (default)', async () => {
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection: mockConnection({}),
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw(
      {
        path: '/api',
        headers: {
          'x-payment': JSON.stringify({
            chain_id: 'splendor-quantum-1',
            from: PAY_TO,
            to: PAY_TO,
            amount: '1',
            valid_after: 0,
            valid_before: validBeforeAt(Date.now()),
            nonce: 'ab'.repeat(32),
            signature: 'YQ==',
          }),
        },
      },
      res as never,
      next,
    );
    expect(res.status).toHaveBeenCalledWith(402);
    expect(json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Settlement Required' }),
    );
    expect(next).not.toHaveBeenCalled();
  });

  it('does not call next on submitted-only settle without a receipt', async () => {
    const connection = mockConnection({ wait: false });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).not.toHaveBeenCalled();
    expect(res.status).toHaveBeenCalledWith(402);
    expect(json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Settlement Unconfirmed', status: 'submitted' }),
    );
  });

  it('calls next after a successful receipt for a submitted settlement', async () => {
    const connection = mockConnection({ settleStatus: 'submitted' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(connection.waitForConfirmation).toHaveBeenCalled();
    expect(next).toHaveBeenCalled();
  });

  it('calls next immediately when settle reports confirmed', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const wait = connection.waitForConfirmation as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
    expect(wait).not.toHaveBeenCalled();
  });

  it('grants on submitted when settlementPolicy is optimistic-submitted', async () => {
    const connection = mockConnection({ settleStatus: 'submitted', wait: false });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      settlementPolicy: 'optimistic-submitted',
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
  });

  it('treats a consumed nonce as idempotent confirmed payment', async () => {
    const connection = mockConnection({ nonceUsed: true, settleStatus: 'confirmed' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
  });
});

// A settled payment confirms for as long as its authorisation window is open,
// so the settle endpoint answers `confirmed` to every presentation of the same
// X-Payment header. QA replayed one settled 500-unit payment and got `confirmed`
// from 25 of 25 settle calls, and the middleware called next() on each — one
// payment buying 25 requests. The consumption record is what stops that: the
// payment is bound to the request it was spent on, and the binding it bought is
// served a bounded number of times inside a bounded window.
describe('X402 payment consumption', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function harness() {
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    return { json, res, next };
  }

  /** The single body a middleware call answered with, if it refused. */
  function refusal(h: ReturnType<typeof harness>): Record<string, unknown> | undefined {
    const call = h.json.mock.calls[0];
    return call ? (call[0] as Record<string, unknown>) : undefined;
  }

  it('refuses a settled payment replayed on a different request', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    const first = harness();
    await mw({ path: '/api/a', method: 'GET', headers: paidHeader }, first.res as never, first.next);
    expect(first.next).toHaveBeenCalled();

    const second = harness();
    await mw({ path: '/api/b', method: 'GET', headers: paidHeader }, second.res as never, second.next);
    expect(second.next).not.toHaveBeenCalled();
    expect(second.res.status).toHaveBeenCalledWith(402);
    expect(second.json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Payment Already Consumed' }),
    );
  });

  it('buys exactly one request, not twenty-five', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const settle = connection.x402Settle as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    let granted = 0;
    const refusals: unknown[] = [];
    for (let i = 0; i < 25; i++) {
      const h = harness();
      await mw({ path: `/api/${i}`, method: 'GET', headers: paidHeader }, h.res as never, h.next);
      if (h.next.mock.calls.length > 0) granted++;
      else refusals.push(refusal(h)?.error);
    }
    expect(granted).toBe(1);
    expect(settle).toHaveBeenCalledTimes(1);
    // Twenty-four different requests, twenty-four identical refusals: the
    // binding is what they failed on, not the allowance.
    expect(new Set(refusals)).toEqual(new Set(['Payment Already Consumed']));
  });

  // The 25-replay QA case, restated for the bound. One settlement was always
  // the right number and still is — but "one settlement" was never the claim
  // that mattered, because all 25 presentations were *served*. Now the same
  // binding is served `maxGrants` times and the remainder are refused.
  it('replaying one binding twenty-five times settles once and serves only the allowance', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const settle = connection.x402Settle as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    let granted = 0;
    const refusals: unknown[] = [];
    for (let i = 0; i < 25; i++) {
      const h = harness();
      await mw({ path: '/feed', method: 'GET', headers: paidHeader }, h.res as never, h.next);
      if (h.next.mock.calls.length > 0) granted++;
      else refusals.push(refusal(h)?.error);
    }
    expect(settle).toHaveBeenCalledTimes(1);
    expect(granted).toBe(X402_DEFAULT_MAX_GRANTS);
    expect(refusals).toHaveLength(25 - X402_DEFAULT_MAX_GRANTS);
    expect(new Set(refusals)).toEqual(new Set(['Payment Reuse Exhausted']));
  });

  it('enforces the bound at exactly the configured count', async () => {
    for (const maxGrants of [1, 2, 5]) {
      const connection = mockConnection({ settleStatus: 'confirmed' });
      const mw = createX402Middleware({
        payTo: PAY_TO,
        price: '0.01',
        connection,
        reuse: { maxGrants },
      });

      let granted = 0;
      for (let i = 0; i < maxGrants + 3; i++) {
        const h = harness();
        await mw({ path: '/feed', method: 'GET', headers: paidHeader }, h.res as never, h.next);
        if (h.next.mock.calls.length > 0) granted++;
      }
      expect(granted, `maxGrants=${maxGrants}`).toBe(maxGrants);
    }
  });

  it('refuses an exhausted payment with a distinct, actionable error', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      reuse: { maxGrants: 2 },
    });

    for (let i = 0; i < 2; i++) {
      const h = harness();
      await mw({ path: '/feed', method: 'GET', headers: paidHeader }, h.res as never, h.next);
      expect(h.next).toHaveBeenCalled();
    }

    const past = harness();
    await mw({ path: '/feed', method: 'GET', headers: paidHeader }, past.res as never, past.next);
    expect(past.next).not.toHaveBeenCalled();
    expect(past.res.status).toHaveBeenCalledWith(402);
    const body = refusal(past)!;
    // Not conflated with the different-binding refusal, and it carries what the
    // caller needs to act: which bound, how much was used, and how to pay again.
    expect(body.error).toBe('Payment Reuse Exhausted');
    expect(body.error).not.toBe('Payment Already Consumed');
    expect(body.limit).toBe('grants');
    expect(body).toMatchObject({ grants: 2, maxGrants: 2 });
    expect(body.requirements).toBeDefined();
  });

  it('closes the replay window from the claim, and keeps refusing other bindings after it', async () => {
    const t0 = 1_700_000_000_000;
    // Sized for the mocked clock, not the real one.
    const t0Header = paidHeaderAt(t0);
    const clock = vi.spyOn(Date, 'now').mockReturnValue(t0);
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      reuse: { replayWindowMs: 60_000 },
    });

    const first = harness();
    await mw({ path: '/feed', method: 'GET', headers: t0Header }, first.res as never, first.next);
    expect(first.next).toHaveBeenCalled();

    const inside = harness();
    clock.mockReturnValue(t0 + 59_000);
    await mw({ path: '/feed', method: 'GET', headers: t0Header }, inside.res as never, inside.next);
    expect(inside.next).toHaveBeenCalled();

    // The payer's own window still has 23 hours to run, so what closed the
    // replay is the server's three-minute allowance measured from its own claim,
    // not anything the payer signed.
    const outside = harness();
    clock.mockReturnValue(t0 + 61_000);
    await mw({ path: '/feed', method: 'GET', headers: t0Header }, outside.res as never, outside.next);
    expect(outside.next).not.toHaveBeenCalled();
    expect(refusal(outside)).toMatchObject({
      error: 'Payment Reuse Exhausted',
      limit: 'replay-window',
    });

    // The record is retained past the replay window on purpose: expiring the
    // replay must not un-spend the payment and hand it to another request.
    const elsewhere = harness();
    clock.mockReturnValue(t0 + 61_000);
    await mw({ path: '/other', method: 'GET', headers: t0Header }, elsewhere.res as never, elsewhere.next);
    expect(elsewhere.next).not.toHaveBeenCalled();
    expect(refusal(elsewhere)).toMatchObject({ error: 'Payment Already Consumed' });
  });

  it('caps concurrent replays of one binding at the allowance', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      reuse: { maxGrants: 3 },
    });

    const calls = Array.from({ length: 10 }, () => harness());
    await Promise.all(
      calls.map((h) => mw({ path: '/feed', method: 'GET', headers: paidHeader }, h.res as never, h.next)),
    );

    const granted = calls.filter((h) => h.next.mock.calls.length > 0).length;
    const exhausted = calls.filter((h) => refusal(h)?.error === 'Payment Reuse Exhausted').length;
    // Charging the allowance at claim time is what makes this hold: ten requests
    // that all read the counter before anyone paid would all have been served.
    expect(granted).toBe(3);
    expect(exhausted).toBe(7);
  });

  it('lets only one of two concurrent requests reserve the payment', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const settle = connection.x402Settle as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    const a = harness();
    const b = harness();
    await Promise.all([
      mw({ path: '/api/a', method: 'GET', headers: paidHeader }, a.res as never, a.next),
      mw({ path: '/api/b', method: 'GET', headers: paidHeader }, b.res as never, b.next),
    ]);

    const served = [a, b].filter((h) => h.next.mock.calls.length > 0);
    expect(served).toHaveLength(1);
    expect(settle).toHaveBeenCalledTimes(1);
    const loser = [a, b].find((h) => h.next.mock.calls.length === 0)!;
    expect(refusal(loser)).toMatchObject({ error: 'Payment Already Consumed' });
  });

  it('refuses to start with an allowance that is not a bound', () => {
    for (const reuse of [{ maxGrants: 0 }, { maxGrants: -1 }, { maxGrants: 1.5 }]) {
      expect(() =>
        createX402Middleware({
          payTo: PAY_TO,
          price: '0.01',
          connection: mockConnection({ settleStatus: 'confirmed' }),
          reuse,
        }),
      ).toThrow(/maxGrants/);
    }
    expect(() =>
      createX402Middleware({
        payTo: PAY_TO,
        price: '0.01',
        connection: mockConnection({ settleStatus: 'confirmed' }),
        reuse: { replayWindowMs: -1 },
      }),
    ).toThrow(/replayWindowMs/);
  });

  it('replays the same request idempotently without settling twice', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const settle = connection.x402Settle as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    const first = harness();
    await mw({ path: '/api', method: 'GET', headers: paidHeader }, first.res as never, first.next);
    expect(first.next).toHaveBeenCalled();
    expect(settle).toHaveBeenCalledTimes(1);

    const retry = harness();
    await mw({ path: '/api', method: 'GET', headers: paidHeader }, retry.res as never, retry.next);
    expect(retry.next).toHaveBeenCalled();
    expect(settle).toHaveBeenCalledTimes(1);
  });

  it('separates two calls to one endpoint by the client idempotency key', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const mw = createX402Middleware({ payTo: PAY_TO, price: '0.01', connection });

    const first = harness();
    await mw(
      { path: '/api', method: 'POST', headers: { ...paidHeader, 'idempotency-key': 'one' } },
      first.res as never,
      first.next,
    );
    expect(first.next).toHaveBeenCalled();

    const second = harness();
    await mw(
      { path: '/api', method: 'POST', headers: { ...paidHeader, 'idempotency-key': 'two' } },
      second.res as never,
      second.next,
    );
    expect(second.next).not.toHaveBeenCalled();
    expect(second.json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Payment Already Consumed' }),
    );
  });

  it('releases the claim when settlement never establishes', async () => {
    // One store across both middlewares, or the second would simply be starting
    // from an empty map and the release would not be under test at all.
    const consumptionStore = createMemoryConsumptionStore();
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection: mockConnection({ settleStatus: 'failed' }),
      consumptionStore,
    });

    const failed = harness();
    await mw({ path: '/api', method: 'GET', headers: paidHeader }, failed.res as never, failed.next);
    expect(failed.next).not.toHaveBeenCalled();
    expect(refusal(failed)).toMatchObject({ error: 'Settlement Failed' });

    // The payer was not charged, so the payment must still be spendable — on a
    // different request too, since nothing was bought — and the failed attempt
    // must not have eaten any of the reuse allowance either.
    const ok = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection: mockConnection({ settleStatus: 'confirmed' }),
      consumptionStore,
    });
    const retry = harness();
    await ok({ path: '/api/other', method: 'GET', headers: paidHeader }, retry.res as never, retry.next);
    expect(retry.next).toHaveBeenCalled();

    let granted = 1;
    for (let i = 0; i < 5; i++) {
      const h = harness();
      await ok({ path: '/api/other', method: 'GET', headers: paidHeader }, h.res as never, h.next);
      if (h.next.mock.calls.length > 0) granted++;
    }
    expect(granted).toBe(X402_DEFAULT_MAX_GRANTS);
  });
});
