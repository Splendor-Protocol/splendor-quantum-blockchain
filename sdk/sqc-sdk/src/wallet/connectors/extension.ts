import type { Connection } from '../../core/connection.js';
import type { Connector, ConnectorConnectOptions } from './types.js';
import { connectExtension, ExtensionSigner } from '../extension-signer.js';

/**
 * Factory connector — attaches to the Splendor Quantum Wallet extension.
 * Returns ExtensionSigner (not an in-page WASM Wallet).
 */
export const extensionConnector: Connector & {
  connect(
    connection: Connection,
    options?: ConnectorConnectOptions,
  ): Promise<ExtensionSigner>;
} = {
  id: 'extension',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const { wallet } = await connectExtension({
      connection,
      networkId: options.networkId,
    });
    return wallet;
  },
};

export type { ExtensionSigner };
