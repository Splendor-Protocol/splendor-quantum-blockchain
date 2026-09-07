/**
 * viem-style client factories — preferred happy-path entrypoints.
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

export type PublicClientOptions = CreateClientOptions & ConnectionOptions;

/**
 * Read-only chain client (Solana Connection / viem PublicClient analogue).
 *
 * ```ts
 * const publicClient = createPublicClient({ network: 'local' });
 * await publicClient.getBalance(address);
 * ```
 */
export function createPublicClient(opts: PublicClientOptions = {}): Connection {
  const session = createSplendorClient(opts);
  return session.wallet.connection;
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
