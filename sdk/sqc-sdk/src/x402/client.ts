/**
 * X402 micropayment protocol helpers.
 */
import { fromSPLD, toSPLD } from '../rpc.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import type { X402PayOpts } from '../types.js';
import { randomNonceHex } from '../encoding.js';
import { assertAddress } from '../address.js';
import { WalletError } from '../errors.js';
import {
  consumptionExpiry,
  createMemoryConsumptionStore,
  defaultRequestBinding,
  paymentIdentity,
  paymentOutlivesConsumption,
  resolveReuseLimits,
  type X402BindableRequest,
  type X402ConsumptionStore,
  type X402ReuseLimits,
} from './consumption.js';

export type PaymentRequirements = {
  pay_to: string;
  amount: string;
  valid_after?: number;
  valid_before?: number;
  description?: string;
};

export type PaymentPayload = {
  version?: number;
  chain_id: string;
  from: string;
  to: string;
  amount: string;
  valid_after: number;
  valid_before: number;
  nonce: string;
  signature?: string | number[];
  /** Payer-signed TxX402 object required for middleware settlement. */
  settlement_tx?: object;
  [key: string]: unknown;
};

export type X402ClientOptions = {
  connection: Connection;
  chainId?: string;
};

export class X402Client {
  readonly connection: Connection;
  readonly chainId: string;

  constructor(opts: X402ClientOptions) {
    this.connection = opts.connection;
    this.chainId = opts.chainId ?? opts.connection.chainId;
  }

  static fromConnection(connection: Connection): X402Client {
    return new X402Client({ connection });
  }

  buildRequirements(payTo: string, amountSPLD: string, windowSecs = 300): PaymentRequirements {
    const now = Math.floor(Date.now() / 1000);
    return {
      pay_to: assertAddress(payTo),
      amount: fromSPLD(amountSPLD),
      valid_after: now - 30,
      valid_before: now + windowSecs,
      description: 'API access payment',
    };
  }

  buildUnsignedPayload(opts: {
    from: string;
    to: string;
    amount: string;
    amountSPLD?: string;
    validWindowSecs?: number;
  }): Omit<PaymentPayload, 'signature'> {
    const now = Math.floor(Date.now() / 1000);
    const window = opts.validWindowSecs ?? 300;
    return {
      version: 1,
      chain_id: this.chainId,
      from: assertAddress(opts.from),
      to: assertAddress(opts.to),
      amount: opts.amountSPLD != null ? fromSPLD(opts.amountSPLD) : opts.amount,
      valid_after: now - 30,
      valid_before: now + window,
      nonce: randomNonceHex(),
    };
  }

  async verify(requirements: PaymentRequirements, payload: PaymentPayload) {
    return this.connection.x402Verify(requirements, payload);
  }

  async settle(
    requirements: PaymentRequirements,
    payload: PaymentPayload,
    settlementTx: object,
  ): Promise<X402SettleResult> {
    return this.connection.x402Settle(requirements, payload, settlementTx);
  }

  /** Full pay flow using a connected wallet (sign + optional verify/settle). */
  async pay(wallet: SplendorPQBridge, opts: X402PayOpts) {
    return wallet.payX402(opts);
  }
}

export type X402SettleResult = {
  success?: boolean;
  status?: 'submitted' | 'confirmed' | 'failed' | string;
  tx_hash?: string;
  error?: string;
};

/** Named policy for payment-gated next(). Default is receipt confirmation. */
export type X402SettlementPolicy = 'receipt' | 'optimistic-submitted';

export type X402MiddlewareConfig = {
  payTo: string;
  price: string | Record<string, string>;
  connection: Connection;
  description?: string;
  /**
   * When true (default), verify-only payments are rejected; the X-Payment
   * payload must include `settlement_tx` and settle must succeed before next().
   * Prefer wallet.payX402 on the client to produce a settled payment.
   */
  requireSettlement?: boolean;
  /**
   * How a queued settlement is treated for payment-gated access.
   * - `receipt` (default): call next() only after a successful on-chain receipt
   * - `optimistic-submitted`: call next() after mempool admission (named
   *   optimistic policy; a queued tx is still not described as confirmed)
   */
  settlementPolicy?: X402SettlementPolicy;
  confirmTimeoutMs?: number;
  confirmPollMs?: number;
  /**
   * Where consumption records live — what each settled payment already bought.
   *
   * A settled payment confirms for as long as its authorisation window is open,
   * so without a record the same X-Payment header buys an unlimited number of
   * requests. The default is a per-process in-memory store, which is
   * authoritative for one process for as long as it runs and no longer:
   *
   *   - a restart (a rolling deploy included) forgets every claim, so a payment
   *     whose window is still open is claimed again and buys `reuse.maxGrants`
   *     more requests;
   *   - behind k replicas each replica grants the same payment up to
   *     `reuse.maxGrants` times, for k × maxGrants in total.
   *
   * Both are properties of a per-process store, not bugs to be fixed inside
   * one. Supply a shared (e.g. Redis) store — the interface is three methods —
   * for anything beyond a single long-lived instance.
   */
  consumptionStore?: X402ConsumptionStore;
  /**
   * How much one settled payment buys. Defaults to
   * `{ maxGrants: 3, replayWindowMs: 180_000 }`: the request that was paid for
   * plus two retries of it, and only within three minutes of the claim.
   *
   * Raise it for a client that legitimately retries harder; set
   * `maxGrants: 1` (or `replayWindowMs: 0`) to sell strictly one request with no
   * replay at all, accepting that a dropped response then costs the payer a
   * second payment. There is no unbounded setting: an unbounded replay is a
   * subscription sold at the price of one request.
   *
   * Once either bound is reached the payment is refused with 402
   * `Payment Reuse Exhausted`, which is a different answer from the
   * `Payment Already Consumed` a different binding gets.
   */
  reuse?: Partial<X402ReuseLimits>;
  /**
   * What a payment is bound to. Default: `METHOD path`, narrowed by a client
   * `Idempotency-Key` / `X-Request-Id` header when one is present.
   *
   * Retrying the SAME binding replays the grant, up to `reuse` above; a
   * DIFFERENT binding carrying the same payment is refused with 402
   * `Payment Already Consumed`.
   */
  bindRequest?: (req: X402BindableRequest) => string;
};

type ReqLike = X402BindableRequest;
type ResLike = {
  status: (code: number) => ResLike;
  json: (body: unknown) => void;
};
type NextFn = () => void;

function resolvePrice(price: string | Record<string, string>, path: string): string {
  if (typeof price === 'string') return price;
  for (const [pattern, p] of Object.entries(price)) {
    if (path.startsWith(pattern) || path.includes(pattern)) return p;
  }
  const values = Object.values(price);
  if (values.length === 0) {
    throw WalletError.invalidAmount('createX402Middleware: price map is empty');
  }
  return values[0]!;
}

/**
 * Framework-agnostic X402 middleware factory (works with Express-style handlers).
 *
 * Default behaviour requires on-chain settlement (`settlement_tx` in the payment
 * payload). Verify-only access is not granted — use `wallet.payX402` to pay.
 */
export function createX402Middleware(config: X402MiddlewareConfig) {
  if (!config.payTo) {
    throw new WalletError('INVALID_ADDRESS', 'createX402Middleware: payTo is required');
  }
  const client = X402Client.fromConnection(config.connection);
  const requireSettlement = config.requireSettlement !== false;
  const settlementPolicy: X402SettlementPolicy = config.settlementPolicy ?? 'receipt';
  const consumption = config.consumptionStore ?? createMemoryConsumptionStore();
  // Checked at construction, not per request: a bad allowance should fail the
  // server's boot, not every payment it takes.
  const reuse = resolveReuseLimits(config.reuse);
  const bindRequest = config.bindRequest ?? defaultRequestBinding;

  return async function x402Middleware(req: ReqLike, res: ResLike, next: NextFn) {
    const header = req.headers['x-payment'];
    const paymentHeader = Array.isArray(header) ? header[0] : header;

    if (!paymentHeader) {
      const price = resolvePrice(config.price, req.path);
      const requirements = client.buildRequirements(config.payTo, price);
      return res.status(402).json({ error: 'Payment Required', requirements });
    }

    try {
      const payload = JSON.parse(paymentHeader) as PaymentPayload;
      const price = resolvePrice(config.price, req.path);
      const requirements = client.buildRequirements(config.payTo, price);
      const verifyResult = (await client.verify(requirements, payload)) as {
        valid?: boolean;
        reason?: string;
        nonce_used?: boolean;
      };
      const nonceUsed = verifyResult.nonce_used === true || isNonceAlreadyUsed(verifyResult.reason);

      if (!verifyResult.valid && !(nonceUsed && requireSettlement)) {
        return res.status(402).json({
          error: 'Invalid Payment',
          reason: verifyResult.reason,
        });
      }

      // Reserve, then settle. The claim is taken BEFORE the settle call, so two
      // concurrent requests carrying the same payment cannot both be settling
      // while each believes it owns the payment; and a settlement that never
      // establishes releases the claim, so a failed attempt does not burn the
      // payer's money without giving them the request. The reuse allowance is
      // charged by the same call, for the same reason: a check that concurrent
      // requests can all pass before any of them pays is not a bound.
      const paymentId = paymentIdentity(payload);
      const binding = bindRequest(req);
      const now = Date.now();
      // Retention is clamped, so a payment signed to stay valid past the clamp
      // outlives the record that counts its reuse: the record expires, the next
      // presentation claims afresh, and the payment buys another full allowance
      // — repeating for as long as the payer's window runs. The node cannot
      // refuse it either, because the nonce really did settle. So a payment this
      // server cannot police for its whole life is declined at the door.
      if (paymentOutlivesConsumption(payload, now)) {
        return res.status(402).json({
          error: 'Payment Window Too Long',
          reason:
            'this payment stays valid for longer than the server retains its consumption record, ' +
            'so its reuse could not be bounded for the whole of that window; sign a shorter valid_before',
          requirements,
        });
      }
      const claim = await consumption.claim(paymentId, binding, {
        now,
        expiresAt: consumptionExpiry(payload, now),
        maxGrants: reuse.maxGrants,
        replayWindowMs: reuse.replayWindowMs,
      });
      if (claim.status === 'conflict') {
        return res.status(402).json({
          error: 'Payment Already Consumed',
          reason:
            'this payment was already spent on a different request ' +
            `(${claim.record.binding}); one settled payment buys one request. ` +
            'Retry the same request to replay it, or pay again',
          requirements,
        });
      }
      // Nothing is wrong with the request — the payment behind it is used up.
      // Kept distinct from the conflict above so a client can tell "ask for
      // something else" from "pay again", and so an operator reading logs can
      // tell a misrouted credential from an exhausted one.
      if (claim.status === 'exhausted') {
        return res.status(402).json({
          error: 'Payment Reuse Exhausted',
          reason: claim.reason,
          limit: claim.limit,
          grants: claim.record.grants,
          maxGrants: reuse.maxGrants,
          replayWindowMs: reuse.replayWindowMs,
          requirements,
        });
      }
      if (claim.status === 'unavailable') {
        return res.status(503).json({ error: 'Payment Consumption Unavailable', reason: claim.reason });
      }
      // A retry of the same request that already got through is served again
      // without re-settling: the payer must not be charged twice for a dropped
      // response. The claim above has already charged this replay against the
      // allowance, so the retries are counted — and the one that goes over is
      // refused as exhausted rather than reaching here.
      if (claim.status === 'replay' && claim.record.granted) {
        next();
        return;
      }

      // Every in-flight settlement owns a token, including a same-binding
      // replay that arrived before the first settlement completed. Grant and
      // release both present it so a stale completion cannot mutate a newer
      // claim and one failed attempt cannot delete another attempt's claim.
      const token = claim.token;

      if (requireSettlement) {
        const settlementTx = payload.settlement_tx;
        if (!settlementTx || typeof settlementTx !== 'object') {
          await consumption.release(paymentId, token);
          return res.status(402).json({
            error: 'Settlement Required',
            reason:
              'X-Payment must include settlement_tx after verify; use wallet.payX402 to settle on-chain',
            requirements,
          });
        }
        let settleResult: X402SettleResult;
        try {
          settleResult = await client.settle(requirements, payload, settlementTx);
        } catch (err) {
          // A settle call that threw — a dropped connection, a timeout, a node
          // that is down — established nothing, so the reservation goes back.
          // It used to fall through to the outer catch, which released nothing
          // and answered "Malformed X-Payment header": the payer lost a grant
          // from the allowance for a request nobody served, and was told their
          // header was the problem.
          await consumption.release(paymentId, token);
          return res.status(502).json({
            error: 'Settlement Unavailable',
            reason: `could not reach the chain to settle this payment: ${errText(err)}`,
            requirements,
          });
        }
        let granted: true | { error: string; reason: string; status?: string };
        try {
          granted = await grantAfterSettle(config, payload, settleResult, settlementPolicy);
        } catch (err) {
          await consumption.release(paymentId, token);
          return res.status(502).json({
            error: 'Settlement Unavailable',
            reason: `could not confirm this payment on chain: ${errText(err)}`,
            requirements,
          });
        }
        if (granted === true) {
          await consumption.grant(paymentId, token, settleResult.tx_hash);
          next();
          return;
        }
        await consumption.release(paymentId, token);
        return res.status(402).json(granted);
      }

      await consumption.grant(paymentId, token);
      next();
    } catch {
      return res.status(400).json({ error: 'Malformed X-Payment header' });
    }
  };
}

/** A thrown value rendered for an operator, without leaking a stack trace. */
function errText(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  return String(err);
}

function isNonceAlreadyUsed(reason?: string): boolean {
  return typeof reason === 'string' && /nonce already used/i.test(reason);
}

function settleConfirmed(result: X402SettleResult): boolean {
  return result.status === 'confirmed' || (result.success === true && result.status !== 'submitted');
}

function settleSubmitted(result: X402SettleResult): boolean {
  return result.status === 'submitted';
}

async function grantAfterSettle(
  config: X402MiddlewareConfig,
  payload: PaymentPayload,
  settleResult: X402SettleResult,
  policy: X402SettlementPolicy,
): Promise<true | { error: string; reason: string; status?: string }> {
  if (settleResult.status === 'failed' || (settleResult.error && !settleConfirmed(settleResult) && !settleSubmitted(settleResult))) {
    return {
      error: 'Settlement Failed',
      reason: settleResult.error || 'settle returned unsuccessful',
      status: settleResult.status,
    };
  }
  if (settleConfirmed(settleResult)) {
    return true;
  }
  if (settleSubmitted(settleResult) || settleResult.success === false) {
    if (policy === 'optimistic-submitted') {
      return true;
    }
    const wait = config.connection.waitForConfirmation;
    if (typeof wait !== 'function' || !settleResult.tx_hash || !payload.from) {
      return {
        error: 'Settlement Unconfirmed',
        reason:
          'settlement is only submitted (queued); payment-gated access requires a successful receipt unless settlementPolicy is optimistic-submitted',
        status: 'submitted',
      };
    }
    try {
      await wait.call(config.connection, {
        from: String(payload.from),
        nonceBefore: 0,
        hash: settleResult.tx_hash,
        timeoutMs: config.confirmTimeoutMs,
        pollMs: config.confirmPollMs,
      });
      return true;
    } catch (e) {
      return {
        error: 'Settlement Unconfirmed',
        reason: e instanceof Error ? e.message : 'settlement receipt not confirmed',
        status: 'submitted',
      };
    }
  }
  return {
    error: 'Settlement Failed',
    reason: settleResult.error || 'settle returned unsuccessful',
    status: settleResult.status,
  };
}

export { fromSPLD, toSPLD };
export type { X402PayOpts };
