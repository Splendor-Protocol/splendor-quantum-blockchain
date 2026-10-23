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
