/**
 * One-touch Splendor client for dApps — single standard entrypoint.
 *
 *   const { provider, wallet, network } = await connectSplendor({
 *     network: 'local',
 *     connect: 'metamask',
 *   });
 */
import { SqcProvider } from './provider.js';
import { SplendorPQBridge } from './splendor-wallet.js';
import type { BridgeOptions, DeriveInfo } from './types.js';
import {
  resolveNetwork,
  registerNetwork,
  networkToRpcOptions,
  type NetworkConfig,
  type NetworkId,
} from './networks.js';
import { BridgeError } from './errors.js';
import type { EthereumProvider } from './metamask.js';
import type { DerivationScheme } from './derive.js';

export type ConnectMode = 'none' | 'metamask' | 'seed' | 'evmKey' | 'evmSignature';

export type CreateClientOptions = {
  /** Network id from sqc.networks.json (default: file defaultNetwork). */
  network?: NetworkId;
  /**
   * Inline full network config (preferred for production dApps).
   * When set, used instead of looking up `network` id (id still taken from config).
   */
  networkConfig?: NetworkConfig;
  /** Register these networks before resolve (sync; safe for React render). */
  networks?: NetworkConfig[];
  /** Override RPC URLs (still uses network chainId / timeouts unless overridden). */
  rpcUrls?: string[];
  /** Override gateway URL (optional — not used by SqRpc). */
  gatewayUrl?: string;
  chainId?: string;
  timeoutMs?: number;
  retries?: number;
  wasmUrl?: string;
  execUrl?: string;
  wasmSha256?: string;
  execSha256?: string;
  /**
   * Allow loading WASM without wasmSha256 on non-local browser networks.
   * Default false — fail closed unless pin is provided.
   */
  allowUnpinnedWasm?: boolean;
  /** Allow http:// RPC/gateway URLs for non-local networks. */
  allowInsecureHttp?: boolean;
  /** Auto-lock after this many ms of inactivity (browser). */
  sessionIdleMs?: number;
  /** Lock immediately when tab is hidden (default false). */
  lockOnHidden?: boolean;
  watchMetaMaskAccounts?: boolean;
  ethereumProvider?: EthereumProvider;
  derivation?: DerivationScheme;
  onRequest?: BridgeOptions['onRequest'];
  onError?: BridgeOptions['onError'];
};

export type ConnectSplendorOptions = CreateClientOptions & {
  /** How to unlock the signing wallet. Default: none (provider-only ready). */
  connect?: ConnectMode;
  seed?: string;
  evmPrivateKey?: string;
  evmSignature?: string;
  evmAddress?: string;
};

export type ClientSession = {
  network: NetworkConfig;
  provider: SqcProvider;
  wallet: SplendorPQBridge;
  info: DeriveInfo | null;
};

/** @deprecated Use ClientSession — kept for backward compatibility. */
export type SplendorClient = ClientSession;

function buildBridgeOptions(opts: CreateClientOptions = {}): {
  network: NetworkConfig;
  bridgeOpts: BridgeOptions;
} {
  if (opts.networks?.length) {
    for (const n of opts.networks) registerNetwork(n);
  }

  let network: NetworkConfig;
  if (opts.networkConfig) {
    registerNetwork(opts.networkConfig);
    network = resolveNetwork(opts.networkConfig.id, {
      allowInsecureHttp: opts.allowInsecureHttp,
      rpcUrls: opts.rpcUrls ?? opts.networkConfig.rpcUrls,
      gatewayUrl: opts.gatewayUrl ?? opts.networkConfig.gatewayUrl,
    });
  } else {
    network = resolveNetwork(opts.network as string | undefined, {
      allowInsecureHttp: opts.allowInsecureHttp,
      rpcUrls: opts.rpcUrls,
      gatewayUrl: opts.gatewayUrl,
    });
  }

  const rpc = networkToRpcOptions(network);
  const wasmSha256 = opts.wasmSha256 ?? (network.wasmSha256 || undefined);
  const execSha256 = opts.execSha256 ?? (network.execSha256 || undefined);
  const allowUnpinnedWasm = opts.allowUnpinnedWasm === true;
  const isBrowser =
    typeof globalThis !== 'undefined' &&
    typeof (globalThis as { document?: unknown }).document !== 'undefined';
  const isLocal = network.id === 'local';
  // Non-local browser: require integrity pin unless explicitly opted out.
  if (isBrowser && !isLocal && !wasmSha256 && !allowUnpinnedWasm) {
    throw BridgeError.wasm(
      `wasmSha256 is required for network "${network.id}" in the browser ` +
        '(pass wasmSha256 or allowUnpinnedWasm: true)',
    );
  }
  const bridgeOpts: BridgeOptions = {
    rpcUrls: rpc.rpcUrls,
    chainId: opts.chainId || rpc.chainId,
    timeoutMs: opts.timeoutMs ?? rpc.timeoutMs,
    retries: opts.retries ?? rpc.retries,
    wasmUrl: opts.wasmUrl ?? network.wasmUrl,
    execUrl: opts.execUrl ?? network.execUrl,
    wasmSha256,
    execSha256,
    allowUnpinnedWasm,
    networkId: network.id,
    allowInsecureHttp: opts.allowInsecureHttp,
    sessionIdleMs: opts.sessionIdleMs,
    lockOnHidden: opts.lockOnHidden,
    watchMetaMaskAccounts: opts.watchMetaMaskAccounts,
    ethereumProvider: opts.ethereumProvider,
    derivation: opts.derivation,
    onRequest: opts.onRequest,
    onError: opts.onError,
  };
  return { network, bridgeOpts };
}

/**
 * Create provider + wallet wired to the standard network config (no connect yet).
 */
export function createSplendorClient(opts: CreateClientOptions = {}): ClientSession {
  const { network, bridgeOpts } = buildBridgeOptions(opts);
  const provider = new SqcProvider(bridgeOpts);
  const wallet = SplendorPQBridge.fromProvider(provider, bridgeOpts);
  return { network, provider, wallet, info: null };
}

/**
 * One-touch: create client and optionally connect a signing session.
 */
export async function connectSplendor(opts: ConnectSplendorOptions = {}): Promise<ClientSession> {
  const client = createSplendorClient(opts);
  const mode = opts.connect ?? 'none';

  if (mode === 'none') {
    return client;
  }

  await client.wallet.init();
  let info: DeriveInfo;

  switch (mode) {
    case 'metamask':
      info = await client.wallet.connectFromMetaMask(opts.ethereumProvider);
      break;
    case 'seed':
      if (!opts.seed) throw BridgeError.notConnected('connect: seed requires opts.seed');
      info = await client.wallet.connectFromSeed(opts.seed);
      break;
    case 'evmKey':
      if (!opts.evmPrivateKey) {
        throw BridgeError.notConnected('connect: evmKey requires opts.evmPrivateKey');
      }
      info = await client.wallet.connectFromEvmPrivateKey(opts.evmPrivateKey);
      break;
    case 'evmSignature':
      if (!opts.evmSignature) {
        throw BridgeError.notConnected('connect: evmSignature requires opts.evmSignature');
      }
      info = await client.wallet.connectFromEvmSignature(
        opts.evmSignature,
        opts.evmAddress,
        opts.derivation || 'eip191-v1',
      );
      break;
    default:
      throw BridgeError.rpc(`Unknown connect mode: ${mode}`);
  }

  return { ...client, info };
}
