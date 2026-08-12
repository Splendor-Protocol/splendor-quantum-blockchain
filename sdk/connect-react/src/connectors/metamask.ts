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
 * True when the EIP-1193 provider already has at least one authorized account
 * (no popup). Used by autoReconnect to avoid eth_requestAccounts prompts.
 */
export async function hasAuthorizedAccounts(
  provider?: EthereumProvider,
): Promise<boolean> {
  const eth = provider ?? getWindowEthereum();
  if (!eth?.request) return false;
  try {
    const accounts = (await eth.request({ method: 'eth_accounts' })) as unknown;
    return Array.isArray(accounts) && accounts.length > 0;
  } catch {
    return false;
  }
}

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
    authorized() {
      return hasAuthorizedAccounts(opts.ethereumProvider);
    },
    async connect(ctx: ConnectorContext): Promise<DeriveInfo> {
      const provider = opts.ethereumProvider ?? getWindowEthereum();
      return ctx.wallet.connectFromMetaMask(provider);
    },
  };
}
