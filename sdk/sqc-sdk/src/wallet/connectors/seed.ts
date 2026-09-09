import { SplendorPQBridge } from '../../splendor-wallet.js';
import type { Connection } from '../../core/connection.js';
import { BridgeError } from '../../errors.js';
import type { Connector, ConnectorConnectOptions } from './types.js';

export const seedConnector: Connector = {
  id: 'seed',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const seed = options.seed;
    if (!seed) throw BridgeError.notConnected('seedConnector requires seed');
    // Reuse the shared Connection — never clone via toRpcOptions() (detaches sticky RPC).
    const wallet = new SplendorPQBridge({
      ...options,
      connection,
    });
    await wallet.init();
    await wallet.connectFromSeed(seed);
    return wallet;
  },
};
