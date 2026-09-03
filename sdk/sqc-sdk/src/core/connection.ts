/**
 * Connection — read-only chain client (replaces SqcProvider in the new API).
 */
import type { Transport } from './transport/types.js';
import { RpcTransport } from './transport/rpc-transport.js';
import { SqRpc, fromSPLD, toSPLD, type RpcOptions } from '../rpc.js';
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

export type ConnectionOptions = RpcOptions & {
  transport?: Transport;
  /** Wrap an existing SqRpc (shared with wallet). */
  existingRpc?: SqRpc;
};

export class Connection {
  readonly transport: Transport;
  readonly rpc: SqRpc;

  constructor(options: ConnectionOptions = {}) {
    if (options.existingRpc) {
      this.rpc = options.existingRpc;
      this.transport = options.transport ?? RpcTransport.fromSqRpc(options.existingRpc);
      return;
    }
    if (options.transport) {
      this.transport = options.transport;
      if (options.transport instanceof RpcTransport) {
        this.rpc = options.transport.toSqRpc();
      } else {
        // Non-RPC transports (e.g. MockTransport): keep a SqRpc aligned to the
        // transport's chain/urls for wallet helpers that still need rpc.*;
        // moduleCall always goes through this.transport.
        this.rpc = new SqRpc({
          ...options,
          chainId: options.transport.chainId,
          rpcUrl: options.transport.rpcUrl,
          rpcUrls: options.transport.rpcUrls,
        });
      }
    } else {
      const rt = new RpcTransport(options);
      this.transport = rt;
      this.rpc = rt.toSqRpc();
    }
  }

  static fromSqRpc(rpc: SqRpc): Connection {
    return new Connection({ existingRpc: rpc });
  }

  get rpcUrl(): string {
    return this.rpc.rpcUrl;
  }

  get rpcUrls(): string[] {
    return this.rpc.rpcUrls;
  }

  get chainId(): string {
    return this.rpc.chainId;
  }

  toRpcOptions(): RpcOptions {
    return {
      rpcUrl: this.rpc.rpcUrls[0],
      rpcUrls: this.rpc.rpcUrls,
      timeoutMs: this.rpc.timeoutMs,
      chainId: this.rpc.chainId,
      retries: this.rpc.retries,
    };
  }

  async getBalance(address: string) {
    return this.rpc.getBalance(address);
  }

  async getNonce(address: string) {
    return this.rpc.getNonce(address);
  }

  async getStatus() {
    return this.rpc.getStatus();
  }

  async assertChainId() {
    return this.rpc.assertChainId();
  }

  async getLatestBlock() {
    return this.rpc.getLatestBlock();
  }

  async getBlockByHeight(height: number | string) {
    return this.rpc.getBlockByHeight(height);
  }

  async getBlockByHash(hash: string) {
    return this.rpc.getBlockByHash(hash);
  }

  async getTx(hash: string) {
    return this.rpc.getTx(hash);
  }

  async getTxReceipt(hash: string) {
    return this.rpc.getTxReceipt(hash);
  }

  async getContractInfo(address: string) {
    return this.rpc.getContractInfo(address);
  }

  async getSyncStatus() {
    return this.rpc.getSyncStatus();
  }

  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    return this.transport.moduleCall<T>(envelope);
  }

  async estimateGas(opts: EstimateGasRequest): Promise<EstimateGasResponse> {
    return this.rpc.estimateGas(opts);
  }

  async getLogs(filter?: LogFilter): Promise<GetLogsResponse> {
    return this.rpc.getLogs(filter);
  }

  async getContractCode(address: string) {
    return this.rpc.getContractCode(address);
  }

  async getContractStorage(address: string, slot: string) {
    return this.rpc.getContractStorage(address, slot);
  }

  async verifyContract(address: string, source: string): Promise<VerifyContractResponse> {
    return this.rpc.verifyContract(address, source);
  }

  async getAiStats() {
    return this.rpc.getAiStats();
  }

  subscribe(
    events: WsEventType | WsEventType[],
    onEvent: (ev: WsEvent) => void,
    onError?: (err: unknown) => void,
  ): Subscription {
    return this.rpc.subscribe(events, onEvent, onError);
  }

  async waitForConfirmation(opts: {
    from: string;
    nonceBefore: number;
    timeoutMs?: number;
    pollMs?: number;
    hash?: string;
  }) {
    return this.rpc.waitForConfirmation(opts);
  }
}

export { fromSPLD, toSPLD };
