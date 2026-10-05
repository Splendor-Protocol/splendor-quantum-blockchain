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

/**
 * A payment evaluation request as seen by the middleware. Only the fields the
 * middleware consumes are declared; extra request metadata is ignored.
 */
export type X402Request = { path: string; headers: Record<string, string | string[] | undefined> };

/**
 * Application-level consumption record. The middleware binds each confirmed
 * settlement to the specific service request it pays for, so replaying one real
 * payment cannot silently purchase an unrelated request. store may be an
 * in-memory Map, a durable KV table, or anything else — only get/put are
 * required and both should be idempotent (put-if-absent on the server side).
 */
export type X402ConsumptionStore = {
  get: (key: string) => Promise<string | undefined>;
  put: (key: string, value: string) => Promise<void>;
  /**
   * Atomic put-if-absent. Returns true when the key was written, false when it already
   * existed. Strongly recommended for multi-instance servers; without it get + put can race.
   */
  putIfAbsent?: (key: string, value: string) => Promise<boolean>;
};

/** Bounded in-memory consumption store used when none is configured (single process only). */
export function createMemoryConsumptionStore(maxEntries = 10_000): X402ConsumptionStore {
  const data = new Map<string, string>();
  const evict = () => {
    while (data.size > maxEntries) {
      const oldest = data.keys().next().value;
      if (oldest === undefined) break;
      data.delete(oldest);
    }
  };
  return {
    get: async (k) => data.get(k),
    put: async (k, v) => {
      data.set(k, v);
      evict();
    },
    putIfAbsent: async (k, v) => {
      if (data.has(k)) return false;
      data.set(k, v);
      evict();
      return true;
    },
  };
}

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
   * Application-level consumption record. A confirmed payment is consumed for exactly
   * one entitlement key; retries of the same request are idempotent, and the same
   * payment used for a different key is rejected. Defaults to a bounded in-memory
   * store (single process). Use a durable store with `putIfAbsent` when running more
   * than one server instance.
   */
  consumptionStore?: X402ConsumptionStore;
  /**
   * Derive the entitlement key for a request. Defaults to req.path.
   */
  requestKey?: (req: X402Request) => string;
};

type ReqLike = { path: string; headers: Record<string, string | string[] | undefined> };
type ResLike = {
  status: (code: number) => ResLike;
  json: (body: unknown) => void;
};
type NextFn = () => void;

/**
 * Exact match first, then the longest pattern that is a path-segment prefix
 * (`/api` matches `/api` and `/api/x`, not `/apix`). Unmatched paths return null.
 */
export function resolvePrice(price: string | Record<string, string>, path: string): string | null {
  if (typeof price === 'string') return price;
  if (Object.prototype.hasOwnProperty.call(price, path)) return price[path]!;
  let best: string | null = null;
  for (const pattern of Object.keys(price)) {
    const base = pattern.endsWith('/') ? pattern.slice(0, -1) : pattern;
    const matches = path === base || path.startsWith(base + '/');
    if (matches && (best === null || pattern.length > best.length)) best = pattern;
  }
  return best === null ? null : price[best]!;
}

function sameAddress(a: unknown, b: string): boolean {
  try {
    return assertAddress(String(a)).toLowerCase() === assertAddress(b).toLowerCase();
  } catch {
    return false;
  }
}

function amountCovers(paid: unknown, required: string): boolean {
  if (typeof paid !== 'string' || !/^(0|[1-9]\d*)$/.test(paid)) return false;
  return BigInt(paid) >= BigInt(required);
}

function consumptionKeyFor(payload: PaymentPayload): string {
  return `x402:payment:${String(payload.from).toLowerCase()}:${String(payload.nonce).toLowerCase()}`;
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
  if (typeof config.price === 'object' && Object.keys(config.price).length === 0) {
    throw WalletError.invalidAmount('createX402Middleware: price map is empty');
  }
  const client = X402Client.fromConnection(config.connection);
  const requireSettlement = config.requireSettlement !== false;
  const settlementPolicy: X402SettlementPolicy = config.settlementPolicy ?? 'receipt';
  const cfg: X402MiddlewareConfig = {
    ...config,
    consumptionStore: config.consumptionStore ?? createMemoryConsumptionStore(),
  };

  return async function x402Middleware(req: ReqLike, res: ResLike, next: NextFn) {
    const header = req.headers['x-payment'];
    const paymentHeader = Array.isArray(header) ? header[0] : header;
    const price = resolvePrice(config.price, req.path);
    if (price === null) {
      return res.status(403).json({ error: 'No Price', reason: 'no x402 price is configured for this path' });
    }

    if (!paymentHeader) {
      const requirements = client.buildRequirements(config.payTo, price);
      return res.status(402).json({ error: 'Payment Required', requirements });
    }

    try {
      const payload = JSON.parse(paymentHeader) as PaymentPayload;
      const requirements = client.buildRequirements(config.payTo, price);

      if (!payload || typeof payload !== 'object') throw new Error('bad payload');
      if (!sameAddress(payload.from, String(payload.from)) || typeof payload.nonce !== 'string' || !payload.nonce) {
        return res.status(402).json({ error: 'Invalid Payment', reason: 'payment from/nonce are missing or malformed' });
      }
      if (!sameAddress(payload.to, config.payTo)) {
        return res.status(402).json({ error: 'Invalid Payment', reason: 'payment recipient does not match payTo' });
      }
      if (!amountCovers(payload.amount, requirements.amount)) {
        return res.status(402).json({ error: 'Invalid Payment', reason: 'payment amount is below the price' });
      }
      if (payload.chain_id !== client.chainId) {
        return res.status(402).json({ error: 'Invalid Payment', reason: 'payment chain_id does not match this network' });
      }

      const verifyResult = (await client.verify(requirements, payload)) as {
        valid?: boolean;
        reason?: string;
        nonce_used?: boolean;
      };
      const nonceUsed = verifyResult.nonce_used === true || isNonceAlreadyUsed(verifyResult.reason);

      if (nonceUsed) {
        // A spent nonce is only an idempotent retry of a payment this server already
        // consumed for this exact request; it is never re-settled.
        if (await isKnownRetry(cfg, payload, req)) {
          next();
          return;
        }
        return res.status(402).json({
          error: 'Payment Already Used',
          reason: 'this payment nonce was already spent and is not recorded for this request',
        });
      }
      if (!verifyResult.valid) {
        return res.status(402).json({
          error: 'Invalid Payment',
          reason: verifyResult.reason,
        });
      }

      if (requireSettlement) {
        const settlementTx = payload.settlement_tx;
        if (!settlementTx || typeof settlementTx !== 'object') {
          return res.status(402).json({
            error: 'Settlement Required',
            reason:
              'X-Payment must include settlement_tx after verify; use wallet.payX402 to settle on-chain',
            requirements,
          });
        }
        const settleResult = await client.settle(requirements, payload, settlementTx);
        const granted = await grantAfterSettle(config, payload, settleResult, settlementPolicy);
        if (granted === true) {
          const consumptionError = await consumePayment(cfg, payload, settleResult, req);
          if (consumptionError) {
            return res.status(402).json(consumptionError);
          }
          next();
          return;
        }
        return res.status(402).json(granted);
      }

      next();
    } catch {
      return res.status(402).json({ error: 'Payment Check Failed', reason: 'could not verify or settle the payment' });
    }
  };
}

function entitlementOf(config: X402MiddlewareConfig, req: ReqLike): string {
  return config.requestKey ? config.requestKey(req) : req.path;
}

async function isKnownRetry(config: X402MiddlewareConfig, payload: PaymentPayload, req: ReqLike): Promise<boolean> {
  const store = config.consumptionStore;
  if (!store) return false;
  const stored = await store.get(consumptionKeyFor(payload));
  return stored !== undefined && stored === entitlementOf(config, req);
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
        requireReceipt: true,
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

/**
 * Bind a confirmed payment to the specific service request it is purchasing.
 *
 * The consumption key is the payment's identity (payer + nonce); the stored
 * value is the entitlement key derived from the request. A retry of the same
 * request maps to the same entitlement key and is idempotent, but a confirmed
 * payment pointed at a different entitlement key is rejected — one real payment
 * cannot silently purchase repeated/unrelated requests.
 */
async function consumePayment(
  config: X402MiddlewareConfig,
  payload: PaymentPayload,
  settleResult: X402SettleResult,
  req: ReqLike,
): Promise<{ error: string; reason: string } | undefined> {
  const store = config.consumptionStore;
  if (!store) return undefined;
  const expectedEntitlement = entitlementOf(config, req);
  const consumptionKey = consumptionKeyFor(payload);

  if (store.putIfAbsent && (await store.putIfAbsent(consumptionKey, expectedEntitlement))) {
    return undefined;
  }
  const stored = await store.get(consumptionKey);
  if (stored !== undefined) {
    if (stored === expectedEntitlement) {
      // Same request retrying with an already-consumed payment: idempotent.
      return undefined;
    }
    return {
      error: 'Payment Already Consumed',
      reason:
        'this payment identity was already consumed for a different request; it cannot purchase this request',
    };
  }

  await store.put(consumptionKey, expectedEntitlement);
  return undefined;
}

export { fromSPLD, toSPLD };
export type { X402PayOpts };
