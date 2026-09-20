/**
 * X402 consumption records: what one settled payment is allowed to buy.
 *
 * A settled payment is proof that money moved. It is NOT, by itself, an
 * entitlement to a service call, and the two are easy to confuse because the
 * X-Payment header is a bearer credential: whoever holds it can present it
 * again. The chain stops the payment from being *settled* twice — the payment
 * nonce is consumed once and for ever — but the chain never sees the HTTP
 * request, so from its point of view the second, tenth and twenty-fifth
 * presentation of the same header are all the same already-settled payment and
 * all answer `confirmed`. Without a record on this side, every one of those
 * confirmations is a free request.
 *
 * This module is that record. It binds a payment identity to the request it
 * was spent on, and bounds how many times that binding may be served, so:
 *
 *   - the first request carrying a payment claims it;
 *   - a retry of the SAME request replays the claim and is served again,
 *     because a retry after a dropped response must not cost a second payment
 *     — but only up to `maxGrants` times and only inside `replayWindowMs` of
 *     the claim;
 *   - once either bound is reached the payment is spent: further presentations
 *     of it are refused as exhausted, including of the binding it bought;
 *   - a DIFFERENT request carrying the same payment is refused.
 *
 * Those are Stripe's idempotency-key semantics — same key and same parameters
 * replay the stored outcome, same key with different parameters is an error —
 * applied to a payment instead of an API key, and they are what the x402
 * literature calls pre-grant idempotency on (payment id, resource id). L402
 * reaches the same place from the other direction: its macaroon names the
 * resource the preimage pays for, so the credential cannot be carried to a
 * different endpoint.
 *
 * ## Why the replay has to be bounded
 *
 * "Replay the same binding" without a bound is not idempotency, it is a
 * subscription: one payment for `GET /feed` serves an unlimited number of
 * `GET /feed` calls for as long as the record lives, and the record lives
 * until the payer's own authorisation window closes — which the payer chooses,
 * up to the 24 h clamp in `consumptionExpiry`. A defined behaviour, but not one
 * an operator pricing a request at 500 units would recognise as what they sold.
 *
 * Both bounds are needed, because each alone leaves the other axis open:
 *
 *   - a count alone still lets the last retry land a day after the purchase,
 *     so the payment is a day-long entitlement with a small number of uses;
 *   - a window alone still allows thousands of requests inside it.
 *
 * The count is charged at CLAIM time, not at grant time. That is what makes the
 * bound hold under concurrency: n simultaneous presentations of one payment all
 * pass through `claim` one at a time, so the (n - maxGrants) that arrive after
 * the budget is gone are refused rather than all reading a not-yet-incremented
 * counter and all being served. A reservation whose settlement never
 * establishes is given back with the record by `release`.
 *
 * ## The store is per-process, and that is a real limitation
 *
 * The default store is a `Map` inside one Node process. It is authoritative for
 * exactly that process and for exactly as long as it lives, so:
 *
 *   - **a restart forgets every claim.** A payment claimed before the restart
 *     is unknown after it, and — if the payer's window is still open — will be
 *     claimed again, granting `maxGrants` more requests for the same money. A
 *     rolling deploy is a restart.
 *   - **a second replica forgets every claim the first took.** Two replicas
 *     grant the same payment `maxGrants` times each; k replicas, k × maxGrants.
 *     A load balancer is all it takes.
 *
 * Neither is fixable inside a per-process Map, and neither is a bug in this
 * module: an off-chain record is only as global as the store behind it. The
 * fix is a shared store — see `X402ConsumptionStore`, which exists for exactly
 * this and is accepted by `createX402Middleware` as `consumptionStore`. A
 * deployment that has more than one replica, or that cannot tolerate a restart
 * re-granting an open payment, MUST supply one. The chain still enforces the
 * part that must be global no matter what the store does: a payment nonce
 * settles exactly once, for ever.
 *
 * The record is deliberately NOT on chain. See docs/modules/x402.md.
 */

/** A request the middleware can bind a payment to. */
export type X402BindableRequest = {
  path: string;
  method?: string;
  headers: Record<string, string | string[] | undefined>;
};

/** What one payment bought, and when. */
export type X402ConsumptionRecord = {
  /** The request binding this payment is spent on. */
  binding: string;
  /** Epoch ms the payment was claimed. */
  claimedAt: number;
  /** Epoch ms after which the record may be dropped. */
  expiresAt: number;
  /**
   * How many grants this payment has been charged for, counted at claim time —
   * so it includes the request currently in flight, and a claim that is then
   * released is not counted because the whole record goes with it. Bounded by
   * the `maxGrants` passed to `claim`.
   */
  grants: number;
  /** True once settlement was established and access was granted. */
  granted: boolean;
  /**
   * Who holds the reservation. Returned to the claimer by `claim` and required
   * by `release`, so only the request that took a claim can give it back.
   */
  token?: string;
  /** Settlement transaction hash, when the settle call reported one. */
  txHash?: string;
};

/** Which bound a payment ran out of. */
export type X402ReuseLimit = 'grants' | 'replay-window';

export type X402ClaimOutcome =
  /** First use of this payment. Proceed to settle. */
  | { status: 'claimed'; token: string }
  /** Same payment, same request, still inside both bounds: an idempotent retry. */
  | { status: 'replay'; record: X402ConsumptionRecord }
  /** Same payment, different request: refuse. */
  | { status: 'conflict'; record: X402ConsumptionRecord }
  /**
   * Same payment, same request, but the reuse allowance is gone: either the
   * grant count is spent or the replay window has closed. Distinct from
   * `conflict` — nothing is wrong with the request, the payment is simply used
   * up — and the caller's answer is to pay again, not to change the request.
   */
  | { status: 'exhausted'; record: X402ConsumptionRecord; limit: X402ReuseLimit; reason: string }
  /**
   * The store cannot take the claim (it is full of live records). Refusing is
   * the only safe answer: evicting a live record to make room would hand the
   * evicted payment a second free request.
   */
  | { status: 'unavailable'; reason: string };

/** How much one settled payment is allowed to buy. */
export type X402ReuseLimits = {
  /**
   * Total requests one settled payment may be granted, retries included.
   * 1 means no replay at all. Must be an integer >= 1.
   */
  maxGrants: number;
  /**
   * How long after the claim a replay is still served, in ms, measured from
   * `claimedAt` — NOT from the payer's own authorisation window, which the
   * payer chooses. 0 means no replay at all.
   */
  replayWindowMs: number;
};

/**
 * Three requests per payment: the one that was paid for, plus two retries.
 *
 * Small on purpose. The reason replay exists at all is a dropped response, and
 * a client that has not got an answer after three attempts is not going to be
 * rescued by a fourth free one. An operator who has a reason to allow more
 * raises it explicitly, which is the point: the allowance is a number in the
 * config rather than "whatever the payer's signature window permits".
 */
export const X402_DEFAULT_MAX_GRANTS = 3;

/**
 * Three minutes of replay, from the claim.
 *
 * Sized against the slowest honest first request: the claim is taken before
 * the settle call, and `waitForConfirmation` waits up to 60 s for a receipt by
 * default, so a response can legitimately arrive ~60 s after `claimedAt`. Three
 * times that leaves room for a client's backoff before the first retry without
 * turning the payment into a day pass.
 */
export const X402_DEFAULT_REPLAY_WINDOW_MS = 180_000;

/**
 * Where consumption records live.
 *
 * `claim` must be atomic with respect to other concurrent claims for the same
 * payment id — the in-memory store gets that from the event loop, a Redis store
 * would get it from `SET NX` plus an `INCR` of the grant count in the same
 * script. It is `claim`, and only `claim`, that decides and charges reuse:
 * `maxGrants` and `replayWindowMs` arrive with every call so that the policy
 * stays in the middleware config while the counter stays wherever the records
 * do. **A store that ignores those two fields does not enforce the bound** —
 * one payment buys unlimited requests again, which is the whole defect this
 * shape exists to close, so a custom store must honour them.
 *
 * Nothing else in this module assumes a single process, so a multi-replica
 * deployment MUST supply a shared store; the default one is per-process and two
 * replicas would each grant the same payment `maxGrants` times. That is stated
 * in the module doc comment above and in the middleware config docs.
 */
export interface X402ConsumptionStore {
  claim(
    paymentId: string,
    binding: string,
    opts: {
      now: number;
      expiresAt: number;
      /** Defaults to `X402_DEFAULT_MAX_GRANTS` when a caller omits it. */
      maxGrants?: number;
      /** Defaults to `X402_DEFAULT_REPLAY_WINDOW_MS` when a caller omits it. */
      replayWindowMs?: number;
    },
  ): X402ClaimOutcome | Promise<X402ClaimOutcome>;
  /** Mark a claimed payment as granted (settlement established). */
  grant(paymentId: string, txHash?: string): void | Promise<void>;
  /**
   * Drop a claim whose settlement never established, so the payer may retry.
   *
   * `token` is the one `claim` returned. A release without it, or with the wrong
   * one, does nothing: a second request replaying the same payment while the
   * first is still settling would otherwise be able to delete the first
   * request's reservation when its own attempt failed, handing the payment back
   * for a different binding while the original was still in flight. A store that
   * ignores the token does not close that race.
   */
  release(paymentId: string, token?: string): void | Promise<void>;
}

export type MemoryConsumptionStoreOptions = {
  /**
   * How many live records to hold. Reaching it refuses new claims rather than
   * evicting live ones; raise it, or plug in a shared store, for real traffic.
   */
  maxEntries?: number;
};

const DEFAULT_MAX_ENTRIES = 10_000;

/**
 * Normalise and check a partial reuse policy.
 *
 * Nonsense is rejected rather than clamped: a `maxGrants` of 0 or -1 is not a
 * strict operator, it is a mistake that would refuse every paid request, and
 * a fractional or infinite bound is not a bound.
 */
export function resolveReuseLimits(opts: Partial<X402ReuseLimits> = {}): X402ReuseLimits {
  const maxGrants = opts.maxGrants ?? X402_DEFAULT_MAX_GRANTS;
  const replayWindowMs = opts.replayWindowMs ?? X402_DEFAULT_REPLAY_WINDOW_MS;
  if (!Number.isInteger(maxGrants) || maxGrants < 1) {
    throw new Error(`x402: reuse.maxGrants must be an integer >= 1 (got ${String(opts.maxGrants)})`);
  }
  if (!Number.isFinite(replayWindowMs) || replayWindowMs < 0) {
    throw new Error(
      `x402: reuse.replayWindowMs must be a finite number >= 0 (got ${String(opts.replayWindowMs)})`,
    );
  }
  return { maxGrants, replayWindowMs };
}

/**
 * The default store: a per-process Map, swept lazily.
 *
 * Records are kept until the payment's own authorisation window closes, which
 * is the moment a *claim* stops being possible anyway — `valid_before` is inside
 * the payer's signature, and the node refuses an expired payload outright. So
 * the record only has to outlive the window it guards, and holding it longer
 * buys nothing. Retention is not the same thing as reuse: for as long as the
 * record is held it refuses the payment to a different binding, while what it
 * *grants* to the binding it bought is bounded separately and much more tightly
 * by `maxGrants` and `replayWindowMs`.
 *
 * Per-process. See the module doc comment for what a restart and a second
 * replica do to that, and for the shared-store seam that fixes it.
 */
export function createMemoryConsumptionStore(
  opts: MemoryConsumptionStoreOptions = {},
): X402ConsumptionStore {
  const maxEntries = opts.maxEntries ?? DEFAULT_MAX_ENTRIES;
  const records = new Map<string, X402ConsumptionRecord>();
  // Distinguishes two claims of the same payment id over the life of the store,
  // so a release cannot be satisfied by a token from an earlier, already-gone
  // claim that happened to share a timestamp.
  let tokenSeq = 0;

  function sweep(now: number): void {
    for (const [id, rec] of records) {
      if (rec.expiresAt <= now) records.delete(id);
    }
  }

  return {
    claim(paymentId, binding, { now, expiresAt, maxGrants, replayWindowMs }) {
      const limits = resolveReuseLimits({ maxGrants, replayWindowMs });
      const existing = records.get(paymentId);
      if (existing && existing.expiresAt > now) {
        // A different request is a different answer from an exhausted one, and
        // they must not be conflated: the first says "change the request", the
        // second says "pay again". Bindings are compared first so a mismatch is
        // never reported as an allowance problem.
        if (existing.binding !== binding) {
          return { status: 'conflict', record: { ...existing } };
        }
        const age = now - existing.claimedAt;
        if (age >= limits.replayWindowMs) {
          return {
            status: 'exhausted',
            record: { ...existing },
            limit: 'replay-window',
            reason:
              `the replay window for this payment closed ${age - limits.replayWindowMs}ms ago ` +
              `(${limits.replayWindowMs}ms from the claim)`,
          };
        }
        if (existing.grants >= limits.maxGrants) {
          return {
            status: 'exhausted',
            record: { ...existing },
            limit: 'grants',
            reason: `this payment has already been granted ${existing.grants} of ${limits.maxGrants} requests`,
          };
        }
        // Charged here, before the request is served, so concurrent
        // presentations cannot all pass a check that none of them has yet paid
        // for. Serving is the caller's business; being allowed to is ours.
        existing.grants += 1;
        return { status: 'replay', record: { ...existing } };
      }
      if (existing) records.delete(paymentId);
      if (records.size >= maxEntries) {
        sweep(now);
        if (records.size >= maxEntries) {
          return {
            status: 'unavailable',
            reason: `x402 consumption store is full (${maxEntries} live records)`,
          };
        }
      }
      const token = `${paymentId}#${now}#${(tokenSeq += 1)}`;
      records.set(paymentId, {
        binding,
        claimedAt: now,
        expiresAt,
        grants: 1,
        granted: false,
        token,
      });
      return { status: 'claimed', token };
    },
    grant(paymentId, txHash) {
      const rec = records.get(paymentId);
      if (!rec) return;
      rec.granted = true;
      if (txHash) rec.txHash = txHash;
    },
    release(paymentId, token) {
      const rec = records.get(paymentId);
      if (!rec) return;
      // Only an ungranted claim is released. Releasing a granted record would
      // hand the payment back for a second, different request. The reservation
      // the claim charged goes with the record, so a failed settlement costs
      // the payer nothing — neither money nor allowance.
      if (rec.granted) return;
      // And only by whoever took it. A replay of the same payment arriving while
      // the first request is still settling gets `replay` with no token; if its
      // own settle attempt then failed it used to delete the record the first
      // request was relying on, and the payment became claimable for a different
      // binding while the original was in flight.
      if (rec.token !== undefined && rec.token !== token) return;
      records.delete(paymentId);
    },
  };
}

/**
 * The identity of a payment: exactly what the chain consumes once. chain + payer
 * + payment nonce is the tuple the on-chain replay registry is keyed by
 * (SHA3-256(from ‖ nonce), per chain), so two headers share an identity iff
 * they are the same payment.
 */
export function paymentIdentity(payload: {
  chain_id?: string;
  from?: string;
  nonce?: string;
}): string {
  const chain = payload.chain_id;
  const from = payload.from;
  const nonce = payload.nonce;
  if (!chain || !from || !nonce) {
    throw new Error('x402: payment payload must carry chain_id, from and nonce');
  }
  // Canonical, because the chain's idea of one payment is narrower than the
  // wire's. On chain the identity is SHA3-256 over DECODED bytes, and both the
  // address and the nonce are hex — `hex.DecodeString` and the SDK's own
  // `hexToBytes` accept either case, and `hexToBytes` strips an `0x` prefix. So
  // `ab…`, `AB…` and `0xab…` are one payment to the node and were three
  // different strings here, each getting its own record and its own full reuse
  // allowance. Re-encoding the same header was enough to buy the requests the
  // allowance exists to stop.
  //
  // chain_id is left exactly as sent: the node compares it to its own by string
  // equality, so a differently-cased chain is not a valid payment at all and
  // cannot reach this in two spellings.
  return `${canonicalChainID(chain)}|${canonicalAddress('from', from)}|${canonicalNonce(nonce)}`;
}

/** 64 hex characters, one case, no prefix — the nonce as the chain sees it. */
function canonicalNonce(raw: string): string {
  const s = raw.trim().replace(/^0[xX]/, '');
  if (!/^[0-9a-fA-F]{64}$/.test(s)) {
    throw new Error(
      `x402: nonce must be 32 bytes of hex; refusing ${JSON.stringify(raw)} rather than ` +
        'treating an unrecognised spelling as a payment of its own',
    );
  }
  return s.toLowerCase();
}

/** `SQ:` plus 64 hex, the prefix exact and the body in one case. */
function canonicalAddress(field: string, raw: string): string {
  const s = raw.trim();
  const prefix = 'SQ:';
  if (!s.startsWith(prefix)) {
    throw new Error(`x402: ${field} must be an SQ: address; refusing ${JSON.stringify(raw)}`);
  }
  const body = s.slice(prefix.length);
  if (!/^[0-9a-fA-F]{64}$/.test(body)) {
    throw new Error(
      `x402: ${field} must be an SQ: address over 32 bytes of hex; refusing ${JSON.stringify(raw)}`,
    );
  }
  return prefix + body.toLowerCase();
}

/**
 * The chain id, unchanged but checked.
 *
 * `|` is the separator between the three parts of an identity, so a value
 * carrying one could make two different payments agree on an identity — and
 * sharing an identity means sharing an allowance.
 */
function canonicalChainID(raw: string): string {
  const s = raw.trim();
  if (s === '' || s.includes('|')) {
    throw new Error(`x402: chain_id must be non-empty and free of "|"; refusing ${JSON.stringify(raw)}`);
  }
  return s;
}

function headerValue(
  headers: Record<string, string | string[] | undefined>,
  name: string,
): string | undefined {
  const raw = headers[name] ?? headers[name.toLowerCase()];
  const v = Array.isArray(raw) ? raw[0] : raw;
  return typeof v === 'string' && v.length > 0 ? v : undefined;
}

/**
 * The default request binding: method, path, and a client-supplied idempotency
 * key when there is one.
 *
 * Method and path are the coarsest binding that is still honest — one payment
 * buys one endpoint, not the API. A client that needs two distinct calls to the
 * same endpoint from one payment does not get them: it sends its own
 * `Idempotency-Key`, which makes each call a distinct binding, and pays for
 * each. Including a client-chosen value can only narrow what a payment buys,
 * never widen it, because a binding that differs from the claimed one is
 * refused.
 *
 * Omitting the key does not widen it either, now that the reuse allowance is
 * the server's number rather than the payer's signature window: a client that
 * wants free repeat reads by sending no key gets `maxGrants` of them, not a
 * day's worth.
 *
 * Bodies are not hashed here: the middleware runs before body parsing in most
 * frameworks, and reading the stream would break the handler behind it. An
 * application that must bind the body supplies its own `bindRequest`.
 */
export function defaultRequestBinding(req: X402BindableRequest): string {
  const method = (req.method ?? 'GET').toUpperCase();
  const key = headerValue(req.headers, 'idempotency-key') ?? headerValue(req.headers, 'x-request-id');
  return key ? `${method} ${req.path}#${key}` : `${method} ${req.path}`;
}

/**
 * When the consumption record may be dropped: the end of the payer's own
 * authorisation window, plus a clock-skew margin, clamped so a payload with a
 * silly window cannot make the record useless or immortal.
 *
 * This is record RETENTION, and it is deliberately generous — a record that is
 * still held is what refuses the payment to a different request, so dropping it
 * early is the unsafe direction. It is not, and must not be read as, how long a
 * payment keeps buying requests: that is `maxGrants` / `replayWindowMs`, which
 * are the server's policy and are measured from `claimedAt` rather than from
 * anything the payer signed.
 */
/**
 * Whether this payment stays valid for longer than the store will remember it.
 *
 * Retention is clamped (24 h by default). A payer who signs a `valid_before`
 * beyond that clamp gets a record that expires while the payment is still good:
 * the next presentation finds nothing, claims afresh, and buys another full
 * allowance — and again every retention period, for as long as the signature
 * window runs. The node cannot stop it either, because the nonce IS settled, so
 * `/x402/settle` confirms it from the on-chain record.
 *
 * So the resource server declines what it cannot police. That is a narrow
 * refusal: it only ever rejects a payment authorised for longer than a day, and
 * `payX402` signs a window of minutes.
 */
export function paymentOutlivesConsumption(
  payload: { valid_before?: number },
  now: number,
  opts: { maxMs?: number; skewMs?: number } = {},
): boolean {
  const maxMs = opts.maxMs ?? 24 * 60 * 60_000;
  const skewMs = opts.skewMs ?? 60_000;
  const validBefore = typeof payload.valid_before === 'number' ? payload.valid_before : 0;
  if (validBefore <= 0) return false;
  return validBefore * 1000 + skewMs - now > maxMs;
}

export function consumptionExpiry(
  payload: { valid_before?: number },
  now: number,
  opts: { minMs?: number; maxMs?: number; skewMs?: number } = {},
): number {
  const minMs = opts.minMs ?? 5 * 60_000;
  const maxMs = opts.maxMs ?? 24 * 60 * 60_000;
  const skewMs = opts.skewMs ?? 60_000;
  const validBefore = typeof payload.valid_before === 'number' ? payload.valid_before : 0;
  const window = validBefore > 0 ? validBefore * 1000 + skewMs - now : 0;
  return now + Math.min(Math.max(window, minMs), maxMs);
}
