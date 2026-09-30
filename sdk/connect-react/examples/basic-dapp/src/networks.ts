import { listNetworks, type NetworkConfig } from '@splendor/sqc-sdk/networks';

/**
 * Per-network overrides, read from env (`.env.local`, shell, or `npm run lab -- --rpc=…`):
 *
 *   VITE_SQC_<ID>_RPC_URL       e.g. VITE_SQC_TESTNET_RPC_URL=https://quantum-rpc-testnet.splendor.org
 *   VITE_SQC_<ID>_CHAIN_ID      e.g. VITE_SQC_TESTNET_CHAIN_ID=splendor-quantum-testnet-1
 *   VITE_SQC_<ID>_EXPLORER_URL  e.g. VITE_SQC_TESTNET_EXPLORER_URL=https://quantum-testnet-explorer.splendor.org
 *
 * <ID> is the upper-cased network id (LOCAL, DEVNET, TESTNET, MAINNET). Unset values keep the
 * SDK built-ins. Used by both vite.config.ts (proxy targets) and the app, so they never disagree.
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

/** Ids whose env overrides differ from the SDK built-ins (for display). */
export function overriddenNetworkIds(env: NetworkEnv): string[] {
  return listNetworks()
    .filter((n) => ['RPC_URL', 'CHAIN_ID', 'EXPLORER_URL'].some((k) => envValue(env, n.id, k)))
    .map((n) => n.id);
}
