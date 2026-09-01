/**
 * RpcTransport — SqRpc adapter implementing the Transport interface.
 */
import { SqRpc, type RpcOptions } from '../../rpc.js';
import type { ModuleCall } from '../module-call.js';
import { encodeModuleCallHex } from '../module-call.js';
import { MODULE_ROUTER_ADDRESS } from '../../addresses/index.js';
import type { Transport } from './types.js';

export class RpcTransport implements Transport {
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

  get<T>(path: string): Promise<T> {
    return this.rpc.transportGet<T>(path);
  }

  post<T>(path: string, body: unknown, stickyOnSuccess = false): Promise<T> {
    return this.rpc.transportPost<T>(path, body, stickyOnSuccess);
  }

  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    return this.rpc.moduleCall<T>(envelope);
  }

  /** Direct access to full SqRpc for wallet/submit flows. */
  toSqRpc(): SqRpc {
    return this.rpc;
  }
}

export { SqRpc };
