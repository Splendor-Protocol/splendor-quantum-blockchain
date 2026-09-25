import {
  connectExtension,
  discoverExtensionProvider,
  getExtensionProvider,
  type DeriveInfo,
  type SplendorProviderLike,
} from '@splendor/sqc-sdk';
import type { Connector, ConnectorContext } from './types.js';

export type ExtensionConnectorOpts = {
  name?: string;
  icon?: string;
  /** Max ms to wait for window.splendor on connect. Default 5000. */
  discoveryTimeoutMs?: number;
  /** Max ms for modal `ready()` when inject is missing. Default 400. */
  readyTimeoutMs?: number;
};

/**
 * Splendor Quantum Wallet browser extension — native ML-DSA accounts.
 * No connectFields (extension owns unlock UI). Resumable via autoReconnect.
 */
export function extensionConnector(opts: ExtensionConnectorOpts = {}): Connector {
  let sessionDisconnect: (() => void) | null = null;
  let cachedProvider: SplendorProviderLike | undefined;
  const discoveryTimeoutMs = opts.discoveryTimeoutMs ?? 5_000;
  const readyTimeoutMs = opts.readyTimeoutMs ?? 400;

  return {
    id: 'splendor-extension',
    name: opts.name ?? 'Splendor Quantum Wallet',
    icon: opts.icon,
    keyOrigin: 'native',
    postQuantumSecure: true,
    async ready() {
      const existing = getExtensionProvider();
      if (existing) {
        cachedProvider = existing;
        return true;
      }
      try {
        // Short burst for ConnectModal — do not wait a full 5s when wallet is missing.
        cachedProvider = await discoverExtensionProvider(readyTimeoutMs);
        return true;
      } catch {
        return !!getExtensionProvider();
      }
    },
    async authorized() {
      let p = cachedProvider ?? getExtensionProvider();
      if (!p) {
        try {
          p = await discoverExtensionProvider(Math.min(discoveryTimeoutMs, 2_000));
          cachedProvider = p;
        } catch {
          return false;
        }
      }
      try {
        const accounts = await p.request({ method: 'sqc_accounts', params: [] });
        return Array.isArray(accounts) && accounts.length > 0;
      } catch {
        try {
          const accounts = await p.request({ method: 'splendor_accounts', params: [] });
          return Array.isArray(accounts) && accounts.length > 0;
        } catch {
          return false;
        }
      }
    },
    async connect(ctx: ConnectorContext): Promise<DeriveInfo> {
      const provider =
        cachedProvider ??
        getExtensionProvider() ??
        (await discoverExtensionProvider(discoveryTimeoutMs));
      cachedProvider = provider;
      const { wallet, info } = await connectExtension({
        connection: ctx.connection,
        networkId: ctx.network.id,
        discoveryTimeoutMs,
        provider,
      });
      ctx.setSessionWallet?.(wallet);
      sessionDisconnect = () => wallet.disconnect();
      return info;
    },
    async disconnect() {
      sessionDisconnect?.();
      sessionDisconnect = null;
    },
    onAccountsChanged(cb) {
      const p = cachedProvider ?? getExtensionProvider();
      if (!p?.on) return () => {};
      const handler = (...args: unknown[]) => {
        const raw = args[0];
        const addr = Array.isArray(raw)
          ? String(raw[0] ?? '')
          : typeof raw === 'string'
            ? raw
            : '';
        if (addr) cb(addr);
      };
      p.on('accountsChanged', handler);
      return () => p.removeListener?.('accountsChanged', handler);
    },
  };
}
