/**
 * Single source of truth for SQDP networks + protocol dynamics.
 * Edit: src/config/sqc.networks.json (do not scatter RPC/chain defaults elsewhere).
 */
import raw from './config/sqc.networks.json' with { type: 'json' };
import { BridgeError } from './errors.js';

export type NetworkId = keyof typeof raw.networks | (string & {});

export type NetworkConfig = {
  id: string;
  name: string;
  chainId: string;
  rpcUrls: string[];
  gatewayUrl: string;
  gatewayPort: number;
  wasmUrl: string;
  execUrl: string;
  timeoutMs: number;
  retries: number;
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
};

export type NetworksFile = {
  sqdpVersion: string;
  defaultNetwork: string;
  protocol: ProtocolConfig;
  networks: Record<string, NetworkConfig>;
};

const CONFIG = raw as NetworksFile;

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

export function listNetworks(): NetworkConfig[] {
  return Object.values(CONFIG.networks);
}

export function getNetwork(id: string): NetworkConfig | undefined {
  return CONFIG.networks[id];
}

/**
 * Resolve a network by id (local | testnet | mainnet | …).
 * Throws BridgeError if unknown.
 */
export function resolveNetwork(id?: string): NetworkConfig {
  const key = (id || CONFIG.defaultNetwork).trim();
  const net = CONFIG.networks[key];
  if (!net) {
    const known = Object.keys(CONFIG.networks).join(', ');
    throw BridgeError.rpc(`Unknown network "${key}". Known: ${known}`);
  }
  return net;
}

export function getDefaultNetwork(): NetworkConfig {
  return resolveNetwork(CONFIG.defaultNetwork);
}

/** Build SQDP derivation domain message for a chain id. */
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
