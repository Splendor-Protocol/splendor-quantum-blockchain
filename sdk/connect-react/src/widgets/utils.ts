/** Shared helpers for dApp widgets. */
import { canonicalizeAddress, fromUnits, toUnits } from '@splendor/sqc-sdk';
import { isSafeExplorerUrl } from '@splendor/sqc-sdk/networks';
import { COMMON_LABELS, formatLabel, type CommonWidgetLabels } from './labels.js';

export function truncateAddress(addr: string, left = 6, right = 4): string {
  if (addr.length <= left + right + 1) return addr;
  return `${addr.slice(0, left)}…${addr.slice(-right)}`;
}

const AMOUNT_RE = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;

/** Decimal human amount strictly greater than zero (and, when given, at most `maxDecimals` places). */
export function isPositiveAmount(v: string, maxDecimals?: number): boolean {
  const t = v.trim();
  if (!t || !AMOUNT_RE.test(t) || /^0(?:\.0+)?$/.test(t)) return false;
  return maxDecimals == null || fractionDigits(t) <= maxDecimals;
}

/** Number of digits after the decimal point. */
export function fractionDigits(v: string): number {
  const i = v.indexOf('.');
  return i < 0 ? 0 : v.length - i - 1;
}

/** Canonical `SQ:` address (lowercase hex), or null for `0x`, raw hex, short, or empty input. */
export function normalizeSqAddress(raw: string | null | undefined): string | null {
  if (typeof raw !== 'string') return null;
  const t = raw.trim();
  if (!t) return null;
  return canonicalizeAddress(t);
}

const DECIMAL_RE = /^\d+(?:\.\d+)?$/;

function splitDecimal(v: string): [string, string] {
  const [int, frac = ''] = v.split('.');
  return [int.replace(/^0+(?=\d)/, ''), frac.replace(/0+$/, '')];
}

/**
 * Compare two non-negative decimal strings without floating point.
 * Returns -1 / 0 / 1, or null when either side is not a plain decimal.
 */
export function compareDecimal(a: string, b: string): -1 | 0 | 1 | null {
  const x = a.trim();
  const y = b.trim();
  if (!DECIMAL_RE.test(x) || !DECIMAL_RE.test(y)) return null;
  const [ai, af] = splitDecimal(x);
  const [bi, bf] = splitDecimal(y);
  if (ai.length !== bi.length) return ai.length < bi.length ? -1 : 1;
  if (ai !== bi) return ai < bi ? -1 : 1;
  const len = Math.max(af.length, bf.length);
  const pa = af.padEnd(len, '0');
  const pb = bf.padEnd(len, '0');
  if (pa === pb) return 0;
  return pa < pb ? -1 : 1;
}

/**
 * Native SPLD left after paying `feeRaw` (atto SPLD), as a human amount floored at 0.
 * Null when `balance` is null or either value is unparsable.
 */
export function spendableAfterFee(balance: string | null | undefined, feeRaw: string | null | undefined): string | null {
  if (balance == null) return null;
  try {
    const left = BigInt(fromUnits(balance, 18)) - BigInt(feeRaw ?? '0');
    return toUnits(left > 0n ? left : 0n, 18);
  } catch {
    return null;
  }
}

/** True when `amount` plus `feeRaw` (atto SPLD) is more than the human SPLD `balance`. */
export function exceedsBalanceWithFee(
  amount: string,
  feeRaw: string | null | undefined,
  balance: string | null | undefined,
): boolean {
  if (balance == null || !feeRaw) return false;
  try {
    return BigInt(fromUnits(amount, 18)) + BigInt(feeRaw) > BigInt(fromUnits(balance, 18));
  } catch {
    return false;
  }
}

export type TransferReviewReason = 'invalid-address' | 'self' | 'bad-amount' | 'too-precise' | 'insufficient';

export type TransferReview =
  | { ok: true; to: string; amount: string }
  | { ok: false; reason: TransferReviewReason };

/**
 * Pre-send checks shared by transfer widgets. `balance` is the human balance in the
 * same unit as `amount`; when null or unparsable the balance check is skipped.
 * `decimals` caps the fractional digits (18 for SPLD, the token's decimals for QRC-20).
 */
export function reviewTransfer(input: {
  to: string;
  amount: string;
  balance?: string | null;
  self?: string | null;
  decimals?: number;
}): TransferReview {
  const to = normalizeSqAddress(input.to);
  if (!to) return { ok: false, reason: 'invalid-address' };
  const self = normalizeSqAddress(input.self);
  if (self && self === to) return { ok: false, reason: 'self' };
  const amount = input.amount.trim();
  if (!isPositiveAmount(amount)) return { ok: false, reason: 'bad-amount' };
  if (input.decimals != null && fractionDigits(amount) > input.decimals) {
    return { ok: false, reason: 'too-precise' };
  }
  if (input.balance != null && compareDecimal(amount, input.balance) === 1) {
    return { ok: false, reason: 'insufficient' };
  }
  return { ok: true, to, amount };
}

export function transferReviewMessage(
  reason: TransferReviewReason,
  symbol = 'SPLD',
  labels: Pick<CommonWidgetLabels, 'invalidAddress' | 'selfSend' | 'badAmount' | 'insufficient'> &
    Partial<Pick<CommonWidgetLabels, 'tooPrecise'>> = COMMON_LABELS,
  decimals?: number,
): string {
  switch (reason) {
    case 'invalid-address':
      return labels.invalidAddress;
    case 'self':
      return labels.selfSend;
    case 'bad-amount':
      return labels.badAmount;
    case 'too-precise':
      return formatLabel(labels.tooPrecise ?? COMMON_LABELS.tooPrecise, { decimals });
    case 'insufficient':
      return formatLabel(labels.insufficient, { symbol });
  }
}

export const RECENT_RECIPIENTS_KEY = 'sqc.connect.recentRecipients';
export const RECENT_RECIPIENTS_MAX = 5;

function recentStore(): Storage | undefined {
  try {
    if (typeof localStorage === 'undefined') return undefined;
    return localStorage;
  } catch {
    return undefined;
  }
}

/** Recently used public `SQ:` recipients, newest first. */
export function getRecentRecipients(): string[] {
  try {
    const raw = recentStore()?.getItem(RECENT_RECIPIENTS_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: string[] = [];
    for (const v of parsed) {
      const addr = typeof v === 'string' ? normalizeSqAddress(v) : null;
      if (addr && !out.includes(addr)) out.push(addr);
      if (out.length >= RECENT_RECIPIENTS_MAX) break;
    }
    return out;
  } catch {
    return [];
  }
}

/** Record a recipient. Anything that is not a valid `SQ:` address is ignored. */
export function rememberRecipient(raw: string): string[] {
  const addr = normalizeSqAddress(raw);
  if (!addr) return getRecentRecipients();
  const next = [addr, ...getRecentRecipients().filter((a) => a !== addr)].slice(
    0,
    RECENT_RECIPIENTS_MAX,
  );
  try {
    recentStore()?.setItem(RECENT_RECIPIENTS_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
  return next;
}

/** Explorer page for an `SQ:` address, or null when the network has no explorer. */
export function addressExplorerUrl(
  explorerUrl: string | null | undefined,
  address: string | null | undefined,
): string | null {
  const addr = normalizeSqAddress(address);
  if (!explorerUrl || !addr) return null;
  const base = explorerUrl.trim().replace(/\/+$/, '');
  if (!base || !isSafeExplorerUrl(base)) return null;
  // `addr` is canonical `SQ:` + 64 hex, so it is already path-safe.
  return `${base}/address/${addr}`;
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/**
 * Allow only https: and raster data: image URLs for connector icons.
 * Rejects svg+xml (scriptable) and http:/javascript:.
 */
export function sanitizeIconUrl(url: string | undefined | null): string | null {
  if (!url || typeof url !== 'string') return null;
  const trimmed = url.trim();
  if (/^data:image\/(png|jpe?g|webp|gif);/i.test(trimmed)) return trimmed;
  if (/^data:image\/svg\+xml/i.test(trimmed)) return null;
  try {
    const u = new URL(trimmed);
    if (u.protocol === 'https:') return u.href;
  } catch {
    return null;
  }
  return null;
}
