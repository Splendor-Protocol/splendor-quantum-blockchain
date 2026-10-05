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
  /** Compiler-canonical `transfer(address,uint256)` selector. */
  canonicalTransferSelector: string;
  /** Legacy hash-as-written `transfer(sqaddress,uint256)` selector. */
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
  /**
   * Allow a non-local network that carries the mainnet chain id to point at loopback or
   * private-network RPCs. Off by default: signatures made against such a node are valid
   * on mainnet.
   */
  allowLocalMainnetChainId?: boolean;
};

/** True for loopback, RFC 1918, link-local and unique-local hosts. */
export function isPrivateOrLoopbackUrl(url: string): boolean {
  if (isLoopbackOrLocalhostUrl(url)) return true;
  try {
    const host = new URL(url).hostname.toLowerCase().replace(/^\[|\]$/g, '');
    if (/^10\./.test(host) || /^192\.168\./.test(host) || /^169\.254\./.test(host)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(host)) return true;
    if (/^127\./.test(host) || host === '0.0.0.0') return true;
    if (/^f[cd][0-9a-f]{2}:/.test(host) || /^fe80:/.test(host)) return true;
    return false;
  } catch {
    return false;
  }
}

/**
 * A loopback/private URL with no path is a node's own RPC base. URLs with a path
 * (e.g. `http://localhost:5173/rpc/mainnet`) are treated as dev proxies to a public RPC.
 */
function isDirectPrivateNodeUrl(url: string): boolean {
  if (!isPrivateOrLoopbackUrl(url)) return false;
  try {
    const path = new URL(url).pathname;
    return path === '' || path === '/';
  } catch {
    return false;
  }
}

let warnedLocalChainId = false;

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

/** True for loopback / localhost (HTTP allowed without production HTTPS gates). */
export function isLoopbackOrLocalhostUrl(url: string): boolean {
  try {
    const u = new URL(url);
    const host = u.hostname.toLowerCase();
    return (
      host === 'localhost' ||
      host === '127.0.0.1' ||
      host === '::1' ||
      host === '[::1]' ||
      host.endsWith('.localhost')
    );
  } catch {
    return false;
  }
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
  for (const [field, url] of [
    ['explorerUrl', net.explorerUrl],
    ['txUrlTemplate', net.txUrlTemplate],
  ] as const) {
    if (url && !isSafeExplorerUrl(url)) {
      throw BridgeError.invalidParams(`registerNetwork: ${field} must use https:// (or http:// on loopback)`);
    }
  }
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
  }

  // Production HTTPS policy follows endpoint host, not the network id label.
  // `local` + remote overrides must still use https unless allowInsecureHttp.
  if (!opts?.allowInsecureHttp) {
    for (const url of merged.rpcUrls) {
      if (isLoopbackOrLocalhostUrl(url)) continue;
      if (isPlaceholderUrl(url) && (key === 'local' || opts?.allowPlaceholder)) continue;
      if (!url.startsWith('https://')) {
        throw BridgeError.rpc(
          `RPC URL must use https:// for non-loopback hosts (${url}). Pass allowInsecureHttp: true to override.`,
        );
      }
    }
  }

  const mainnetChainId = CONFIG.networks.mainnet?.chainId;
  if (mainnetChainId && merged.chainId === mainnetChainId && merged.rpcUrls.some(isDirectPrivateNodeUrl)) {
    if (key === 'local') {
      if (!warnedLocalChainId && typeof console !== 'undefined') {
        warnedLocalChainId = true;
        console.warn(
          `[sqc-sdk] network "local" uses the mainnet chain id (${mainnetChainId}); signatures made ` +
            'against it are valid on mainnet. Never use a mainnet seed with a local node.',
        );
      }
    } else if (!opts?.allowLocalMainnetChainId) {
      throw BridgeError.chainMismatch(
        `${mainnetChainId} on a loopback/private RPC (network "${key}")`,
        'a non-mainnet chain id for local nodes (or pass allowLocalMainnetChainId: true)',
      );
    }
  }

  if (opts?.requireGateway && key !== 'local' && !opts?.allowPlaceholder) {
    if (!merged.gatewayUrl || isPlaceholderUrl(merged.gatewayUrl)) {
      throw BridgeError.rpc(
        `Network "${key}" requires a real gatewayUrl (got ${merged.gatewayUrl || 'empty'}).`,
      );
    }
    if (
      !opts?.allowInsecureHttp &&
      !isLoopbackOrLocalhostUrl(merged.gatewayUrl) &&
      !merged.gatewayUrl.startsWith('https://')
    ) {
      throw BridgeError.rpc(
        `Network "${key}" gatewayUrl must use https:// (${merged.gatewayUrl}).`,
      );
    }
  }
  return merged;
}

export function getDefaultNetwork(): NetworkConfig {
  return resolveNetwork(CONFIG.defaultNetwork);
}

/** True for `https:` URLs, or `http:` on loopback. Anything else (javascript:, data:, …) is refused. */
export function isSafeExplorerUrl(url: string | undefined): boolean {
  if (!url) return false;
  try {
    const u = new URL(url.replace(/\{hash\}/g, '0'));
    if (u.protocol === 'https:') return true;
    return u.protocol === 'http:' && isLoopbackOrLocalhostUrl(url);
  } catch {
    return false;
  }
}

const TX_HASH_RE = /^(0x)?[0-9a-fA-F]{64}$/;

/** Build a tx explorer URL when the network defines txUrlTemplate or explorerUrl. */
export function txExplorerUrl(net: NetworkConfig, hash: string): string | null {
  if (!hash || !TX_HASH_RE.test(hash)) return null;
  const safeHash = encodeURIComponent(hash);
  if (net.txUrlTemplate && isSafeExplorerUrl(net.txUrlTemplate)) {
    return net.txUrlTemplate.replace(/\{hash\}/g, safeHash);
  }
  if (net.explorerUrl && isSafeExplorerUrl(net.explorerUrl)) {
    const base = net.explorerUrl.replace(/\/$/, '');
    return `${base}/tx/${safeHash}`;
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
