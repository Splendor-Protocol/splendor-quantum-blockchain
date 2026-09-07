/**
 * Connector interfaces — how users unlock a signing wallet.
 *
 * Two shapes coexist:
 * - {@link Connector} — headless factory: builds a Wallet from a Connection.
 * - {@link WalletUnlockConnector} — unlock API over a prebuilt wallet (Connect UI).
 *
 * Prefer {@link WalletUnlockConnector} for UI kits; keep {@link Connector} for
 * headless `connect()` recipes. `@splendor/connect-react` adapts unlock + UI metadata.
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
