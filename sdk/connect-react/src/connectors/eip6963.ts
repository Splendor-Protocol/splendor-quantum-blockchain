import type { EthereumProvider } from '@splendor/pq-bridge';
import { injectedConnector } from './injected.js';
import { metamaskConnector } from './metamask.js';
import { seedConnector } from './seed.js';
import type { Connector } from './types.js';

type Eip6963ProviderDetail = {
  info: { uuid: string; name: string; icon: string; rdns: string };
  provider: EthereumProvider;
};

/**
 * Discover EIP-6963 announced injected wallets and map them to Splendor connectors.
 * Derivation-only (personal_sign) — not an SQC EIP-1193 chain provider.
 */
export function discoverInjectedConnectors(opts?: {
  timeoutMs?: number;
  /** Skip rdns that duplicate metamaskConnector (default true). */
  skipMetaMask?: boolean;
}): Promise<Connector[]> {
  const timeoutMs = opts?.timeoutMs ?? 50;
  const skipMetaMask = opts?.skipMetaMask !== false;

  return new Promise((resolve) => {
    if (typeof window === 'undefined') {
      resolve([]);
      return;
    }

    const found = new Map<string, Eip6963ProviderDetail>();
    const onAnnounce = (event: Event) => {
      const detail = (event as CustomEvent<Eip6963ProviderDetail>).detail;
      if (!detail?.info?.uuid || !detail.provider?.request) return;
      found.set(detail.info.uuid, detail);
    };

    window.addEventListener('eip6963:announceProvider', onAnnounce);
    window.dispatchEvent(new Event('eip6963:requestProvider'));

    setTimeout(() => {
      window.removeEventListener('eip6963:announceProvider', onAnnounce);
      const connectors: Connector[] = [];
      for (const detail of found.values()) {
        const rdns = detail.info.rdns || '';
        if (skipMetaMask && /metamask/i.test(rdns)) continue;
        connectors.push(
          injectedConnector({
            id: `eip6963:${detail.info.uuid}`,
            name: detail.info.name,
            icon: detail.info.icon,
            ethereumProvider: detail.provider,
          }),
        );
      }
      resolve(connectors);
    }, timeoutMs);
  });
}

/** MetaMask + EIP-6963 discovers + optional seed. */
export async function defaultBrowserConnectors(opts?: {
  includeSeed?: boolean;
}): Promise<Connector[]> {
  const list: Connector[] = [metamaskConnector()];
  const discovered = await discoverInjectedConnectors();
  list.push(...discovered);
  if (opts?.includeSeed !== false) list.push(seedConnector());
  return list;
}
