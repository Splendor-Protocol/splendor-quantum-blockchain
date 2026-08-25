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

export type ConnectorField = {
  name: string;
  type: 'password' | 'text';
  label: string;
  placeholder?: string;
};

/**
 * How strong the key this connector produces actually is.
 *
 * - `native`: the key comes from post-quantum entropy. Security rests on
 *   ML-DSA-65 alone.
 *
 * - `evm-derived`: the ML-DSA seed is SHA3-256 of an ECDSA signature. Signing
 *   is post-quantum, but the key is only as strong as secp256k1: an adversary
 *   with a quantum computer recovers the EVM private key from any signature
 *   that account has published, replays the deterministic derivation message,
 *   and reproduces this exact ML-DSA key.
 */
export type ConnectorKeyOrigin = 'native' | 'evm-derived';

/**
 * Modular wallet connector (AppKit-style).
 * Implementations unlock the PQ WASM session via @splendor/pq-bridge.
 */
export type Connector = {
  id: string;
  name: string;
  icon?: string;
  /** When true, UI shows this under Advanced. */
  advanced?: boolean;
  /**
   * How this connector's key is produced. Connectors MUST declare this so the
   * UI can tell the user when an account is not quantum-safe.
   */
  keyOrigin: ConnectorKeyOrigin;
  /**
   * False when the key inherits secp256k1's weakness. The ConnectModal renders
   * a warning for these, so a user is never shown an EVM-derived account as if
   * it were post-quantum.
   */
  postQuantumSecure: boolean;
  /** Form fields for advanced connectors (rendered by ConnectModal). */
  connectFields?: ConnectorField[];
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
