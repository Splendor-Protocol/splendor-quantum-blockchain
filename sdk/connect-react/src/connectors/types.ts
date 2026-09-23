/**
 * Connect-react UI connector — metadata + unlock for ConnectKit / SplendorConnectProvider.
 *
 * Supported wallets: **Splendor Quantum Wallet extension** and **native in-page seed**.
 * This is **not** MetaMask / EIP-1193 / WalletConnect. Do not pass
 * `@splendor/sqc-sdk` headless `WalletFactoryConnector` factories here
 * (`seedWalletFactory` / `extensionWalletFactory`) — different shape.
 */
import type {
  DeriveInfo,
  ExtensionSigner,
  NetworkConfig,
  SplendorPQBridge,
  SqcProvider,
  WalletUnlockContext,
} from '@splendor/sqc-sdk';

/**
 * Connect-react connector context — unlocks a prebuilt wallet.
 * Extends the SDK {@link WalletUnlockContext} with UI-facing provider/network.
 */
export type ConnectorContext = WalletUnlockContext & {
  /** @deprecated Prefer `connection` (same RPC). Kept for existing connectors. */
  provider: SqcProvider;
  network: NetworkConfig;
  /**
   * When connecting via ExtensionSigner, replace the session wallet so hooks
   * (`useWallet`, transfers, etc.) route through the extension.
   */
  setSessionWallet?: (wallet: SplendorPQBridge | ExtensionSigner) => void;
};

export type ConnectorField = {
  name: string;
  type: 'password' | 'text';
  label: string;
  placeholder?: string;
};

/**
 * Key provenance for ConnectKit.
 * Only `native` (ML-DSA-65 from extension or CSPRNG seed) is supported.
 * `evm-derived` is retained for type compatibility but is not offered in UI.
 */
export type ConnectorKeyOrigin = 'native' | 'evm-derived';

/**
 * Modular wallet connector (AppKit-style).
 * Adapts SDK `WalletUnlockConnector`: `connect(ctx)` unlocks WASM on `ctx.wallet`.
 * UI fields (`name`, `icon`, `keyOrigin`) stay in connect-react.
 */
export type Connector = {
  id: string;
  name: string;
  icon?: string;
  /** When true, UI shows this under Advanced. */
  advanced?: boolean;
  /**
   * How this connector's key is produced. Must be `native` for ConnectKit
   * (extension or in-page seed).
   */
  keyOrigin: ConnectorKeyOrigin;
  /** Must be true for supported Splendor PQ connectors. */
  postQuantumSecure: boolean;
  /** Form fields for seed / advanced connectors (rendered by ConnectModal). */
  connectFields?: ConnectorField[];
  ready: () => boolean | Promise<boolean>;
  /**
   * Optional: already authorized (e.g. extension `sqc_accounts` / `splendor_accounts`).
   * Used by autoReconnect to avoid wallet popups.
   */
  authorized?: () => boolean | Promise<boolean>;
  connect: (ctx: ConnectorContext, opts?: Record<string, unknown>) => Promise<DeriveInfo>;
  disconnect?: () => Promise<void>;
  /** Extension account switch — callback receives SQ: address (not EVM). */
  onAccountsChanged?: (cb: (address: string) => void) => () => void;
};

// Re-export SDK unlock types for addon authors.
export type { WalletUnlockContext };
export type { WalletUnlockConnector } from '@splendor/sqc-sdk';
