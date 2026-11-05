/**
 * Pure helpers shared by blocks. Exports copy this file next to the blocks, and the gallery's
 * single-file download inlines it, so it must stay dependency-free.
 */

/**
 * Split a list prop into trimmed, non-empty items. Items are separated by newlines when the value
 * contains one (so items may hold commas), otherwise by commas.
 */
export function parseList(raw: string): string[] {
  return raw
    .split(raw.includes('\n') ? /\r?\n/ : ',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Contract call arguments: one comma-separated field, trimmed, empty entries dropped. */
export function parseArgs(raw: string): string[] {
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/** Raw integer units → "1,234.56" (thousands separators, at most `maxFrac` decimals, truncated). */
export function formatUnits(raw: unknown, decimals: number, maxFrac = 2): string {
  let v: bigint;
  try {
    v = BigInt(String(raw));
  } catch {
    return String(raw ?? '—');
  }
  const neg = v < 0n;
  if (neg) v = -v;
  const base = 10n ** BigInt(decimals);
  const whole = (v / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const frac = decimals > 0 ? (v % base).toString().padStart(decimals, '0').slice(0, maxFrac).replace(/0+$/, '') : '';
  return `${neg ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`;
}

/** Ref callback: a slow opacity pulse for loading placeholders. */
export function pulse(el: HTMLElement | null): void {
  el?.animate?.([{ opacity: 0.35 }, { opacity: 0.8 }], { duration: 900, iterations: Infinity, direction: 'alternate' });
}
