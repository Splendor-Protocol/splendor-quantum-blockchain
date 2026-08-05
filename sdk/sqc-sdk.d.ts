/**
 * Splendor Quantum Chain — TypeScript Type Definitions
 * 
 * All addresses use the "SQ:" prefix format (32-byte, SHA3-256 derived).
 * All signatures use CRYSTALS-Dilithium Mode 3 (NIST FIPS 204).
 * NO ECDSA. NO keccak. NO Ethereum address format.
 */

export declare const SDK_VERSION: string;

// ── Address types ─────────────────────────────────────────────────────────────

/** A Splendor Quantum Chain address. Format: "SQ:" + 64 hex chars (32 bytes). */
export type SQAddress = string;

/** A SHA3-256 hash as a 64-character hex string (no 0x prefix). */
export type Hash32Hex = string;

// ── Transaction types ─────────────────────────────────────────────────────────

export enum TxType {
  Transfer = 1,
  Deploy   = 2,
  Call     = 3,
  Register = 4,
  X402    = 5,
}

/** An unsigned transaction object. Must be signed before submitting. */
export interface UnsignedTransaction {
  version:   1;
  chain_id:  string;
  type:      TxType;
  from:      SQAddress;
  to:        SQAddress;
  amount:    string;     // smallest SPLD units as decimal string
  fee:       string;     // smallest SPLD units as decimal string
  nonce:     number;
  timestamp: number;     // unix seconds
  data?:     string;     // base64-encoded calldata or memo
}

/** A signed transaction — add public_key (first tx) and signature. */
export interface SignedTransaction extends UnsignedTransaction {
  public_key?: string;  // base64-encoded Dilithium public key (1952 bytes), first tx only
  signature:   string;  // base64-encoded Dilithium signature (3293 bytes)
}

/** Result of submitting a transaction. */
export interface SubmitTxResult {
  status: 'accepted';
  hash:   Hash32Hex;
}

// ── Block types ───────────────────────────────────────────────────────────────

export interface BlockHeader {
  height:         number;
  timestamp:      number;
  chain_id:       string;
  prev_hash:      Hash32Hex;
  tx_merkle_root: Hash32Hex;
  state_root:     Hash32Hex;
  validator_addr: SQAddress;
  tx_count:       number;
}

export interface Block {
  header:              BlockHeader;
  hash:                Hash32Hex;
  validator_signature: string;   // base64-encoded Dilithium signature
  transactions:        SignedTransaction[];
}

// ── Receipt types ─────────────────────────────────────────────────────────────

export enum TxStatus {
  Failed  = 0,
  Success = 1,
}

export interface Log {
  address:      SQAddress;
  topics:       Hash32Hex[];   // SHA3-256 hashes of event signature + indexed params
  data:         string;        // hex-encoded non-indexed event data
  block_height: number;
  tx_hash:      Hash32Hex;
  tx_index:     number;
  log_index:    number;
}

export interface Receipt {
  tx_hash:          Hash32Hex;
  tx_index:         number;
  block_height:     number;
  block_hash:       Hash32Hex;
  status:           TxStatus;
  gas_used:         number;
  error?:           string;
  from:             SQAddress;
  to:               SQAddress;
  amount:           string;
  fee:              string;
  contract_address?: SQAddress;
  logs:             Log[];
  type:             TxType;
}

// ── Account types ─────────────────────────────────────────────────────────────

export interface AccountBalance {
  address:      SQAddress;
  balance:      string;       // smallest SPLD units as decimal string
  balance_spld: string;       // human-readable SPLD (6 decimal places)
  unit:         'smallest_spld';
}

export interface AccountNonce {
  address: SQAddress;
  nonce:   number;
}

export interface ContractInfo {
  address:      SQAddress;
  is_contract:  boolean;
  code_hash:    Hash32Hex;
  storage_root: Hash32Hex;
  balance:      string;
  nonce:        number;
}

// ── Node status ───────────────────────────────────────────────────────────────

export interface NodeStatus {
  chain_id:     string;
  height:       number;
  mempool_size: number;
  peer_count:   number;
  node_version: string;
  timestamp:    number;
}

export interface SyncStatus {
  is_syncing:   boolean;
  local_height: number;
  best_height:  number;
  peer_count:   number;
}

export interface PeerInfo {
  addr:      string;
  last_seen: number;
  height:    number;
  version?:  string;
}

// ── AI scheduler ──────────────────────────────────────────────────────────────

export interface AISchedulerStats {
  BatchSize:       number;
  EMAmempoolDepth: number;
  EMATxPerBlock:   number;
  AnomalyActive:   boolean;
  Observations:    number;
}

// ── X402 payment types ────────────────────────────────────────────────────────

export interface X402PaymentRequirements {
  pay_to:       SQAddress;
  amount:       string;       // smallest SPLD units
  valid_after:  number;       // unix seconds
  valid_before: number;       // unix seconds
  description?: string;
}

export interface X402PaymentPayload {
  version:      1;
  chain_id:     string;
  from:         SQAddress;
  to:           SQAddress;
  amount:       string;
  valid_after:  number;
  valid_before: number;
  nonce:        string;       // 64 hex chars (32 random bytes)
  signature?:   string;       // base64 Dilithium signature — must be added by caller
}

export interface X402VerifyResult {
  valid:   boolean;
  reason?: string;
}

export interface X402SettleResult {
  success:   boolean;
  tx_hash?:  Hash32Hex;
  error?:    string;
}

// ── WebSocket / SSE event types ───────────────────────────────────────────────

export type WSEventType =
  | 'new_block'
  | 'new_tx'
  | 'new_receipt'
  | 'node_status'
  | 'error'
  | 'connected';

export interface WSEvent<T = unknown> {
  type:      WSEventType;
  timestamp: number;
  data:      T;
}

export interface NewBlockEventData {
  height:    number;
  hash:      Hash32Hex;
  tx_count:  number;
  timestamp: number;
  validator: SQAddress;
}

export interface NewTxEventData {
  hash:      Hash32Hex;
  from:      SQAddress;
  to:        SQAddress;
  amount:    string;
  fee:       string;
  type:      TxType;
  timestamp: number;
}

export interface Subscription {
  close(): void;
}

// ── SDK Options ───────────────────────────────────────────────────────────────

export interface SplendorQCOptions {
  timeoutMs?: number;
  chainId?:   string;
}

// ── Main SDK class ────────────────────────────────────────────────────────────

export declare class SplendorQC {
  constructor(rpcUrl?: string, options?: SplendorQCOptions);

  readonly rpcUrl:   string;
  readonly chainId:  string;
  readonly _version: string;

  // Account
  getBalance(address: SQAddress): Promise<AccountBalance>;
  getNonce(address: SQAddress): Promise<AccountNonce>;

  // Transactions
  submitTransaction(signedTx: SignedTransaction): Promise<SubmitTxResult>;
  getReceipt(txHash: Hash32Hex): Promise<Receipt>;

  // Blocks
  getLatestBlock(): Promise<Block>;
  getBlockByHeight(height: number | string): Promise<Block>;
  getBlockByHash(hash: Hash32Hex): Promise<Block>;

  // Node
  getStatus(): Promise<NodeStatus>;
  getPeers(): Promise<PeerInfo[]>;
  getAIStats(): Promise<AISchedulerStats>;

  // Explorer
  getContractInfo(address: SQAddress): Promise<ContractInfo>;
  getStorageValue(address: SQAddress, slot: Hash32Hex): Promise<{ value: string }>;
  getSyncStatus(): Promise<SyncStatus>;

  // X402
  x402Verify(requirements: X402PaymentRequirements, payload: X402PaymentPayload): Promise<X402VerifyResult>;
  x402Settle(requirements: X402PaymentRequirements, payload: X402PaymentPayload): Promise<X402SettleResult>;
  buildX402Payload(opts: {
    from: SQAddress;
    to:   SQAddress;
    amount: string;
    validWindowSecs?: number;
  }): Omit<X402PaymentPayload, 'signature'>;
  buildX402Requirements(payTo: SQAddress, amountSPLD: string): X402PaymentRequirements;

  // Subscriptions (SSE)
  subscribe(
    events: WSEventType | WSEventType[],
    onEvent: (event: WSEvent) => void,
    onError?: (err: Error) => void,
  ): Subscription;

  // Transaction builders
  buildTransfer(opts: {
    from:    SQAddress;
    to:      SQAddress;
    amount:  string;
    fee?:    string;
    nonce:   number;
    memo?:   string;
  }): UnsignedTransaction;

  buildCall(opts: {
    from:      SQAddress;
    contract:  SQAddress;
    calldata:  string | Uint8Array;
    value?:    string;
    fee?:      string;
    nonce:     number;
  }): UnsignedTransaction;

  // Utilities
  toSPLD(units: string | number | bigint): string;
  fromSPLD(spld: string | number): string;
  isValidAddress(addr: string): addr is SQAddress;
}

// ── X402 Express middleware ────────────────────────────────────────────────────

export interface X402MiddlewareConfig {
  payTo: SQAddress;
  /** Price per request in SPLD, or a route-pattern map */
  price: string | number | Record<string, string | number>;
}

/**
 * Express.js middleware that enforces X402 payment for protected routes.
 * @example
 * app.use('/api/premium', x402Middleware(sdk, { payTo: 'SQ:abc...', price: '0.001' }));
 */
export declare function x402Middleware(
  sdk: SplendorQC,
  config: X402MiddlewareConfig,
): (req: unknown, res: unknown, next: () => void) => Promise<void>;

// ── ABI helpers (re-exported from VM) ─────────────────────────────────────────

/**
 * Compute a QSol function selector: SHA3-256(signature)[0:4]
 * Example: selector('transfer(sqaddress,uint256)')
 */
export declare function selector(signature: string): Uint8Array;

/**
 * Encode a function call: selector(signature) + packed uint256 args
 */
export declare function encodeCall(signature: string, ...args: bigint[]): Uint8Array;

/**
 * Decode a uint256 from ABI-encoded bytes at a given offset.
 */
export declare function decodeUint256(data: Uint8Array, offset: number): bigint;
