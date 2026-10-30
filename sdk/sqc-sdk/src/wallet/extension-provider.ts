/**
 * Dual-method aliases between extension (`sqc_*` / `wallet_*`) and in-page SDK (`splendor_*`).
 * Ground truth: Splendor Quantum Wallet inject speaks `sqc_*` + `wallet_disconnect`.
 * Content script only forwards **array** params — object params become `[]`.
 */
import { BridgeError } from '../errors.js';

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
  wallet_disconnect: 'splendor_disconnect',
  sqc_switchNetwork: 'splendor_switchNetwork',
  sqc_moduleCall: 'splendor_moduleCall',
  sqc_signOrder: 'splendor_signOrder',
  sqc_signPermit: 'splendor_signPermit',
  // Canonical → extension preferred when talking TO extension
  splendor_accounts: 'sqc_accounts',
  splendor_connect: 'sqc_requestAccounts',
  splendor_chainId: 'sqc_chainId',
  splendor_sendTransaction: 'sqc_sendTransaction',
  splendor_signMessage: 'sqc_sign',
  splendor_signTransaction: 'sqc_signTransaction',
  // Extension uses wallet_disconnect (origin-scoped); sqc_disconnect is legacy fallback.
  splendor_disconnect: 'wallet_disconnect',
  splendor_switchNetwork: 'sqc_switchNetwork',
  splendor_moduleCall: 'sqc_moduleCall',
  splendor_signOrder: 'sqc_signOrder',
  splendor_signPermit: 'sqc_signPermit',
};

/** Normalize any known alias to the in-page `splendor_*` canonical name. */
export function toCanonicalMethod(method: string): string {
  if (method.startsWith('splendor_')) return method;
  if (method === 'wallet_disconnect' || method === 'sqc_disconnect') return 'splendor_disconnect';
  const mapped = METHOD_ALIASES[method];
  if (mapped?.startsWith('splendor_')) return mapped;
  return method;
}

/** Prefer extension `sqc_*` / `wallet_*` name when calling an extension provider. */
export function toExtensionMethod(method: string): string {
  if (method.startsWith('sqc_') || method.startsWith('wallet_')) return method;
  const mapped = METHOD_ALIASES[method];
  if (mapped && (mapped.startsWith('sqc_') || mapped.startsWith('wallet_'))) return mapped;
  return method;
}

/**
 * Extension content script only forwards Array params. Object params are dropped to [].
 * Pass undefined/null as [] (no-arg methods).
 */
export function toExtensionParams(params?: unknown): unknown[] {
  if (params == null) return [];
  if (Array.isArray(params)) return params;
  return [params];
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
 * Treat as extension only when not an in-page inject.
 * `(isSplendor && !isInPage)` or `(isExtension && !isInPage)`.
 * Page scripts must not spoof via `isExtension: true` + `isInPage: true`.
 */
export function isExtensionProvider(p: unknown): p is SplendorProviderLike {
  if (!p || typeof p !== 'object') return false;
  const x = p as SplendorProviderLike;
  if (typeof x.request !== 'function') return false;
  if (x.isInPage === true) return false;
  if (x.isExtension === true) return true;
  return x.isSplendor === true;
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

/** EIP-6963 reverse-DNS id the Splendor Quantum Wallet announces itself with. */
export const SPLENDOR_WALLET_RDNS = 'network.splendor.wallet';

/** Negative discovery cache — avoid re-waiting full timeout after a miss. */
let notFoundUntil = 0;
const NOT_FOUND_TTL_MS = 30_000;
/**
 * The first provider discovery settled on. Later calls return it even if `window.splendor` is
 * replaced afterwards, so a script that runs after the wallet cannot swap itself in.
 */
let pinnedProvider: SplendorProviderLike | null = null;

/** Clear the discovery caches ("not found" and the pinned provider) — tests / after install. */
export function clearExtensionDiscoveryCache(): void {
  notFoundUntil = 0;
  pinnedProvider = null;
}

function pin(p: SplendorProviderLike): SplendorProviderLike {
  notFoundUntil = 0;
  pinnedProvider = p;
  return p;
}

/**
 * Ask EIP-6963 wallets to announce themselves and return the Splendor one if it answers
 * synchronously (wallets announce from their `eip6963:requestProvider` listener).
 */
function requestAnnouncedProvider(): SplendorProviderLike | undefined {
  if (typeof window === 'undefined' || typeof window.addEventListener !== 'function') return undefined;
  let found: SplendorProviderLike | undefined;
  const onAnnounce = (ev: Event) => {
    const detail = (ev as CustomEvent).detail as { info?: { rdns?: string }; provider?: SplendorProviderLike } | undefined;
    if (!found && detail?.info?.rdns === SPLENDOR_WALLET_RDNS && isExtensionProvider(detail.provider)) found = detail.provider;
  };
  window.addEventListener('eip6963:announceProvider', onAnnounce);
  try {
    window.dispatchEvent(new Event('eip6963:requestProvider'));
  } catch {
    /* ignore */
  } finally {
    window.removeEventListener('eip6963:announceProvider', onAnnounce);
  }
  return found;
}

/**
 * Wait for the Splendor Quantum Wallet (EIP-6963 announcement, then `window.splendor` poll and
 * `splendor#initialized`). An EIP-6963 announcement with the Splendor rdns wins over a
 * `window.splendor` global, and the provider found first stays pinned for the page's lifetime.
 * Any same-origin script can still fake either source; this only removes the easiest swaps.
 * After a miss, caches "not found" for 30s so ConnectModal reopen stays fast.
 */
export async function discoverExtensionProvider(
  timeoutMs = 5_000,
): Promise<SplendorProviderLike> {
  if (pinnedProvider && isExtensionProvider(pinnedProvider)) return pinnedProvider;

  const announced = requestAnnouncedProvider();
  if (announced) return pin(announced);
  const existing = getExtensionProvider();
  if (existing) return pin(existing);

  if (typeof window === 'undefined') {
    throw BridgeError.noProvider(
      'Splendor Quantum Wallet extension not found (window.splendor)',
    );
  }

  if (Date.now() < notFoundUntil) {
    throw BridgeError.noProvider(
      'Splendor Quantum Wallet extension not found. Please install it.',
    );
  }

  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs;
    let settled = false;

    const finish = (p: SplendorProviderLike | undefined) => {
      if (settled || !p || !isExtensionProvider(p)) return;
      settled = true;
      cleanup();
      resolve(pin(p));
    };

    const fail = () => {
      if (settled) return;
      settled = true;
      notFoundUntil = Date.now() + NOT_FOUND_TTL_MS;
      cleanup();
      reject(
        BridgeError.noProvider(
          'Splendor Quantum Wallet extension not found. Please install it.',
        ),
      );
    };

    const onAnnounce = (ev: Event) => {
      const detail = (ev as CustomEvent).detail as
        | { info?: { rdns?: string }; provider?: SplendorProviderLike }
        | undefined;
      if (detail?.info?.rdns === SPLENDOR_WALLET_RDNS && detail.provider) {
        finish(detail.provider);
      }
    };

    const onInit = (ev: Event) => {
      const detail = (ev as CustomEvent).detail as SplendorProviderLike | undefined;
      if (detail && isExtensionProvider(detail)) finish(detail);
      else finish(getExtensionProvider());
    };

    const poll = setInterval(() => {
      finish(getExtensionProvider());
      if (!settled && Date.now() > deadline) fail();
    }, 100);

    const cleanup = () => {
      clearInterval(poll);
      window.removeEventListener('eip6963:announceProvider', onAnnounce);
      window.removeEventListener('splendor#initialized', onInit);
    };

    window.addEventListener('eip6963:announceProvider', onAnnounce);
    window.addEventListener('splendor#initialized', onInit);
    try {
      window.dispatchEvent(new Event('eip6963:requestProvider'));
    } catch {
      /* ignore */
    }

    finish(getExtensionProvider());
    if (!settled && Date.now() > deadline) fail();
  });
}

function isUnsupportedMethodError(e: unknown): boolean {
  const msg = e instanceof Error ? e.message : String(e);
  if (/unsupported|unknown method|not support|4200/i.test(msg)) return true;
  const code =
    e && typeof e === 'object' && 'code' in e ? (e as { code?: unknown }).code : undefined;
  return code === 4200 || code === '4200';
}

/**
 * Call provider with preferred method, falling back to its alias on unsupported-method errors.
 * Always coerces params to an array for the extension content-script boundary.
 */
export async function requestWithAlias(
  provider: SplendorProviderLike,
  preferred: string,
  params?: unknown,
): Promise<unknown> {
  const wireParams = toExtensionParams(params);
  const tryMethods = [preferred];
  // Disconnect: wallet_disconnect is canonical on extension; sqc_disconnect is a soft fallback.
  if (preferred === 'wallet_disconnect' || preferred === 'splendor_disconnect') {
    tryMethods.push('wallet_disconnect', 'sqc_disconnect');
  } else {
    const alt = METHOD_ALIASES[preferred];
    if (alt && alt !== preferred) tryMethods.push(alt);
  }
  const unique = [...new Set(tryMethods.map(toExtensionMethod))];

  let lastErr: unknown;
  for (let i = 0; i < unique.length; i++) {
    const method = unique[i]!;
    try {
      return await provider.request({ method, params: wireParams });
    } catch (e) {
      lastErr = e;
      const retryable = i < unique.length - 1 && isUnsupportedMethodError(e);
      if (!retryable) throw e;
    }
  }
  throw lastErr;
}
