import { listNetworks, type NetworkConfig } from '@splendor/sqc-sdk/networks';

/**
 * Per-network overrides from env: VITE_SQC_<ID>_RPC_URL, VITE_SQC_<ID>_CHAIN_ID,
 * VITE_SQC_<ID>_EXPLORER_URL (<ID> = LOCAL, DEVNET, TESTNET, MAINNET). Shared by vite.config.ts
 * (proxy targets) and the app so they never disagree.
 */
export type NetworkEnv = Record<string, string | undefined>;

function envValue(env: NetworkEnv, id: string, key: string): string | undefined {
  const v = env[`VITE_SQC_${id.toUpperCase()}_${key}`]?.trim();
  return v ? v : undefined;
}

function withScheme(url: string): string {
  const u = url.replace(/\/+$/, '');
  return /^https?:\/\//i.test(u) ? u : `https://${u}`;
}

export function resolveNetworks(env: NetworkEnv): NetworkConfig[] {
  return listNetworks().map((n) => {
    const rpc = envValue(env, n.id, 'RPC_URL');
    const chainId = envValue(env, n.id, 'CHAIN_ID');
    const explorer = envValue(env, n.id, 'EXPLORER_URL');
    if (!rpc && !chainId && !explorer) return n;
    const explorerUrl = explorer ? withScheme(explorer) : n.explorerUrl;
    return {
      ...n,
      rpcUrls: rpc ? [withScheme(rpc)] : n.rpcUrls,
      chainId: chainId ?? n.chainId,
      explorerUrl,
      txUrlTemplate: explorer ? `${explorerUrl}/tx/{hash}` : n.txUrlTemplate,
    };
  });
}

/**
 * `customNetworks` for the live provider. On localhost every https network goes through the
 * dev proxy (/rpc/<id>); set VITE_SQC_DIRECT_RPC=1 to call RPCs directly.
 */
export function playgroundNetworks(): NetworkConfig[] | undefined {
  const env = import.meta.env as unknown as NetworkEnv;
  const resolved = resolveNetworks(env);
  const host = typeof window === 'undefined' ? '' : window.location.hostname;
  const local = host === 'localhost' || host === '127.0.0.1';
  if (import.meta.env.VITE_SQC_DIRECT_RPC === '1' || !local) {
    const changed = resolved.filter((n, i) => n !== listNetworks()[i]);
    return changed.length ? changed : undefined;
  }
  return resolved
    .filter((n) => n.rpcUrls.some((u) => u.startsWith('https://')))
    .map((n) => ({ ...n, rpcUrls: [`${window.location.origin}/rpc/${n.id}`] }));
}
