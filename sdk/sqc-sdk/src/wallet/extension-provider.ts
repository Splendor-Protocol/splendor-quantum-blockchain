/**
 * Dual-method aliases between extension (`sqc_*`) and in-page SDK (`splendor_*`).
 * See monorepo README §splendor-quantum-wallet-extension.
 */
export const METHOD_ALIASES: Record<string, string> = {
  // Extension → canonical in-page
  sqc_accounts: 'splendor_accounts',
  sqc_requestAccounts: 'splendor_connect',
  sqc_chainId: 'splendor_chainId',
  sqc_sendTransaction: 'splendor_sendTransaction',
  sqc_sign: 'splendor_signMessage',
  sqc_signMessage: 'splendor_signMessage',
  sqc_signTransaction: 'splendor_signTransaction',
  sqc_disconnect: 'splendor_disconnect',
  sqc_switchNetwork: 'splendor_switchNetwork',
  // Canonical → extension preferred when talking TO extension
  splendor_accounts: 'sqc_accounts',
  splendor_connect: 'sqc_requestAccounts',
  splendor_chainId: 'sqc_chainId',
  splendor_sendTransaction: 'sqc_sendTransaction',
  splendor_signMessage: 'sqc_sign',
  splendor_signTransaction: 'sqc_signTransaction',
  splendor_disconnect: 'sqc_disconnect',
  splendor_switchNetwork: 'sqc_switchNetwork',
};

/** Normalize any known alias to the in-page `splendor_*` canonical name. */
export function toCanonicalMethod(method: string): string {
  if (method.startsWith('splendor_')) return method;
  const mapped = METHOD_ALIASES[method];
  if (mapped?.startsWith('splendor_')) return mapped;
  return method;
}

/** Prefer extension `sqc_*` name when calling an extension provider. */
export function toExtensionMethod(method: string): string {
  if (method.startsWith('sqc_')) return method;
  const mapped = METHOD_ALIASES[method];
  if (mapped?.startsWith('sqc_')) return mapped;
  return method;
}

export type SplendorProviderLike = {
  isSplendor?: boolean;
  isInPage?: boolean;
  /** Explicit extension flag from the official Chrome extension inject. */
  isExtension?: boolean;
  request: (args: { method: string; params?: unknown }) => Promise<unknown>;
  on?: (event: string, handler: (...args: unknown[]) => void) => void;
  removeListener?: (event: string, handler: (...args: unknown[]) => void) => void;
};

/**
 * Heuristic: treat as extension when `(isSplendor && !isInPage)` or `isExtension === true`.
 * Page scripts can spoof `window.splendor` — dApps should prefer the official extension
 * plus CSP (`script-src` / no ambient inject from untrusted origins).
 */
export function isExtensionProvider(p: unknown): p is SplendorProviderLike {
  if (!p || typeof p !== 'object') return false;
  const x = p as SplendorProviderLike;
  if (typeof x.request !== 'function') return false;
  if (x.isExtension === true) return true;
  return x.isSplendor === true && !x.isInPage;
}

export function isInPageProvider(p: unknown): p is SplendorProviderLike {
  if (!p || typeof p !== 'object') return false;
  const x = p as SplendorProviderLike;
  return x.isSplendor === true && x.isInPage === true && typeof x.request === 'function';
}

/** Return window.splendor when present (extension or in-page). */
export function getSplendorInjected(): SplendorProviderLike | undefined {
  if (typeof window === 'undefined') return undefined;
  const p = window.splendor as SplendorProviderLike | undefined;
  if (p?.isSplendor && typeof p.request === 'function') return p;
  return undefined;
}

export function getExtensionProvider(): SplendorProviderLike | undefined {
  const p = getSplendorInjected();
  return p && isExtensionProvider(p) ? p : undefined;
}

/**
 * Call provider with preferred method, falling back to its alias on unsupported-method errors.
 */
export async function requestWithAlias(
  provider: SplendorProviderLike,
  preferred: string,
  params?: unknown,
): Promise<unknown> {
  try {
    return await provider.request({ method: preferred, params });
  } catch (e) {
    const alt = METHOD_ALIASES[preferred];
    if (!alt || alt === preferred) throw e;
    const msg = e instanceof Error ? e.message : String(e);
    if (!/unsupported|unknown method|not support/i.test(msg)) throw e;
    return provider.request({ method: alt, params });
  }
}
