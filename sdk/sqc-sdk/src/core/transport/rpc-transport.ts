/**
 * RpcTransport — SqRpc adapter implementing the full Transport interface.
 * SqRpc stays private to this adapter; Connection should not need a parallel client.
 */
import { SqRpc, type RpcOptions } from '../../rpc.js';
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
import type {
  BalanceResult,
  CallRequest,
  ConfirmationOpts,
  ConfirmationResult,
  NonceResult,
  SubmitResult,
  Transport,
} from './types.js';

export class RpcTransport implements Transport {
  /** @internal Prefer Transport methods; exposed for legacy Connection.rpc / Wallet.rpc. */
  readonly rpc: SqRpc;

  constructor(options: RpcOptions = {}) {
    this.rpc = new SqRpc(options);
  }

  static fromSqRpc(rpc: SqRpc): RpcTransport {
    const t = Object.create(RpcTransport.prototype) as RpcTransport;
    (t as { rpc: SqRpc }).rpc = rpc;
    return t;
  }

  get chainId(): string {
    return this.rpc.chainId;
  }

  get rpcUrls(): string[] {
    return this.rpc.rpcUrls;
  }

  get rpcUrl(): string {
    return this.rpc.rpcUrl;
  }

  get timeoutMs(): number {
    return this.rpc.timeoutMs;
  }

  get retries(): number {
    return this.rpc.retries;
  }

  get<T>(path: string): Promise<T> {
    return this.rpc.transportGet<T>(path);
  }

  post<T>(path: string, body: unknown, stickyOnSuccess = false): Promise<T> {
    return this.rpc.transportPost<T>(path, body, stickyOnSuccess);
  }

  moduleCall<T>(envelope: ModuleCall): Promise<T> {
    return this.rpc.moduleCall<T>(envelope);
  }

  getBalance(address: string): Promise<BalanceResult> {
    return this.rpc.getBalance(address);
  }

  getNonce(address: string): Promise<NonceResult> {
    return this.rpc.getNonce(address);
  }

  getStatus(): Promise<Record<string, unknown>> {
    return this.rpc.getStatus();
  }

  assertChainId(): Promise<void> {
    return this.rpc.assertChainId();
  }

  getLatestBlock(): Promise<unknown> {
    return this.rpc.getLatestBlock();
  }

  getBlockByHeight(height: number | string): Promise<unknown> {
    return this.rpc.getBlockByHeight(height);
  }

  getBlockByHash(hash: string): Promise<unknown> {
    return this.rpc.getBlockByHash(hash);
  }

  getTx(hash: string): Promise<Record<string, unknown>> {
    return this.rpc.getTx(hash);
  }

  getTxReceipt(hash: string): Promise<Record<string, unknown>> {
    return this.rpc.getTxReceipt(hash);
  }

  getContractInfo(address: string): Promise<Record<string, unknown>> {
    return this.rpc.getContractInfo(address);
  }

  getSyncStatus(): Promise<Record<string, unknown>> {
    return this.rpc.getSyncStatus();
  }

  call(opts: CallRequest): Promise<Record<string, unknown>> {
    if (opts.data == null) {
      throw new Error('RpcTransport.call requires data');
    }
    return this.rpc.call({ ...opts, data: opts.data });
  }

  estimateGas(opts: EstimateGasRequest): Promise<EstimateGasResponse> {
    return this.rpc.estimateGas(opts);
  }

  getLogs(filter?: LogFilter): Promise<GetLogsResponse> {
    return this.rpc.getLogs(filter);
  }

  getContractCode(address: string): Promise<Record<string, unknown>> {
    return this.rpc.getContractCode(address);
  }

  getContractStorage(address: string, slot: string): Promise<Record<string, unknown>> {
    return this.rpc.getContractStorage(address, slot);
  }

  verifyContract(address: string, source: string): Promise<VerifyContractResponse> {
    return this.rpc.verifyContract(address, source);
  }

  getAiStats(): Promise<Record<string, unknown>> {
    return this.rpc.getAiStats();
  }

  submitTransaction(signedTx: string | object): Promise<SubmitResult> {
    return this.rpc.submitTransaction(signedTx);
  }

  x402Verify(requirements: object, payload: object) {
    return this.rpc.x402Verify(requirements, payload);
  }

  x402Settle(requirements: object, payload: object, settlementTx: object) {
    return this.rpc.x402Settle(requirements, payload, settlementTx);
  }

  waitForConfirmation(opts: ConfirmationOpts): Promise<ConfirmationResult> {
    return this.rpc.waitForConfirmation(opts);
  }

  subscribe(
    events: WsEventType | WsEventType[],
    onEvent: (ev: WsEvent) => void,
    onError?: (err: unknown) => void,
  ): Subscription {
    return this.rpc.subscribe(events, onEvent, onError);
  }

  /** @deprecated Prefer Transport methods. */
  toSqRpc(): SqRpc {
    return this.rpc;
  }
}

export { SqRpc };
