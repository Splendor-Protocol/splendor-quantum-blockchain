/**
 * Transport — full chain IO surface (reads + submits).
 *
 * Shipped: RpcTransport (SqRpc), MockTransport (tests).
 * Gateway HTTP service remains under sdk/sqc-sdk/gateway/ (no GatewayTransport class).
 */
import type { ModuleCall } from '../module-call.js';
import type {
  EstimateGasRequest,
  EstimateGasResponse,
  GetLogsResponse,
  LogFilter,
  Subscription,
  VerifyContractResponse,
  WsEvent,
  WsEventType,
} from '../explorer-types.js';

export type BalanceResult = {
  address: string;
  balance: string;
  balance_spld: string;
  unit: string;
};

export type NonceResult = { address: string; nonce: number };

export type SubmitResult = { status: string; hash: string };

export type CallRequest = {
  to: string;
  data?: string | Uint8Array;
  from?: string;
  encoding?: 'hex' | 'base64';
  value?: string;
};

export type ConfirmationOpts = {
  from: string;
  nonceBefore: number;
  timeoutMs?: number;
  pollMs?: number;
  hash?: string;
  /** With `hash`: only a successful receipt confirms; the nonce-advance fallback is disabled. */
  requireReceipt?: boolean;
};

export type ConfirmationResult = {
  confirmed: true;
  nonce: number;
  receipt?: Record<string, unknown>;
};

/**
 * Chain transport used by Connection (and thus Wallet IO).
 * get/post remain for low-level paths; high-level methods are required so
 * MockTransport can own the full surface without a hidden SqRpc.
 */
export interface Transport {
  readonly chainId: string;
  readonly rpcUrls: string[];
  readonly rpcUrl: string;
  /** Optional timeout/retries metadata for toRpcOptions. */
  readonly timeoutMs?: number;
  readonly retries?: number;

  get<T>(path: string): Promise<T>;
  post<T>(path: string, body: unknown, stickyOnSuccess?: boolean): Promise<T>;
  moduleCall<T>(envelope: ModuleCall): Promise<T>;

  getBalance(address: string): Promise<BalanceResult>;
  getNonce(address: string): Promise<NonceResult>;
  getStatus(): Promise<Record<string, unknown>>;
  assertChainId(): Promise<void>;
  getLatestBlock(): Promise<unknown>;
  getBlockByHeight(height: number | string): Promise<unknown>;
  getBlockByHash(hash: string): Promise<unknown>;
  getTx(hash: string): Promise<Record<string, unknown>>;
  getTxReceipt(hash: string): Promise<Record<string, unknown>>;
  getContractInfo(address: string): Promise<Record<string, unknown>>;
  getSyncStatus(): Promise<Record<string, unknown>>;
  call(opts: CallRequest): Promise<Record<string, unknown>>;
  estimateGas(opts: EstimateGasRequest): Promise<EstimateGasResponse>;
  getLogs(filter?: LogFilter): Promise<GetLogsResponse>;
  getContractCode(address: string): Promise<Record<string, unknown>>;
  getContractStorage(address: string, slot: string): Promise<Record<string, unknown>>;
  verifyContract(address: string, source: string): Promise<VerifyContractResponse>;
  getAiStats(): Promise<Record<string, unknown>>;
  submitTransaction(signedTx: string | object): Promise<SubmitResult>;
  x402Verify(
    requirements: object,
    payload: object,
  ): Promise<{ valid: boolean; reason?: string; nonce_used?: boolean }>;
  x402Settle(
    requirements: object,
    payload: object,
    settlementTx: object,
  ): Promise<{
    success?: boolean;
    status?: 'submitted' | 'confirmed' | 'failed' | string;
    tx_hash?: string;
    error?: string;
  }>;
  waitForConfirmation(opts: ConfirmationOpts): Promise<ConfirmationResult>;
  subscribe(
    events: WsEventType | WsEventType[],
    onEvent: (ev: WsEvent) => void,
    onError?: (err: unknown) => void,
  ): Subscription;
}
