import type { DeriveInfo, EthereumProvider } from '@splendor/sqc-sdk';
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
 * MetaMask connector.
 *
 * NOT post-quantum. The ML-DSA key is derived from an ECDSA `personal_sign`
 * signature, so the account is only as strong as secp256k1 no matter which
 * algorithm signs the transactions. Offered for onboarding convenience; the
 * seed connector is the quantum-safe path.
 */
export function metamaskConnector(opts: MetamaskConnectorOpts = {}): Connector {
  const id = 'metamask';
  return {
    id,
    name: opts.name ?? 'MetaMask',
    icon: opts.icon,
    keyOrigin: 'evm-derived',
    postQuantumSecure: false,
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
