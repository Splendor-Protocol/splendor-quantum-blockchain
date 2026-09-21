/**
 * SplendorClient — factory for connection + optional wallet.
 * Note: the deprecated ClientSession type alias was formerly also named SplendorClient;
 * use ClientSession for the session shape.
 */
import { Connection } from './core/connection.js';
import { SplendorPQBridge } from './splendor-wallet.js';
import {
  createSplendorClient,
  type CreateClientOptions,
  type ClientSession,
} from './connect.js';
import { createPublicClient } from './clients.js';

export class SplendorClient {
  static create(opts: CreateClientOptions = {}): ClientSession {
    return createSplendorClient(opts);
  }

  /** Read-only Connection (no Wallet allocation). */
  static connection(opts: CreateClientOptions = {}): Connection {
    return createPublicClient(opts);
  }

  static wallet(opts: CreateClientOptions = {}): SplendorPQBridge {
    return createSplendorClient(opts).wallet as SplendorPQBridge;
  }
}
