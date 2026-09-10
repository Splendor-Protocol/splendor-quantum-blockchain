/**
 * createPublicClient / createWalletClient — viem-style factories.
 * Public client allocates Connection only (no Wallet / SqcProvider).
 */
import { Connection, type ConnectionOptions } from './core/connection.js';
import { SplendorPQBridge } from './splendor-wallet.js';
import {
  createSplendorClient,
  connectSplendor,
  type CreateClientOptions,
  type ConnectSplendorOptions,
  type ClientSession,
} from './connect.js';
import {
  resolveNetwork,
  registerNetwork,
  networkToRpcOptions,
  type NetworkConfig,
} from './networks.js';

export type PublicClientOptions = CreateClientOptions & ConnectionOptions;

function resolveNetworkForPublicClient(opts: CreateClientOptions): NetworkConfig {
  if (opts.networks?.length) {
    for (const n of opts.networks) registerNetwork(n);
  }
  if (opts.networkConfig) {
    registerNetwork(opts.networkConfig);
    return resolveNetwork(opts.networkConfig.id, {
      allowInsecureHttp: opts.allowInsecureHttp,
      rpcUrls: opts.rpcUrls ?? opts.networkConfig.rpcUrls,
      gatewayUrl: opts.gatewayUrl ?? opts.networkConfig.gatewayUrl,
    });
  }
  return resolveNetwork(opts.network as string | undefined, {
    allowInsecureHttp: opts.allowInsecureHttp,
    rpcUrls: opts.rpcUrls,
    gatewayUrl: opts.gatewayUrl,
  });
}

/**
 * Read-only chain client (Solana Connection / viem PublicClient analogue).
 * Builds Transport-backed Connection only — never allocates Wallet.
 *
 * ```ts
 * const publicClient = createPublicClient({ network: 'local' });
 * await publicClient.getBalance(address);
 * ```
 */
export function createPublicClient(opts: PublicClientOptions = {}): Connection {
  if (opts.transport || opts.existingRpc) {
    return new Connection({
      transport: opts.transport,
      existingRpc: opts.existingRpc,
      rpcUrl: opts.rpcUrl,
      rpcUrls: opts.rpcUrls,
      chainId: opts.chainId,
      timeoutMs: opts.timeoutMs,
      retries: opts.retries,
    });
  }

  const network = resolveNetworkForPublicClient(opts);
  const rpc = networkToRpcOptions(network);
  return new Connection({
    rpcUrls: opts.rpcUrls?.length ? opts.rpcUrls : rpc.rpcUrls,
    chainId: opts.chainId || rpc.chainId,
    timeoutMs: opts.timeoutMs ?? rpc.timeoutMs,
    retries: opts.retries ?? rpc.retries,
  });
}

/**
 * Signing wallet client (locked until `wallet.connect(...)` or connect opts unlock).
 *
 * ```ts
 * const wallet = createWalletClient({ network: 'local' });
 * await wallet.connect({ mode: 'seed', seed });
 * ```
 */
export function createWalletClient(opts: CreateClientOptions = {}): SplendorPQBridge {
  return createSplendorClient(opts).wallet;
}

/**
 * Create and unlock a wallet in one step (async).
 *
 * ```ts
 * const wallet = await createWalletClientAsync({ network: 'local', connect: 'seed', seed });
 * ```
 */
export async function createWalletClientAsync(
  opts: ConnectSplendorOptions = {},
): Promise<SplendorPQBridge> {
  const session = await connectSplendor(opts);
  return session.wallet;
}

export type { ClientSession, CreateClientOptions, ConnectSplendorOptions };
export { Connection, SplendorPQBridge };
export { Wallet } from './splendor-wallet.js';
