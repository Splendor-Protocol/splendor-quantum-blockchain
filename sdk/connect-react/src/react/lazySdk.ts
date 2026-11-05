/**
 * Lazy sqc-sdk subpath imports, each loaded once. Hooks import these instead of the subpaths, so
 * apps that never touch tokens, contracts or the bridge never download those chunks.
 */
function once<T>(load: () => Promise<T>): () => Promise<T> {
  let pending: Promise<T> | null = null;
  return () =>
    (pending ??= load().catch((e) => {
      // A failed chunk load (offline, deploy in progress) must be retried next time, not cached.
      pending = null;
      throw e;
    }));
}

export const loadTokens = once(() => import('@splendor/sqc-sdk/tokens'));
export const loadContracts = once(() => import('@splendor/sqc-sdk/contracts'));
export const loadBridge = once(() => import('@splendor/sqc-sdk/bridge'));
