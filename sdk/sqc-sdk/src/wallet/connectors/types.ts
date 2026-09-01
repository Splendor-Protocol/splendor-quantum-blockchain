/**
 * Connector interface — how users unlock a signing wallet.
 */
import type { Connection } from '../../core/connection.js';
import type { SplendorPQBridge } from '../../splendor-wallet.js';
import type { BridgeOptions } from '../../types.js';

export interface Connector {
  readonly id: string;
  connect(connection: Connection, options?: Record<string, unknown>): Promise<SplendorPQBridge>;
}

export type ConnectorConnectOptions = BridgeOptions & {
  seed?: string;
  evmPrivateKey?: string;
  evmSignature?: string;
  evmAddress?: string;
  ethereumProvider?: BridgeOptions['ethereumProvider'];
};
