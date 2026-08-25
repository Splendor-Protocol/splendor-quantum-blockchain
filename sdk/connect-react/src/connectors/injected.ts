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
 * Generic EIP-1193 inject connector (same derivation path as MetaMask, and so
 * carries the same caveat: the key is derived from an ECDSA signature and is
 * therefore not post-quantum).
 */
export function injectedConnector(opts: InjectedConnectorOpts): Connector {
  return {
    id: opts.id ?? 'injected',
    name: opts.name ?? 'Browser Wallet',
    icon: opts.icon,
    keyOrigin: 'evm-derived',
    postQuantumSecure: false,
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
