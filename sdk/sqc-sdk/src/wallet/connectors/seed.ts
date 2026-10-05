import { SplendorPQBridge } from '../../splendor-wallet.js';
import type { Connection } from '../../core/connection.js';
import { BridgeError } from '../../errors.js';
import type { Connector, ConnectorConnectOptions } from './types.js';

/**
 * Headless factory connector for `connect()` / WalletFactoryConnector.
 * **Not** the ConnectKit UI connector — use `@splendor/connect-react` `seedConnector` for UI.
 */
export const seedWalletFactory: Connector = {
  id: 'seed',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const { seed, ...rest } = options;
    if (!seed) throw BridgeError.notConnected('seedWalletFactory requires seed');
    // Reuse the shared Connection — never clone via toRpcOptions() (detaches sticky RPC).
    const wallet = new SplendorPQBridge({
      ...rest,
      connection,
    });
    await wallet.init();
    await wallet.connectFromSeed(seed);
    return wallet;
  },
};

/** @deprecated Use {@link seedWalletFactory}. Name collides with connect-react UI connector. */
export const seedConnector = seedWalletFactory;
