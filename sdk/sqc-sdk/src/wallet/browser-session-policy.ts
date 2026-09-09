/**
 * BrowserSessionPolicy — idle lock + visibility (host-owned; ConnectKit / connect() installs).
 * Keeps `document` listeners out of Wallet.
 */
import type { SplendorPQBridge } from '../splendor-wallet.js';

export type BrowserSessionPolicyOpts = {
  sessionIdleMs?: number;
  lockOnHidden?: boolean;
};

type WalletEvents = {
  on(event: 'connect' | 'accountsChanged' | 'lock' | 'disconnect', fn: () => void): () => void;
  lock(): void;
};

/**
 * Attach idle / visibility lock to a wallet. Returns disposer.
 * No-op when `document` is undefined or neither idle nor lockOnHidden is configured.
 */
export function installBrowserSessionPolicy(
  wallet: SplendorPQBridge | WalletEvents,
  opts: BrowserSessionPolicyOpts,
): () => void {
  const ms = opts.sessionIdleMs;
  const lockOnHidden = opts.lockOnHidden === true;
  if ((!ms || ms <= 0) && !lockOnHidden) return () => {};
  if (typeof document === 'undefined') return () => {};

  let idleTimer: ReturnType<typeof setTimeout> | null = null;
  const idleUnsubs: Array<() => void> = [];

  const clearIdle = () => {
    if (idleTimer != null) {
      clearTimeout(idleTimer);
      idleTimer = null;
    }
    for (const u of idleUnsubs) u();
    idleUnsubs.length = 0;
  };

  const setupIdle = () => {
    clearIdle();
    if (ms && ms > 0) {
      const reset = () => {
        if (idleTimer != null) clearTimeout(idleTimer);
        idleTimer = setTimeout(() => wallet.lock(), ms);
      };
      const onActivity = () => reset();
      document.addEventListener('pointerdown', onActivity);
      idleUnsubs.push(() => document.removeEventListener('pointerdown', onActivity));
      document.addEventListener('keydown', onActivity);
      idleUnsubs.push(() => document.removeEventListener('keydown', onActivity));
      reset();
    }
    if (lockOnHidden) {
      const onVisibility = () => {
        if (document.visibilityState === 'hidden') wallet.lock();
      };
      const onPageHide = () => wallet.lock();
      document.addEventListener('visibilitychange', onVisibility);
      idleUnsubs.push(() => document.removeEventListener('visibilitychange', onVisibility));
      globalThis.addEventListener?.('pagehide', onPageHide);
      idleUnsubs.push(() => globalThis.removeEventListener?.('pagehide', onPageHide));
    }
  };

  const onConnect = () => setupIdle();
  const onClear = () => clearIdle();

  const offConnect = wallet.on('connect', onConnect);
  const offAccounts = wallet.on('accountsChanged', onConnect);
  const offLock = wallet.on('lock', onClear);
  const offDisconnect = wallet.on('disconnect', onClear);

  return () => {
    clearIdle();
    offConnect();
    offAccounts();
    offLock();
    offDisconnect();
  };
}
