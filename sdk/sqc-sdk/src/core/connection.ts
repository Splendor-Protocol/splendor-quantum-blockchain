/**
 * Connection — chain client backed entirely by Transport.
 * Prefer Connection over deprecated SqcProvider.
 */
import type { Transport } from './transport/types.js';
import { RpcTransport } from './transport/rpc-transport.js';
import { SqRpc, fromSPLD, toSPLD, type RpcOptions } from '../rpc.js';
import { BridgeError } from '../errors.js';
import type { ModuleCall } from './module-call.js';
import type {
  EstimateGasRequest,
  EstimateGasResponse,
  GetLogsResponse,
  LogFilter,
  Subscription,
  VerifyContractResponse,
  WsEvent,
  WsEventType,
} from './explorer-types.js';
import type { CallRequest, ConfirmationOpts } from './transport/types.js';

export type ConnectionOptions = RpcOptions & {
  transport?: Transport;
  /** Wrap an existing SqRpc (shared with wallet). */
  existingRpc?: SqRpc;
};

export class Connection {
  readonly transport: Transport;

  constructor(options: ConnectionOptions = {}) {
    if (options.existingRpc) {
      this.transport = options.transport ?? RpcTransport.fromSqRpc(options.existingRpc);
      return;
    }
    if (options.transport) {
      this.transport = options.transport;
      return;
    }
    this.transport = new RpcTransport(options);
  }

  static fromSqRpc(rpc: SqRpc): Connection {
    return new Connection({ existingRpc: rpc });
  }

  static fromTransport(transport: Transport): Connection {
    return new Connection({ transport });
  }

  /**
   * @deprecated Prefer Transport-backed Connection methods. Only available when
   * the underlying transport is RpcTransport.
   */
  get rpc(): SqRpc {
    if (this.transport instanceof RpcTransport) {
      return this.transport.toSqRpc();
    }
    throw BridgeError.rpc(
      'Connection.rpc is unavailable for non-RpcTransport (use Connection methods / Transport)',
    );
  }

  get rpcUrl(): string {
    return this.transport.rpcUrl;
  }

  get rpcUrls(): string[] {
    return this.transport.rpcUrls;
  }

  get chainId(): string {
    return this.transport.chainId;
  }

  toRpcOptions(): RpcOptions {
    return {
      rpcUrl: this.transport.rpcUrls[0],
      rpcUrls: this.transport.rpcUrls,
      timeoutMs: this.transport.timeoutMs,
      chainId: this.transport.chainId,
      retries: this.transport.retries,
    };
  }

  async getBalance(address: string) {
    return this.transport.getBalance(address);
  }

  async getNonce(address: string) {
    return this.transport.getNonce(address);
  }

  async getStatus() {
    return this.transport.getStatus();
  }

  async assertChainId() {
    return this.transport.assertChainId();
  }

  async getLatestBlock() {
    return this.transport.getLatestBlock();
  }

  async getBlockByHeight(height: number | string) {
    return this.transport.getBlockByHeight(height);
  }

  async getBlockByHash(hash: string) {
    return this.transport.getBlockByHash(hash);
  }

  async getTx(hash: string) {
    return this.transport.getTx(hash);
  }

  async getTxReceipt(hash: string) {
    return this.transport.getTxReceipt(hash);
  }

  async getContractInfo(address: string) {
    return this.transport.getContractInfo(address);
  }

  async getSyncStatus() {
    return this.transport.getSyncStatus();
  }

  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    return this.transport.moduleCall<T>(envelope);
  }

  async call(opts: CallRequest) {
    return this.transport.call(opts);
  }

  async estimateGas(opts: EstimateGasRequest): Promise<EstimateGasResponse> {
    return this.transport.estimateGas(opts);
  }

  async getLogs(filter?: LogFilter): Promise<GetLogsResponse> {
    return this.transport.getLogs(filter);
  }

  async getContractCode(address: string) {
    return this.transport.getContractCode(address);
  }

  async getContractStorage(address: string, slot: string) {
    return this.transport.getContractStorage(address, slot);
  }

  async verifyContract(address: string, source: string): Promise<VerifyContractResponse> {
    return this.transport.verifyContract(address, source);
  }

  async getAiStats() {
    return this.transport.getAiStats();
  }

  async submitTransaction(signedTx: string | object) {
    return this.transport.submitTransaction(signedTx);
  }

  async x402Verify(requirements: object, payload: object) {
    return this.transport.x402Verify(requirements, payload);
  }

  async x402Settle(requirements: object, payload: object, settlementTx: object) {
    return this.transport.x402Settle(requirements, payload, settlementTx);
  }

  subscribe(
    events: WsEventType | WsEventType[],
    onEvent: (ev: WsEvent) => void,
    onError?: (err: unknown) => void,
  ): Subscription {
    return this.transport.subscribe(events, onEvent, onError);
  }

  async waitForConfirmation(opts: ConfirmationOpts) {
    return this.transport.waitForConfirmation(opts);
  }
}

export { fromSPLD, toSPLD };
