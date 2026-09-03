/**
 * MockTransport — scripted Transport for unit tests (no network).
 */
import type { ModuleCall } from '../module-call.js';
import type { Transport } from './types.js';
import { BridgeError } from '../../errors.js';

export type MockTransportHandlers = {
  get?: (path: string) => Promise<unknown> | unknown;
  post?: (path: string, body: unknown) => Promise<unknown> | unknown;
  moduleCall?: (envelope: ModuleCall) => Promise<unknown> | unknown;
};

export class MockTransport implements Transport {
  readonly chainId: string;
  readonly rpcUrls: string[];
  readonly rpcUrl: string;
  private handlers: MockTransportHandlers;
  readonly calls: {
    get: string[];
    post: Array<{ path: string; body: unknown }>;
    moduleCall: ModuleCall[];
  } = { get: [], post: [], moduleCall: [] };

  constructor(
    opts: {
      chainId?: string;
      rpcUrl?: string;
      rpcUrls?: string[];
      handlers?: MockTransportHandlers;
    } = {},
  ) {
    this.chainId = opts.chainId ?? 'splendor-quantum-1';
    this.rpcUrls = opts.rpcUrls ?? [opts.rpcUrl ?? 'http://mock.local'];
    this.rpcUrl = this.rpcUrls[0]!;
    this.handlers = opts.handlers ?? {};
  }

  setHandlers(handlers: MockTransportHandlers): void {
    this.handlers = { ...this.handlers, ...handlers };
  }

  async get<T>(path: string): Promise<T> {
    this.calls.get.push(path);
    if (!this.handlers.get) {
      throw BridgeError.rpc(`MockTransport: no get handler for ${path}`);
    }
    return (await this.handlers.get(path)) as T;
  }

  async post<T>(path: string, body: unknown, _stickyOnSuccess = false): Promise<T> {
    this.calls.post.push({ path, body });
    if (!this.handlers.post) {
      throw BridgeError.rpc(`MockTransport: no post handler for ${path}`);
    }
    return (await this.handlers.post(path, body)) as T;
  }

  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    this.calls.moduleCall.push(envelope);
    if (!this.handlers.moduleCall) {
      throw BridgeError.rpc(
        `MockTransport: no moduleCall handler for ${envelope.module}.${envelope.action}`,
      );
    }
    return (await this.handlers.moduleCall(envelope)) as T;
  }
}
