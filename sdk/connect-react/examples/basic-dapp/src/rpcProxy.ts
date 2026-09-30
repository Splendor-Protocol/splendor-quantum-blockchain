import type { NetworkConfig } from '@splendor/sqc-sdk/networks';
import { overriddenNetworkIds, resolveNetworks } from './networks';

/**
 * Networks handed to the provider as `customNetworks`: built-ins plus env overrides
 * (see networks.ts). On localhost every https network is pointed at the Vite `/rpc/<id>`
 * proxy (see vite.config.ts) because the public RPCs do not allow cross-origin browser calls.
 * Set VITE_SQC_DIRECT_RPC=1 to talk to the RPCs directly (e.g. when served from an allowed origin).
 */
export function labNetworks(): NetworkConfig[] | undefined {
  const env = import.meta.env as Record<string, string | undefined>;
  const resolved = resolveNetworks(env);
  const overridden = overriddenNetworkIds(env);
  const direct = import.meta.env.VITE_SQC_DIRECT_RPC === '1';
  const host = typeof window === 'undefined' ? '' : window.location.hostname;
  const local = host === 'localhost' || host === '127.0.0.1';

  if (direct || !local) {
    const changed = resolved.filter((n) => overridden.includes(n.id));
    return changed.length ? changed : undefined;
  }
  return resolved
    .filter((n) => n.rpcUrls.some((u) => u.startsWith('https://')))
    .map((n) => ({ ...n, rpcUrls: [`${window.location.origin}/rpc/${n.id}`] }));
}
