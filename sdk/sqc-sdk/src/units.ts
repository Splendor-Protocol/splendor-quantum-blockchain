/**
 * Generic token / module amount helpers (any decimals).
 * Prefer fromSPLD / toSPLD for native 18-decimal SPLD.
 */
export function fromUnits(amount: string | number, decimals: number): string {
  try {
    const d = Math.max(0, Math.floor(decimals));
    const parts = String(amount).trim().split('.');
    const whole = BigInt(parts[0] || '0');
    const fracStr = (parts[1] || '').padEnd(d, '0').slice(0, d);
    const frac = fracStr ? BigInt(fracStr) : 0n;
    const base = 10n ** BigInt(d);
    return String(whole * base + frac);
  } catch {
    return '0';
  }
}

export function toUnits(raw: string | number | bigint, decimals: number): string {
  try {
    const d = Math.max(0, Math.floor(decimals));
    const u = BigInt(raw);
    const base = 10n ** BigInt(d);
    const whole = u / base;
    const frac = u % base;
    if (d === 0) return String(whole);
    const fracStr = frac.toString().padStart(d, '0').replace(/0+$/, '') || '0';
    return `${whole}.${fracStr}`;
  } catch {
    return dFallback(decimals);
  }
}

function dFallback(decimals: number): string {
  return decimals > 0 ? `0.${'0'.repeat(Math.min(decimals, 6))}` : '0';
}
