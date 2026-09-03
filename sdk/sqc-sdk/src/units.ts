/**
 * Generic token / module amount helpers (any decimals).
 * Prefer fromSPLD / toSPLD for native 18-decimal SPLD.
 * Invalid input throws — never silently returns '0'.
 */
import { BridgeError } from './errors.js';

/** Decimal string: optional sign not allowed; no scientific notation; max one '.'. */
const DECIMAL_RE = /^(0|[1-9]\d*)(\.\d+)?$/;

function parseDecimalParts(amount: string | number, label: string): { whole: string; frac: string } {
  if (typeof amount === 'number') {
    if (!Number.isFinite(amount) || amount < 0) {
      throw BridgeError.invalidAmount(`${label}: invalid number ${amount}`);
    }
    // Reject scientific notation from Number#toString (e.g. 1e+21).
    const s = String(amount);
    if (/[eE]/.test(s)) {
      throw BridgeError.invalidAmount(
        `${label}: use a decimal string (scientific notation not allowed): ${s}`,
      );
    }
    amount = s;
  }
  const trimmed = String(amount).trim();
  if (!trimmed || !DECIMAL_RE.test(trimmed)) {
    throw BridgeError.invalidAmount(`${label}: expected non-negative decimal string, got ${JSON.stringify(amount)}`);
  }
  const parts = trimmed.split('.');
  return { whole: parts[0]!, frac: parts[1] || '' };
}

export function fromUnits(amount: string | number, decimals: number): string {
  if (!Number.isFinite(decimals) || decimals < 0 || !Number.isInteger(decimals)) {
    throw BridgeError.invalidAmount(`fromUnits: decimals must be a non-negative integer, got ${decimals}`);
  }
  const d = decimals;
  const { whole, frac } = parseDecimalParts(amount, 'fromUnits');
  if (frac.length > d) {
    throw BridgeError.invalidAmount(
      `fromUnits: too many fractional digits (max ${d}), got ${frac.length}`,
    );
  }
  const wholeBi = BigInt(whole);
  const fracStr = frac.padEnd(d, '0').slice(0, d);
  const fracBi = fracStr ? BigInt(fracStr) : 0n;
  const base = 10n ** BigInt(d);
  return String(wholeBi * base + fracBi);
}

export function toUnits(raw: string | number | bigint, decimals: number): string {
  if (!Number.isFinite(decimals) || decimals < 0 || !Number.isInteger(decimals)) {
    throw BridgeError.invalidAmount(`toUnits: decimals must be a non-negative integer, got ${decimals}`);
  }
  const d = decimals;
  let u: bigint;
  try {
    if (typeof raw === 'bigint') {
      u = raw;
    } else if (typeof raw === 'number') {
      if (!Number.isFinite(raw) || !Number.isInteger(raw) || raw < 0) {
        throw BridgeError.invalidAmount(`toUnits: invalid raw number ${raw}`);
      }
      u = BigInt(raw);
    } else {
      const s = String(raw).trim();
      if (!/^(0|[1-9]\d*)$/.test(s)) {
        throw BridgeError.invalidAmount(`toUnits: expected non-negative integer string, got ${JSON.stringify(raw)}`);
      }
      u = BigInt(s);
    }
  } catch (e) {
    if (e instanceof BridgeError) throw e;
    throw BridgeError.invalidAmount(`toUnits: cannot parse ${JSON.stringify(raw)}`);
  }
  if (u < 0n) {
    throw BridgeError.invalidAmount(`toUnits: negative amount`);
  }
  const base = 10n ** BigInt(d);
  const whole = u / base;
  const frac = u % base;
  if (d === 0) return String(whole);
  const fracStr = frac.toString().padStart(d, '0').replace(/0+$/, '') || '0';
  return `${whole}.${fracStr}`;
}
