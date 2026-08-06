/**
 * One-touch Splendor client for dApps — single standard entrypoint.
 *
 *   const { provider, wallet, network } = await connectSplendor({
 *     network: 'local',
 *     connect: 'metamask',
 *   });
 */
import { SqcProvider } from './provider.js';
import { SplendorPQBridge } from './wallet.js';
import type { BridgeOptions, DeriveInfo } from './types.js';
import {
  resolveNetwork,
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
  /** Override RPC URLs (still uses network chainId / timeouts unless overridden). */
  rpcUrls?: string[];
  chainId?: string;
  timeoutMs?: number;
  retries?: number;
  wasmUrl?: string;
  execUrl?: string;
  wasmSha256?: string;
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

export type SplendorClient = {
  network: NetworkConfig;
  provider: SqcProvider;
  wallet: SplendorPQBridge;
  info: DeriveInfo | null;
};

function buildBridgeOptions(opts: CreateClientOptions = {}): {
  network: NetworkConfig;
  bridgeOpts: BridgeOptions;
} {
  const network = resolveNetwork(opts.network as string | undefined);
  const rpc = networkToRpcOptions(network);
  const bridgeOpts: BridgeOptions = {
    rpcUrls: opts.rpcUrls?.length ? opts.rpcUrls : rpc.rpcUrls,
    chainId: opts.chainId || rpc.chainId,
    timeoutMs: opts.timeoutMs ?? rpc.timeoutMs,
    retries: opts.retries ?? rpc.retries,
    wasmUrl: opts.wasmUrl ?? network.wasmUrl,
    execUrl: opts.execUrl ?? network.execUrl,
    wasmSha256: opts.wasmSha256 ?? (network.wasmSha256 || undefined),
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
export function createSplendorClient(opts: CreateClientOptions = {}): SplendorClient {
  const { network, bridgeOpts } = buildBridgeOptions(opts);
  const provider = new SqcProvider(bridgeOpts);
  const wallet = SplendorPQBridge.fromProvider(provider, bridgeOpts);
  return { network, provider, wallet, info: null };
}

/**
 * One-touch: create client and optionally connect a signing session.
 */
export async function connectSplendor(opts: ConnectSplendorOptions = {}): Promise<SplendorClient> {
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
