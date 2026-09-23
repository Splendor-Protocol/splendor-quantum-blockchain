import {
  connectExtension,
  getExtensionProvider,
  type DeriveInfo,
} from '@splendor/sqc-sdk';
import type { Connector, ConnectorContext } from './types.js';

export type ExtensionConnectorOpts = {
  name?: string;
  icon?: string;
};

/**
 * Splendor Quantum Wallet browser extension — native ML-DSA accounts.
 * No connectFields (extension owns unlock UI). Resumable via autoReconnect.
 */
export function extensionConnector(opts: ExtensionConnectorOpts = {}): Connector {
  let sessionDisconnect: (() => void) | null = null;

  return {
    id: 'splendor-extension',
    name: opts.name ?? 'Splendor Quantum Wallet',
    icon: opts.icon,
    keyOrigin: 'native',
    postQuantumSecure: true,
    ready() {
      return !!getExtensionProvider();
    },
    async authorized() {
      const p = getExtensionProvider();
      if (!p) return false;
      try {
        const accounts = await p.request({ method: 'sqc_accounts' });
        return Array.isArray(accounts) && accounts.length > 0;
      } catch {
        try {
          const accounts = await p.request({ method: 'splendor_accounts' });
          return Array.isArray(accounts) && accounts.length > 0;
        } catch {
          return false;
        }
      }
    },
    async connect(ctx: ConnectorContext): Promise<DeriveInfo> {
      const { wallet, info } = await connectExtension({
        connection: ctx.connection,
        networkId: ctx.network.id,
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
      const p = getExtensionProvider();
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
