/**
 * Payment request links, in the style of BIP-21 / EIP-681:
 *
 *   splendor:SQ:<64 hex>?amount=1.5&chain=splendor-quantum-1
 *
 * `amount` is human SPLD (decimal, at most 18 places). `chain` lets a wallet refuse a request
 * meant for another chain. Unknown parameters are ignored by `parsePaymentUri`.
 */
export const PAYMENT_URI_SCHEME = 'splendor';

const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const AMOUNT_RE = /^(?:0|[1-9]\d*)(?:\.\d{1,18})?$/;
const CHAIN_RE = /^[A-Za-z0-9._-]{1,64}$/;

export type PaymentRequest = { address: string; amount?: string; chainId?: string };

export function buildPaymentUri(req: PaymentRequest): string {
  const address = req.address.trim();
  if (!ADDRESS_RE.test(address)) throw new Error('buildPaymentUri: expected an SQ: address');
  const addr = `SQ:${address.slice(3).toLowerCase()}`;
  const params = new URLSearchParams();
  const amount = req.amount?.trim();
  if (amount) {
    if (!AMOUNT_RE.test(amount) || /^0(?:\.0+)?$/.test(amount)) throw new Error('buildPaymentUri: invalid amount');
    params.set('amount', amount);
  }
  if (req.chainId) {
    if (!CHAIN_RE.test(req.chainId)) throw new Error('buildPaymentUri: invalid chain id');
    params.set('chain', req.chainId);
  }
  const qs = params.toString();
  return `${PAYMENT_URI_SCHEME}:${addr}${qs ? `?${qs}` : ''}`;
}

/** Parse a payment link (or a bare `SQ:` address). Returns null for anything malformed. */
export function parsePaymentUri(raw: string): PaymentRequest | null {
  const s = raw.trim();
  if (ADDRESS_RE.test(s)) return { address: `SQ:${s.slice(3).toLowerCase()}` };
  const prefix = `${PAYMENT_URI_SCHEME}:`;
  if (s.slice(0, prefix.length).toLowerCase() !== prefix) return null;
  const rest = s.slice(prefix.length);
  const q = rest.indexOf('?');
  const address = q < 0 ? rest : rest.slice(0, q);
  if (!ADDRESS_RE.test(address)) return null;
  const out: PaymentRequest = { address: `SQ:${address.slice(3).toLowerCase()}` };
  if (q >= 0) {
    const params = new URLSearchParams(rest.slice(q + 1));
    const amount = params.get('amount');
    if (amount != null) {
      if (!AMOUNT_RE.test(amount) || /^0(?:\.0+)?$/.test(amount)) return null;
      out.amount = amount;
    }
    const chain = params.get('chain');
    if (chain != null) {
      if (!CHAIN_RE.test(chain)) return null;
      out.chainId = chain;
    }
  }
  return out;
}
