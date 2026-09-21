/**
 * Unit tests for the consumption store itself.
 *
 * The middleware tests drive this through HTTP-shaped calls, which is the right
 * altitude for "what does a caller get"; these drive `claim` directly, because
 * the bound is a property of the store and the clock is an argument there rather
 * than a global to be mocked.
 */
import { describe, expect, it } from 'vitest';
import {
  createMemoryConsumptionStore,
  resolveReuseLimits,
  X402_DEFAULT_MAX_GRANTS,
  X402_DEFAULT_REPLAY_WINDOW_MS,
  type X402ClaimOutcome,
} from '../x402/consumption.js';

const PAYMENT = 'splendor-quantum-1|SQ:aa|nonce-1';
const BINDING = 'GET /feed';
const T0 = 1_700_000_000_000;
const HOUR = 3_600_000;

function store(maxEntries?: number) {
  const s = createMemoryConsumptionStore(maxEntries == null ? {} : { maxEntries });
  return {
    raw: s,
    /** Claim with the record kept alive for a day, as a real payload would. */
    claim(
      opts: {
        now?: number;
        binding?: string;
        maxGrants?: number;
        replayWindowMs?: number;
        paymentId?: string;
        expiresAt?: number;
      } = {},
    ): X402ClaimOutcome {
      const now = opts.now ?? T0;
      return s.claim(opts.paymentId ?? PAYMENT, opts.binding ?? BINDING, {
        now,
        expiresAt: opts.expiresAt ?? now + 24 * HOUR,
        maxGrants: opts.maxGrants,
        replayWindowMs: opts.replayWindowMs,
      }) as X402ClaimOutcome;
    },
  };
}

describe('x402 consumption store: the reuse bound', () => {
  it('charges the first claim against the allowance', () => {
    const s = store();
    expect(s.claim().status).toBe('claimed');
    const second = s.claim();
    expect(second.status).toBe('replay');
    // grants counts the request in flight, so the first claim is already 1 and
    // the replay is 2 — otherwise maxGrants would mean "1 + maxGrants".
    expect(second.status === 'replay' && second.record.grants).toBe(2);
  });

  it('serves exactly maxGrants and then reports exhausted', () => {
    for (const maxGrants of [1, 2, 3, 7]) {
      const s = store();
      const served: X402ClaimOutcome['status'][] = [];
      for (let i = 0; i < maxGrants + 2; i++) {
        served.push(s.claim({ maxGrants }).status);
      }
      expect(served.filter((x) => x === 'claimed' || x === 'replay'), `maxGrants=${maxGrants}`)
        .toHaveLength(maxGrants);
      expect(served.slice(maxGrants)).toEqual(['exhausted', 'exhausted']);
    }
  });

  it('names the bound it ran out of', () => {
    const s = store();
    s.claim({ maxGrants: 1 });
    const spent = s.claim({ maxGrants: 1 });
    expect(spent.status).toBe('exhausted');
    if (spent.status !== 'exhausted') throw new Error('unreachable');
    expect(spent.limit).toBe('grants');
    expect(spent.reason).toMatch(/already been granted 1 of 1/);
  });

  it('measures the replay window from the claim, not from the payment window', () => {
    const s = store();
    // The record is retained for a day; the replay window is a minute.
    s.claim({ now: T0, replayWindowMs: 60_000 });
    expect(s.claim({ now: T0 + 59_999, replayWindowMs: 60_000 }).status).toBe('replay');
    const late = s.claim({ now: T0 + 60_000, replayWindowMs: 60_000 });
    expect(late.status).toBe('exhausted');
    if (late.status !== 'exhausted') throw new Error('unreachable');
    expect(late.limit).toBe('replay-window');
  });

  it('keeps the record — and so keeps refusing other bindings — after the window closes', () => {
    const s = store();
    s.claim({ now: T0, replayWindowMs: 60_000 });
    expect(s.claim({ now: T0 + HOUR, replayWindowMs: 60_000 }).status).toBe('exhausted');
    // Expiring the replay right must not un-spend the payment: a closed window
    // that re-opened the payment for a different request would be a worse hole
    // than the one the bound closes.
    const elsewhere = s.claim({ now: T0 + HOUR, binding: 'GET /other', replayWindowMs: 60_000 });
    expect(elsewhere.status).toBe('conflict');
  });

  it('reads a different binding as a conflict even when the allowance is gone', () => {
    const s = store();
    s.claim({ maxGrants: 1 });
    expect(s.claim({ maxGrants: 1 }).status).toBe('exhausted');
    const other = s.claim({ binding: 'POST /feed', maxGrants: 1 });
    // "Change the request" and "pay again" are different answers and the more
    // specific one wins; conflating them would tell a misrouted client to pay.
    expect(other.status).toBe('conflict');
    expect(other.status === 'conflict' && other.record.binding).toBe(BINDING);
  });

  it('treats maxGrants 1 and replayWindowMs 0 as no replay at all', () => {
    const byCount = store();
    byCount.claim({ maxGrants: 1 });
    expect(byCount.claim({ maxGrants: 1 }).status).toBe('exhausted');

    const byWindow = store();
    byWindow.claim({ replayWindowMs: 0 });
    expect(byWindow.claim({ replayWindowMs: 0 }).status).toBe('exhausted');
  });

  it('applies the documented defaults when a caller omits the limits', () => {
    const s = store();
    let served = 1;
    expect(s.claim().status).toBe('claimed');
    for (let i = 0; i < 6; i++) if (s.claim().status === 'replay') served++;
    expect(served).toBe(X402_DEFAULT_MAX_GRANTS);

    const windowed = store();
    windowed.claim({ paymentId: 'p2' });
    expect(
      windowed.claim({ paymentId: 'p2', now: T0 + X402_DEFAULT_REPLAY_WINDOW_MS - 1 }).status,
    ).toBe('replay');
    expect(
      windowed.claim({ paymentId: 'p2', now: T0 + X402_DEFAULT_REPLAY_WINDOW_MS }).status,
    ).toBe('exhausted');
  });
});

describe('x402 consumption store: what the bound must not break', () => {
  it('refuses a different binding for the same payment', () => {
    const s = store();
    expect(s.claim().status).toBe('claimed');
    const other = s.claim({ binding: 'GET /premium' });
    expect(other.status).toBe('conflict');
    expect(other.status === 'conflict' && other.record.binding).toBe(BINDING);
  });

  it('gives the whole reservation back when a claim is released', () => {
    const s = store();
    const first = s.claim();
    expect(first.status).toBe('claimed');
    s.raw.release(PAYMENT, first.status === 'claimed' ? first.token : undefined);
    // Released before it was ever granted: the payment is unspent, so even a
    // different binding may have it, and the allowance starts over.
    const again = s.claim({ binding: 'GET /other' });
    expect(again.status).toBe('claimed');
    let served = 1;
    for (let i = 0; i < 5; i++) {
      if (s.claim({ binding: 'GET /other' }).status === 'replay') served++;
    }
    expect(served).toBe(X402_DEFAULT_MAX_GRANTS);
  });

  it('never releases a granted claim', () => {
    const s = store();
    const first = s.claim();
    const token = first.status === 'claimed' ? first.token : '';
    s.raw.grant(PAYMENT, token, 'tx-1');
    s.raw.release(PAYMENT, token);
    // Releasing here would hand a settled payment to a second, different
    // request — the release path exists for unsettled claims only.
    expect(s.claim({ binding: 'GET /other' }).status).toBe('conflict');
  });

  it('keeps a granted payment spent after its authorization record expires', () => {
    const s = store();
    const first = s.claim({ now: T0, expiresAt: T0 + 60_000, maxGrants: 1 });
    if (first.status !== 'claimed') throw new Error('expected first claim');
    s.raw.grant(PAYMENT, first.token, 'settled-tx');

    expect(
      s.claim({ now: T0 + HOUR, binding: 'GET /other', expiresAt: T0 + 2 * HOUR }).status,
    ).toBe('conflict');
    expect(s.claim({ now: T0 + HOUR, expiresAt: T0 + 2 * HOUR, maxGrants: 1 }).status).toBe(
      'exhausted',
    );
  });

  it('keeps the reservation while another same-binding settlement is in flight', () => {
    const s = store();
    const first = s.claim({ maxGrants: 3 });
    const replay = s.claim({ maxGrants: 3 });
    if (first.status !== 'claimed' || replay.status !== 'replay') {
      throw new Error('expected claimed then replay');
    }

    s.raw.release(PAYMENT, first.token);
    expect(s.claim({ binding: 'GET /other' }).status).toBe('conflict');

    s.raw.grant(PAYMENT, replay.token, 'settled-tx');
    expect(s.claim({ binding: 'GET /other' }).status).toBe('conflict');
  });

  it('does not let a stale completion grant a replacement claim', () => {
    const s = store();
    const first = s.claim();
    if (first.status !== 'claimed') throw new Error('expected first claim');
    s.raw.release(PAYMENT, first.token);

    const replacement = s.claim({ binding: 'GET /other' });
    if (replacement.status !== 'claimed') throw new Error('expected replacement claim');
    s.raw.grant(PAYMENT, first.token, 'stale-tx');
    s.raw.release(PAYMENT, replacement.token);

    expect(s.claim({ binding: 'GET /third' }).status).toBe('claimed');
  });

  it('lets exactly one concurrent claim reserve the payment', async () => {
    const s = store();
    const outcomes = await Promise.all(
      Array.from({ length: 10 }, () => Promise.resolve().then(() => s.claim({ maxGrants: 3 }))),
    );
    const counted = outcomes.reduce<Record<string, number>>((acc, o) => {
      acc[o.status] = (acc[o.status] ?? 0) + 1;
      return acc;
    }, {});
    expect(counted.claimed).toBe(1);
    expect(counted.replay).toBe(2);
    expect(counted.exhausted).toBe(7);
  });

  it('refuses rather than evicting a live record when full', () => {
    const s = store(1);
    expect(s.claim({ paymentId: 'p1' }).status).toBe('claimed');
    const full = s.claim({ paymentId: 'p2' });
    expect(full.status).toBe('unavailable');
    // Room appears only when the first record is genuinely dead.
    expect(s.claim({ paymentId: 'p2', now: T0 + 25 * HOUR }).status).toBe('claimed');
  });

  it('claims again once the record itself has expired', () => {
    const s = store();
    s.claim({ now: T0, expiresAt: T0 + 5 * 60_000 });
    // Past the retention the payment is unknown again — by then the payer's own
    // signed window is what refuses it, at the node.
    expect(s.claim({ now: T0 + 6 * 60_000, binding: 'GET /other' }).status).toBe('claimed');
  });
});

describe('resolveReuseLimits', () => {
  it('defaults to three grants inside three minutes', () => {
    expect(resolveReuseLimits()).toEqual({
      maxGrants: X402_DEFAULT_MAX_GRANTS,
      replayWindowMs: X402_DEFAULT_REPLAY_WINDOW_MS,
    });
    expect(X402_DEFAULT_MAX_GRANTS).toBe(3);
    expect(X402_DEFAULT_REPLAY_WINDOW_MS).toBe(180_000);
  });

  it('rejects an allowance that is not a bound', () => {
    for (const maxGrants of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => resolveReuseLimits({ maxGrants }), String(maxGrants)).toThrow(/maxGrants/);
    }
    for (const replayWindowMs of [-1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => resolveReuseLimits({ replayWindowMs }), String(replayWindowMs)).toThrow(
        /replayWindowMs/,
      );
    }
  });

  it('takes each limit independently', () => {
    expect(resolveReuseLimits({ maxGrants: 1 })).toEqual({
      maxGrants: 1,
      replayWindowMs: X402_DEFAULT_REPLAY_WINDOW_MS,
    });
    expect(resolveReuseLimits({ replayWindowMs: 0 })).toEqual({
      maxGrants: X402_DEFAULT_MAX_GRANTS,
      replayWindowMs: 0,
    });
  });
});
