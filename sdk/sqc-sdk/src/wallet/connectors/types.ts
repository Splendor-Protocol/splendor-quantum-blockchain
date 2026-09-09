/**
 * Connector interfaces — how users unlock a signing wallet.
 *
 * Naming:
 * - {@link WalletFactoryConnector} — headless factory: builds a Wallet from a Connection
 *   (SDK `connect()` recipes). Prefer this name; {@link Connector} is a deprecated alias.
 * - {@link WalletUnlockConnector} — unlock API over a prebuilt wallet (Connect UI shape).
 *
 * `@splendor/connect-react` uses its own UI `Connector` type (different shape + metadata).
 * Do not pass SDK factory connectors into SplendorConnectProvider.
 */
import type { Connection } from '../../core/connection.js';
import type { SplendorPQBridge } from '../../splendor-wallet.js';
import type { BridgeOptions, DeriveInfo } from '../../types.js';

/** Context passed when unlocking an already-constructed wallet. */
export type WalletUnlockContext = {
  wallet: SplendorPQBridge;
  connection: Connection;
};

/**
 * Canonical unlock connector (UI / ConnectKit style).
 * Implementations unlock WASM on `ctx.wallet` and return derive info.
 */
export interface WalletUnlockConnector {
  readonly id: string;
  unlock(
    ctx: WalletUnlockContext,
    options?: Record<string, unknown>,
  ): Promise<DeriveInfo>;
}

/**
 * Headless factory connector — creates/unlocks a wallet from a Connection.
 * Used by `@splendor/sqc-sdk` `connect()` helpers.
 */
export interface WalletFactoryConnector {
  readonly id: string;
  connect(connection: Connection, options?: Record<string, unknown>): Promise<SplendorPQBridge>;
}

/**
 * @deprecated Use {@link WalletFactoryConnector} — name collided with connect-react UI Connector.
 */
export type Connector = WalletFactoryConnector;

export type ConnectorConnectOptions = BridgeOptions & {
  seed?: string;
  evmPrivateKey?: string;
  evmSignature?: string;
  evmAddress?: string;
  ethereumProvider?: BridgeOptions['ethereumProvider'];
};
