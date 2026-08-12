import type { DeriveInfo, EthereumProvider } from '@splendor/pq-bridge';
import type { Connector, ConnectorContext } from './types.js';
import { hasAuthorizedAccounts } from './metamask.js';

export type InjectedConnectorOpts = {
  /** Required EIP-1193 provider (Rabby, Coinbase inject, etc.). */
  ethereumProvider: EthereumProvider;
  id?: string;
  name?: string;
  icon?: string;
};

/**
 * Generic EIP-1193 inject connector (same PQ derivation path as MetaMask).
 */
export function injectedConnector(opts: InjectedConnectorOpts): Connector {
  return {
    id: opts.id ?? 'injected',
    name: opts.name ?? 'Browser Wallet',
    icon: opts.icon,
    ready() {
      return !!opts.ethereumProvider?.request;
    },
    authorized() {
      return hasAuthorizedAccounts(opts.ethereumProvider);
    },
    async connect(ctx: ConnectorContext): Promise<DeriveInfo> {
      return ctx.wallet.connectFromMetaMask(opts.ethereumProvider);
    },
  };
}
