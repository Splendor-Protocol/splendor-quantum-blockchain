/**
 * Unified connect() entry — beginner-friendly one-touch API.
 */
import { Connection } from '../core/connection.js';
import { SplendorPQBridge } from '../splendor-wallet.js';
import {
  createSplendorClient,
  connectSplendor,
  type ConnectMode,
  type CreateClientOptions,
  type ConnectSplendorOptions,
  type ClientSession,
  type SplendorClient,
} from '../connect.js';
import { seedConnector, metamaskConnector, evmKeyConnector } from './connectors/index.js';
import type { ConnectorConnectOptions } from './connectors/types.js';
import type { DeriveInfo } from '../types.js';
import type { NetworkConfig } from '../networks.js';

export type ConnectOptions = ConnectSplendorOptions & {
  /** Alias for connectSplendor `connect` field. */
  mode?: ConnectMode;
};

export type ConnectResult = {
  client: ClientSession;
  connection: Connection;
  wallet: SplendorPQBridge;
  network: NetworkConfig;
  info: DeriveInfo | null;
};

/**
 * One-touch connect — creates client and optionally unlocks wallet.
 *
 * ```ts
 * const { wallet } = await connect({ network: 'local', mode: 'seed', seed });
 * ```
 */
export async function connect(opts: ConnectOptions = {}): Promise<ConnectResult> {
  const mode = opts.mode ?? opts.connect ?? 'none';
  const client = await connectSplendor({ ...opts, connect: mode });
  return {
    client,
    connection: client.wallet.connection,
    wallet: client.wallet,
    network: client.network,
    info: client.info,
  };
}

export { createSplendorClient, connectSplendor };
export type { ConnectMode, CreateClientOptions, ConnectSplendorOptions, ClientSession, SplendorClient };
export { seedConnector, metamaskConnector, evmKeyConnector };
export type { ConnectorConnectOptions };
