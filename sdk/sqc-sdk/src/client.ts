/**
 * SplendorClient — factory for connection + optional wallet.
 */
import { Connection } from './core/connection.js';
import { SplendorPQBridge } from './splendor-wallet.js';
import {
  createSplendorClient,
  type CreateClientOptions,
  type ClientSession,
} from './connect.js';

export class SplendorClient {
  static create(opts: CreateClientOptions = {}): ClientSession {
    return createSplendorClient(opts);
  }

  static connection(opts: CreateClientOptions = {}): Connection {
    const client = createSplendorClient(opts);
    return client.wallet.connection;
  }

  static wallet(opts: CreateClientOptions = {}): SplendorPQBridge {
    return createSplendorClient(opts).wallet;
  }
}
