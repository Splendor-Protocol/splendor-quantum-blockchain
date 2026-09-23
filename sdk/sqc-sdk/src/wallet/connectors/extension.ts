import type { Connection } from '../../core/connection.js';
import type { Connector, ConnectorConnectOptions } from './types.js';
import { connectExtension, ExtensionSigner } from '../extension-signer.js';

/**
 * Headless factory — attaches to the Splendor Quantum Wallet extension.
 * Returns ExtensionSigner (not an in-page WASM Wallet).
 * **Not** the ConnectKit UI connector — use `@splendor/connect-react` `extensionConnector` for UI.
 */
export const extensionWalletFactory: Connector & {
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

/** @deprecated Use {@link extensionWalletFactory}. Name collides with connect-react UI connector. */
export const extensionConnector = extensionWalletFactory;

export type { ExtensionSigner };
