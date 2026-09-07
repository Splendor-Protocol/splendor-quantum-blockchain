/** Shared helpers for dApp widgets. */

export function truncateAddress(addr: string, left = 6, right = 4): string {
  if (addr.length <= left + right + 1) return addr;
  return `${addr.slice(0, left)}…${addr.slice(-right)}`;
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
 * Allow only https: and data: image URLs for connector icons (XSS / tracking hygiene).
 */
export function sanitizeIconUrl(url: string | undefined | null): string | null {
  if (!url || typeof url !== 'string') return null;
  const trimmed = url.trim();
  if (trimmed.startsWith('data:image/')) return trimmed;
  try {
    const u = new URL(trimmed);
    if (u.protocol === 'https:') return u.href;
  } catch {
    return null;
  }
  return null;
}
