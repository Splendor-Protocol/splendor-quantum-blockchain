/**
 * Read-only SQC provider — no WASM / signing.
 * @deprecated Prefer `Connection` from `@splendor/sqc-sdk/core`.
 * Share across UI components; create Wallet only when the user connects.
 */
import { SqRpc, fromSPLD, toSPLD, type RpcOptions } from './rpc.js';

export type ProviderOptions = RpcOptions;

export class SqcProvider {
  readonly rpc: SqRpc;

  constructor(options: ProviderOptions = {}, existingRpc?: SqRpc) {
    this.rpc = existingRpc ?? new SqRpc(options);
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

  get timeoutMs(): number {
    return this.rpc.timeoutMs;
  }

  get retries(): number {
    return this.rpc.retries;
  }

  /** Options suitable for constructing SplendorPQBridge with the same RPC endpoints. */
  toBridgeOptions(): RpcOptions {
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
