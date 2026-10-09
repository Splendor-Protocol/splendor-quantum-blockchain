/** Shared bridge types (no runtime deps — breaks circular imports). */
import type { RpcOptions } from './rpc.js';
import type { SqcProvider } from './provider.js';
import type { AbiArg } from './abi.js';

export type BridgeOptions = RpcOptions & {
  wasmUrl?: string;
  execUrl?: string;
  /** Optional SHA-256 hex of sqc_signer.wasm — verified when set. */
  wasmSha256?: string;
  /** Optional SHA-256 hex of wasm_exec.js — verified when set. */
  execSha256?: string;
  /**
   * Allow loading WASM without wasmSha256 on non-local browser networks.
   * Default false — fail closed unless pin is provided.
   */
  allowUnpinnedWasm?: boolean;
  /** Refuse to sign any tx whose fee (base units) exceeds this cap. */
  maxFee?: string;
  /**
   * Network id from sqc.networks.json (local | testnet | mainnet).
   * Used for browser pin policy and HTTPS checks.
   */
  networkId?: string;
  /**
   * Allow http:// RPC URLs for non-local networks (private LAN).
   * Default false — resolveNetwork requires https for testnet/mainnet.
   */
  allowInsecureHttp?: boolean;
  /**
   * Auto-lock after this many ms of inactivity (browser). 0 / undefined = disabled.
   */
  sessionIdleMs?: number;
  /**
   * When true, also lock immediately when the tab becomes hidden.
   * Default false — only the idle timer locks (avoids surprise desync on tab switch).
   */
  lockOnHidden?: boolean;
  /** Optional shared read provider; RPC options are taken from it when provided. */
  provider?: SqcProvider;
  /** Prefer over provider — Transport-backed chain client. */
  connection?: import('./core/connection.js').Connection;
  /** Optional request/error middleware for observability. */
  onRequest?: (info: { method: string; path?: string }) => void;
  onError?: (err: unknown) => void;
  /**
   * Security warnings the caller should surface to the user — currently emitted
   * when connecting with a key that is not post-quantum secure.
   */
  onWarning?: (warning: { code: 'NOT_POST_QUANTUM'; message: string }) => void;
};

/**
 * How the ML-DSA key was obtained, and therefore how strong it actually is.
 *
 * - `native`: the key comes from post-quantum entropy (a generated or supplied
 *   seed). Its security rests on ML-DSA-65 alone.
 *
 * - `evm-derived`: the seed is SHA3-256 of an ECDSA `personal_sign` signature
 *   from an Ethereum key. The signing itself is ML-DSA, but the key is only as
 *   strong as secp256k1: an adversary with a quantum computer recovers the EVM
 *   private key from any signature that account has ever published, replays the
 *   deterministic derivation message, and reproduces this exact ML-DSA key.
 *   Convenient for onboarding — NOT quantum-safe for value at rest.
 */
export type KeyOrigin = 'native' | 'evm-derived';

export type DeriveInfo = {
  address: string;
  public_key_b64: string;
  public_key_hex: string;
  evmAddress?: string;

  /** How this key was produced. See KeyOrigin. */
  keyOrigin?: KeyOrigin;

  /**
   * False when the key is EVM-derived: the account inherits secp256k1's
   * vulnerability regardless of the signature algorithm used on the wire.
   */
  postQuantumSecure?: boolean;
};

export type ConnectionState = 'disconnected' | 'connected';

export type TransferOpts = {
  to: string;
  /** Atto amount string; omit when amountSPLD is set. */
  amount?: string;
  amountSPLD?: string;
  fee?: string;
  memo?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
  /**
   * Accept a nonce advance as confirmation when the node has no receipt yet. Default false:
   * another tx from the same account can advance the nonce, so only a receipt confirms.
   */
  allowNonceConfirmation?: boolean;
};

export type CallOpts = {
  contract: string;
  calldata?: Uint8Array | string;
  function?: string;
  args?: AbiArg[];
  value?: string;
  valueSPLD?: string;
  fee?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
  /** See `TransferOpts.allowNonceConfirmation`. */
  allowNonceConfirmation?: boolean;
};

export type DeployOpts = {
  bytecode: Uint8Array | string;
  fee?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
  /** See `TransferOpts.allowNonceConfirmation`. */
  allowNonceConfirmation?: boolean;
};

export type X402PayOpts = {
  to: string;
  amount: string;
  amountSPLD?: string;
  validAfter?: number;
  /** Unix seconds after which the authorisation expires. Defaults to now + 300. */
  validBefore?: number;
  requirements?: object;
  settle?: boolean;
  /** Refuse to sign when the payment amount (base units) exceeds this cap. */
  maxAmount?: string;
  /**
   * Fee for the on-chain settlement transaction. Settlement is a normal
   * transaction that a validator must include, so it pays a fee like any other.
   * Defaults to MIN_FEE.
   */
  fee?: string;
};

/** Dry-run / preflight estimate (no signing). */
export type PreflightResult = {
  from: string;
  balance: string;
  balance_spld: string;
  nonce: number;
  fee: string;
  chainId: string;
  sufficient: boolean;
};

/** Native module call write (qrc20, dex, etc.). */
/** Fee and confirmation options accepted by every module-client write (token, NFT, bridge). */
export type WriteOpts = Pick<ModuleCallOpts, 'fee' | 'waitForConfirmation' | 'confirmTimeoutMs' | 'allowNonceConfirmation'>;

export type ModuleCallOpts = {
  module: string;
  action: string;
  params: Record<string, unknown>;
  fee?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
  /** See `TransferOpts.allowNonceConfirmation`. */
  allowNonceConfirmation?: boolean;
};

/** QRC-20 permit signing params (off-chain ML-DSA authorisation). */
export type PermitParams = {
  token: string;
  spender: string;
  amount: string;
  nonce: number;
  deadline: number;
};

export type SignedPermit = {
  owner: string;
  token: string;
  spender: string;
  amount: string;
  nonce: number;
  deadline: number;
  chain_id: string;
  digest_hex: string;
  signature: number[];
  owner_pubkey: number[];
};

/** Exchange order signing (off-chain ML-DSA, exchange module). */
export type OrderParams = {
  exchange: string;
  /** 64 hex chars; empty = random salt (signOrder only). */
  salt?: string;
  condition: number;
  outcome: number;
  /** 0 = buy, 1 = sell (types.OrderSideBuy / OrderSideSell). */
  side: number;
  share_amount: string;
  /** 1e6-scaled price in (0, 1e6). */
  price: string;
  /** Block time in seconds; 0 = never expires. */
  expiration: number;
  nonce: number;
  /** Empty = pay the maker. */
  recipient?: string;
};

export type SignedOrder = {
  salt: string;
  maker: string;
  exchange: string;
  condition: number;
  outcome: number;
  side: number;
  share_amount: string;
  price: string;
  expiration: number;
  nonce: number;
  recipient?: string;
  signature?: number[];
  maker_pubkey?: number[];
  hash: string;
  chain_id: string;
};

export type SignedCancel = {
  exchange: string;
  order: SignedOrder;
  cancel_signature: number[];
  maker_pubkey: number[];
  order_hash: string;
  digest_hex: string;
  chain_id: string;
};

export type IncrementNonceParams = {
  exchange: string;
  expected_nonce: number;
};

export type SignedIncrementNonce = {
  exchange: string;
  maker: string;
  expected_nonce: number;
  signature: number[];
  maker_pubkey: number[];
  chain_id: string;
  digest_hex: string;
};

/** Bridge claim attestation (off-chain ML-DSA under CtxBridge). */
export type BridgeClaimSignParams = {
  bridge: string;
  id: number;
  asset: 'spld' | 'splv';
  amount: string;
  recipient: string;
  /** 0x + 64 hex chars (legacy burn tx hash). */
  evm_tx: string;
};

export type SignedBridgeClaim = {
  pubkey: number[];
  signature: number[];
  chain_id: string;
  digest_hex: string;
  bridge: string;
  id: number;
  asset: string;
  amount: string;
  recipient: string;
  evm_tx: string;
};

/** Nullable account snapshot — does not throw when locked/disconnected. */
export type WalletAccount = {
  address: string;
  publicKeyB64: string;
  keyOrigin: KeyOrigin;
  postQuantumSecure: boolean;
  evmAddress?: string;
};

/**
 * Unified wallet.connect() options — seed-only (native ML-DSA).
 * Extension unlock uses {@link ExtensionSigner} / connect({ mode: 'extension' }).
 */
export type WalletConnectOpts = { mode: 'seed'; seed: string };

/** Unsigned transaction envelope for signTransaction / sendTransaction. */
export type UnsignedTx =
  | ({ type: 'transfer' } & TransferOpts)
  | ({ type: 'call' } & CallOpts)
  | ({ type: 'deploy' } & DeployOpts)
  | ({ type: 'module' } & ModuleCallOpts);

/** Signed wire transaction returned by signTransaction (not yet submitted). */
export type SignedTx = {
  type: UnsignedTx['type'];
  signedTx: object;
  nonce: number;
  /** Wire chain_id at sign time — required; sendTransaction rejects mismatch. */
  chainId: string;
  /**
   * SDK network id at sign time (local/testnet/mainnet/…).
   * Soft-switch can keep the same wire chain_id; networkId catches that.
   */
  networkId: string;
};

/** Result of sendTransaction / signAndSendTransaction. */
export type SendTxResult = {
  status: string;
  hash: string;
  signedTx: object;
  confirmation?: { confirmed: true; nonce: number };
};

export type SignMessageOpts = {
  /**
   * When true, sign the message bytes as-is (Connect v2 challenges, etc.).
   * Default false: domain-separate with SIGN_MESSAGE_DOMAIN unless the message
   * already starts with a known protocol prefix.
   */
  raw?: boolean;
};
