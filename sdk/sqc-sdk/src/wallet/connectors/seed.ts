import { SplendorPQBridge } from '../../splendor-wallet.js';
import type { Connection } from '../../core/connection.js';
import { BridgeError } from '../../errors.js';
import type { Connector, ConnectorConnectOptions } from './types.js';

export const seedConnector: Connector = {
  id: 'seed',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const seed = options.seed;
    if (!seed) throw BridgeError.notConnected('seedConnector requires seed');
    const wallet = new SplendorPQBridge({ ...connection.toRpcOptions(), ...options });
    await wallet.init();
    await wallet.connectFromSeed(seed);
    return wallet;
  },
};
