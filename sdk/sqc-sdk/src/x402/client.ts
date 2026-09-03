/**
 * X402 micropayment protocol helpers.
 */
import { fromSPLD, toSPLD } from '../rpc.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import type { X402PayOpts } from '../types.js';
import { randomNonceHex } from '../encoding.js';
import { assertAddress } from '../address.js';

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
    return this.connection.rpc.x402Verify(requirements, payload);
  }

  async settle(requirements: PaymentRequirements, payload: PaymentPayload, settlementTx: object) {
    return this.connection.rpc.x402Settle(requirements, payload, settlementTx);
  }

  /** Full pay flow using a connected wallet (sign + optional verify/settle). */
  async pay(wallet: SplendorPQBridge, opts: X402PayOpts) {
    return wallet.payX402(opts);
  }
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
};

type ReqLike = { path: string; headers: Record<string, string | string[] | undefined> };
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
    throw new Error('createX402Middleware: price map is empty');
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
  if (!config.payTo) throw new Error('createX402Middleware: payTo is required');
  const client = X402Client.fromConnection(config.connection);
  const requireSettlement = config.requireSettlement !== false;

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
      const verifyResult = await client.verify(requirements, payload);
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
        if (settleResult.error || settleResult.success === false) {
          return res.status(402).json({
            error: 'Settlement Failed',
            reason: settleResult.error || 'settle returned unsuccessful',
          });
        }
      }

      next();
    } catch {
      return res.status(400).json({ error: 'Malformed X-Payment header' });
    }
  };
}

export { fromSPLD, toSPLD };
export type { X402PayOpts };
