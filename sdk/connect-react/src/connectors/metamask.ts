import type { DeriveInfo, EthereumProvider } from '@splendor/pq-bridge';
import type { Connector, ConnectorContext } from './types.js';

function getWindowEthereum(): EthereumProvider | undefined {
  if (typeof globalThis === 'undefined') return undefined;
  return (globalThis as { ethereum?: EthereumProvider }).ethereum;
}

export type MetamaskConnectorOpts = {
  /** Override injected provider (defaults to window.ethereum). */
  ethereumProvider?: EthereumProvider;
  name?: string;
  icon?: string;
};

/**
 * Primary connector: MetaMask (or compatible inject) derives PQ keys via personal_sign.
 */
export function metamaskConnector(opts: MetamaskConnectorOpts = {}): Connector {
  const id = 'metamask';
  return {
    id,
    name: opts.name ?? 'MetaMask',
    icon: opts.icon,
    ready() {
      const eth = opts.ethereumProvider ?? getWindowEthereum();
      return !!eth?.request;
    },
    async connect(ctx: ConnectorContext): Promise<DeriveInfo> {
      const provider = opts.ethereumProvider ?? getWindowEthereum();
      return ctx.wallet.connectFromMetaMask(provider);
    },
  };
}
