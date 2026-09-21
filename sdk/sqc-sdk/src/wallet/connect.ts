/**
 * Unified connect() entry — beginner-friendly one-touch API.
 * Unlock path uses WalletFactoryConnectors attached to a shared Connection.
 */
import { Connection } from '../core/connection.js';
import { SplendorPQBridge } from '../splendor-wallet.js';
import { SqcProvider } from '../provider.js';
import {
  createSplendorClient,
  connectSplendor,
  buildBridgeOptions,
  type ConnectMode,
  type CreateClientOptions,
  type ConnectSplendorOptions,
  type ClientSession,
} from '../connect.js';
import { seedConnector, extensionConnector } from './connectors/index.js';
import type { ConnectorConnectOptions } from './connectors/types.js';
import type { DeriveInfo } from '../types.js';
import type { NetworkConfig } from '../networks.js';
import { BridgeError } from '../errors.js';
import type { ExtensionSigner } from './extension-signer.js';

export type ConnectOptions = ConnectSplendorOptions & {
  /** Alias for connectSplendor `connect` field. */
  mode?: ConnectMode;
};

export type ConnectResult = {
  client: ClientSession;
  connection: Connection;
  wallet: SplendorPQBridge | ExtensionSigner;
  network: NetworkConfig;
  info: DeriveInfo | null;
};

/**
 * One-touch connect — creates a shared Connection and unlocks via factory connectors.
 *
 * ```ts
 * const { wallet } = await connect({ network: 'local', mode: 'seed', seed });
 * ```
 */
export async function connect(opts: ConnectOptions = {}): Promise<ConnectResult> {
  const mode = opts.mode ?? opts.connect ?? 'none';
  const { network, bridgeOpts } = buildBridgeOptions(opts);
  const connection = new Connection(bridgeOpts);
  const provider = new SqcProvider(bridgeOpts, connection.rpc);

  if (mode === 'none') {
    const wallet = new SplendorPQBridge({ ...bridgeOpts, connection, provider });
    const client: ClientSession = { network, provider, wallet, info: null };
    return { client, connection, wallet, network, info: null };
  }

  const connectorOpts: ConnectorConnectOptions = {
    ...bridgeOpts,
    seed: opts.seed,
    networkId: network.id,
  };

  if (mode === 'extension') {
    const wallet = await extensionConnector.connect(connection, connectorOpts);
    const info = wallet.getDeriveInfo();
    const client: ClientSession = { network, provider, wallet, info };
    return { client, connection: wallet.connection, wallet, network, info };
  }

  if (mode === 'seed') {
    if (!opts.seed) throw BridgeError.notConnected('connect: seed requires opts.seed');
    const wallet = await seedConnector.connect(connection, connectorOpts);
    const info = wallet.getDeriveInfo();
    const client: ClientSession = { network, provider, wallet, info };
    return { client, connection: wallet.connection, wallet, network, info };
  }

  throw BridgeError.rpc(`Unknown connect mode: ${mode}`);
}

/** @deprecated Use ClientSession — type name collided with SplendorClient class. */
export type LegacyClientSession = ClientSession;
/** @deprecated Use ClientSession */
export type SplendorClient = ClientSession;

export { createSplendorClient, connectSplendor, buildBridgeOptions };
export type { ConnectMode, CreateClientOptions, ConnectSplendorOptions, ClientSession };
export { seedConnector, extensionConnector };
export type { ConnectorConnectOptions };
export { installBrowserSessionPolicy } from './browser-session-policy.js';
export { WasmSession } from './wasm-session.js';
