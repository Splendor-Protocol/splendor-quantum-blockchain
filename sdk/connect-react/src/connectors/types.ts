import type {
  DeriveInfo,
  NetworkConfig,
  SplendorPQBridge,
  SqcProvider,
} from '@splendor/pq-bridge';

export type ConnectorContext = {
  /** Constructed client wallet; WASM may not be unlocked yet. */
  wallet: SplendorPQBridge;
  provider: SqcProvider;
  network: NetworkConfig;
};

/**
 * Modular wallet connector (AppKit-style).
 * Implementations unlock the PQ WASM session via @splendor/pq-bridge.
 */
export type Connector = {
  id: string;
  name: string;
  icon?: string;
  /** When true, UI shows this under Advanced (e.g. seed). */
  advanced?: boolean;
  ready: () => boolean | Promise<boolean>;
  /**
   * Optional: already authorized (e.g. eth_accounts non-empty).
   * Used by autoReconnect to avoid wallet popups.
   */
  authorized?: () => boolean | Promise<boolean>;
  connect: (ctx: ConnectorContext, opts?: Record<string, unknown>) => Promise<DeriveInfo>;
  disconnect?: () => Promise<void>;
  onAccountsChanged?: (cb: (evmAddress: string) => void) => () => void;
};
