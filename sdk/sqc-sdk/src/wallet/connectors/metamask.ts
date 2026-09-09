import { SplendorPQBridge } from '../../splendor-wallet.js';
import type { Connection } from '../../core/connection.js';
import { BridgeError } from '../../errors.js';
import type { Connector, ConnectorConnectOptions } from './types.js';

export const metamaskConnector: Connector = {
  id: 'metamask',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const wallet = new SplendorPQBridge({
      ...options,
      connection,
      ethereumProvider: options.ethereumProvider,
    });
    await wallet.init();
    await wallet.connectFromMetaMask(options.ethereumProvider);
    return wallet;
  },
};

export const evmKeyConnector: Connector = {
  id: 'evmKey',
  async connect(connection: Connection, options: ConnectorConnectOptions = {}) {
    const wallet = new SplendorPQBridge({
      ...options,
      connection,
    });
    await wallet.init();
    if (!options.evmPrivateKey) {
      throw BridgeError.notConnected('evmKeyConnector requires evmPrivateKey');
    }
    await wallet.connectFromEvmPrivateKey(options.evmPrivateKey);
    return wallet;
  },
};
