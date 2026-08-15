/**
 * Single source of truth for SQDP networks + protocol dynamics.
 * Edit: src/config/sqc.networks.json (mirrored to config/ via npm run sync:config).
 * Runtime: registerNetwork() / inline networkConfig for dApp-supplied endpoints.
 */
import raw from './config/sqc.networks.json' with { type: 'json' };
import { BridgeError } from './errors.js';

export type NetworkId = keyof typeof raw.networks | (string & {});

export type NetworkConfig = {
  id: string;
  name: string;
  /** Client-facing network identity (local/testnet/mainnet). Distinct per env. */
  networkId: string;
  /** Wire / protocol chain_id (must match node types.ChainID). */
  chainId: string;
  rpcUrls: string[];
  /** Optional HTTP gateway (operators / X402). Not required for SqRpc clients. */
  gatewayUrl: string;
  gatewayPort: number;
  wasmUrl: string;
  execUrl: string;
  /** Optional SHA-256 hex pin for sqc_signer.wasm (empty = skip verify). */
  wasmSha256: string;
  /** Optional SHA-256 hex pin for wasm_exec.js (empty = skip verify). */
  execSha256: string;
  timeoutMs: number;
  retries: number;
  /** Optional block explorer base URL (e.g. https://explorer.example). */
  explorerUrl?: string;
  /** Optional template with `{hash}` placeholder for tx deep links. */
  txUrlTemplate?: string;
};

export type ProtocolConfig = {
  minFee: string;
  addressPrefix: string;
  addressHexLen: number;
  confirmation: string;
  abiHash: string;
  goldenTransferSelector: string;
  derivationDomainVersion: string;
  bridgeClientVersion: string;
  /** Canonical wire chain id used in txs (node constant). */
  wireChainId?: string;
};

export type NetworksFile = {
  sqdpVersion: string;
  defaultNetwork: string;
  protocol: ProtocolConfig;
  networks: Record<string, NetworkConfig>;
};

export type ResolveNetworkOptions = {
  allowInsecureHttp?: boolean;
  /** Override RPC URLs — skips *.example rejection when non-empty and non-placeholder. */
  rpcUrls?: string[];
  gatewayUrl?: string;
  /** Allow resolving even when baked-in URLs are placeholders (UI listing only). */
  allowPlaceholder?: boolean;
  /**
   * When true, also reject placeholder / non-https gatewayUrl.
   * Default false — SDK SqRpc only needs rpcUrls.
   */
  requireGateway?: boolean;
};

function deepFreeze<T>(obj: T): T {
  if (obj && typeof obj === 'object' && !Object.isFrozen(obj)) {
    Object.freeze(obj);
    for (const v of Object.values(obj as object)) {
      deepFreeze(v);
    }
  }
  return obj;
}

const CONFIG: NetworksFile = deepFreeze(structuredClone(raw) as NetworksFile);
/** Runtime-registered networks (prefer over baked-in when same id). */
const RUNTIME: Record<string, NetworkConfig> = {};

function isPlaceholderUrl(url: string): boolean {
  if (!url) return false;
  return /\.example(\/|$)/i.test(url) || /splendor\.example/i.test(url);
}

function cloneNetwork(net: NetworkConfig): NetworkConfig {
  return {
    ...net,
    rpcUrls: [...net.rpcUrls],
  };
}

/** Full config document (protocol + all networks). */
export function getNetworksFile(): NetworksFile {
  return CONFIG;
}

export function getProtocolConfig(): ProtocolConfig {
  return CONFIG.protocol;
}

export function getDefaultNetworkId(): string {
  return CONFIG.defaultNetwork;
}

/** Register or replace a network at runtime (dApp RPCs / WASM URLs). */
export function registerNetwork(net: NetworkConfig): void {
  if (!net?.id) throw BridgeError.rpc('registerNetwork: id is required');
  if (!net.rpcUrls?.length) throw BridgeError.rpc('registerNetwork: rpcUrls required');
  if (!net.chainId) throw BridgeError.rpc('registerNetwork: chainId is required');
  if (!net.wasmUrl) throw BridgeError.rpc('registerNetwork: wasmUrl is required');
  if (!net.execUrl) throw BridgeError.rpc('registerNetwork: execUrl is required');
  RUNTIME[net.id] = cloneNetwork({
    ...net,
    gatewayUrl: net.gatewayUrl ?? '',
    gatewayPort: net.gatewayPort ?? 443,
    networkId: net.networkId || net.id,
    name: net.name || net.id,
    wasmSha256: net.wasmSha256 ?? '',
    execSha256: net.execSha256 ?? '',
    timeoutMs: net.timeoutMs ?? 20_000,
    retries: net.retries ?? 2,
  });
}

export function unregisterNetwork(id: string): void {
  delete RUNTIME[id];
}

export function clearRegisteredNetworks(): void {
  for (const k of Object.keys(RUNTIME)) delete RUNTIME[k];
}

export function listNetworks(): NetworkConfig[] {
  const map = new Map<string, NetworkConfig>();
  for (const n of Object.values(CONFIG.networks)) map.set(n.id, cloneNetwork(n));
  for (const n of Object.values(RUNTIME)) map.set(n.id, cloneNetwork(n));
  return [...map.values()];
}

/**
 * True when RPC URLs still use documentation placeholders (*.example).
 * gatewayUrl is ignored for SDK usability (optional for SqRpc).
 */
export function isPlaceholderNetwork(net: NetworkConfig | string): boolean {
  const config = typeof net === 'string' ? getNetwork(net) : net;
  if (!config) return true;
  return config.rpcUrls.some((url) => isPlaceholderUrl(url));
}

/**
 * Networks suitable for a UI picker.
 * By default excludes placeholder *.example endpoints unless allowPlaceholder is true.
 */
export function listSelectableNetworks(opts?: {
  allowPlaceholder?: boolean;
}): NetworkConfig[] {
  const all = listNetworks();
  if (opts?.allowPlaceholder) return all;
  return all.filter((n) => n.id === 'local' || !isPlaceholderNetwork(n));
}

export function getNetwork(id: string): NetworkConfig | undefined {
  const n = RUNTIME[id] ?? CONFIG.networks[id];
  return n ? cloneNetwork(n) : undefined;
}

/**
 * Resolve a network by id (local | testnet | mainnet | registered).
 * Pass rpcUrls overrides to use real endpoints over baked-in placeholders.
 * gatewayUrl is optional for SDK clients (SqRpc); set requireGateway to enforce it.
 */
export function resolveNetwork(
  id?: string,
  opts?: ResolveNetworkOptions,
): NetworkConfig {
  const key = (id || CONFIG.defaultNetwork).trim();
  const base = getNetwork(key);
  if (!base) {
    const known = listNetworks()
      .map((n) => n.id)
      .join(', ');
    throw BridgeError.rpc(`Unknown network "${key}". Known: ${known}`);
  }

  const merged: NetworkConfig = {
    ...base,
    rpcUrls: opts?.rpcUrls?.length ? [...opts.rpcUrls] : [...base.rpcUrls],
    gatewayUrl: opts?.gatewayUrl ?? base.gatewayUrl,
  };

  if (key !== 'local' && !opts?.allowPlaceholder) {
    for (const url of merged.rpcUrls) {
      if (isPlaceholderUrl(url)) {
        throw BridgeError.rpc(
          `Network "${key}" still uses placeholder RPC URL (${url}). ` +
            `Pass rpcUrls: [...] with real endpoints, or registerNetwork().`,
        );
      }
    }
    if (!opts?.allowInsecureHttp) {
      for (const url of merged.rpcUrls) {
        if (!url.startsWith('https://')) {
          throw BridgeError.rpc(
            `Network "${key}" RPC URL must use https:// (${url}). Pass allowInsecureHttp: true to override.`,
          );
        }
      }
    }
    if (opts?.requireGateway) {
      if (!merged.gatewayUrl || isPlaceholderUrl(merged.gatewayUrl)) {
        throw BridgeError.rpc(
          `Network "${key}" requires a real gatewayUrl (got ${merged.gatewayUrl || 'empty'}).`,
        );
      }
      if (!opts?.allowInsecureHttp && !merged.gatewayUrl.startsWith('https://')) {
        throw BridgeError.rpc(
          `Network "${key}" gatewayUrl must use https:// (${merged.gatewayUrl}).`,
        );
      }
    }
  }
  return merged;
}

export function getDefaultNetwork(): NetworkConfig {
  return resolveNetwork(CONFIG.defaultNetwork);
}

/** Build a tx explorer URL when the network defines txUrlTemplate or explorerUrl. */
export function txExplorerUrl(net: NetworkConfig, hash: string): string | null {
  if (!hash) return null;
  if (net.txUrlTemplate) return net.txUrlTemplate.replace(/\{hash\}/g, hash);
  if (net.explorerUrl) {
    const base = net.explorerUrl.replace(/\/$/, '');
    return `${base}/tx/${hash}`;
  }
  return null;
}

/**
 * Build SQDP derivation domain message for a wire chain id.
 * Must stay in sync with Go signer.DerivationDomainMessage (checked by sync:config).
 */
export function derivationDomainMessage(chainId: string): string {
  const v = CONFIG.protocol.derivationDomainVersion;
  return `Splendor Quantum Chain\nKey derivation ${v}\nchain: ${chainId}`;
}

/** Options suitable for SqRpc / SqcProvider / SplendorPQBridge. */
export function networkToRpcOptions(net: NetworkConfig): {
  rpcUrls: string[];
  chainId: string;
  timeoutMs: number;
  retries: number;
} {
  return {
    rpcUrls: [...net.rpcUrls],
    chainId: net.chainId,
    timeoutMs: net.timeoutMs,
    retries: net.retries,
  };
}
