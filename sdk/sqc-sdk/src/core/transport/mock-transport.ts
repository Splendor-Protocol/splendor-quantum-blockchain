/**
 * MockTransport — scripted full Transport for unit tests (no network).
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
import { BridgeError } from '../../errors.js';
import { assertAddress } from '../../address.js';
import { toSPLD } from '../../rpc.js';
import type {
  BalanceResult,
  CallRequest,
  ConfirmationOpts,
  ConfirmationResult,
  NonceResult,
  SubmitResult,
  Transport,
} from './types.js';

export type MockTransportHandlers = {
  get?: (path: string) => Promise<unknown> | unknown;
  post?: (path: string, body: unknown) => Promise<unknown> | unknown;
  moduleCall?: (envelope: ModuleCall) => Promise<unknown> | unknown;
  getBalance?: (address: string) => Promise<BalanceResult> | BalanceResult;
  getNonce?: (address: string) => Promise<NonceResult> | NonceResult;
  getStatus?: () => Promise<Record<string, unknown>> | Record<string, unknown>;
  submitTransaction?: (tx: object) => Promise<SubmitResult> | SubmitResult;
  waitForConfirmation?: (opts: ConfirmationOpts) => Promise<ConfirmationResult> | ConfirmationResult;
  call?: (opts: CallRequest) => Promise<Record<string, unknown>> | Record<string, unknown>;
  x402Verify?: (
    requirements: object,
    payload: object,
  ) =>
    | Promise<{ valid: boolean; reason?: string; nonce_used?: boolean }>
    | { valid: boolean; reason?: string; nonce_used?: boolean };
  x402Settle?: (
    requirements: object,
    payload: object,
    settlementTx: object,
  ) =>
    | Promise<{
        success?: boolean;
        status?: string;
        tx_hash?: string;
        error?: string;
      }>
    | { success?: boolean; status?: string; tx_hash?: string; error?: string };
};

function missing(method: string): never {
  throw BridgeError.rpc(`MockTransport: no handler for ${method}`);
}

export class MockTransport implements Transport {
  readonly chainId: string;
  readonly rpcUrls: string[];
  readonly rpcUrl: string;
  readonly timeoutMs: number;
  readonly retries: number;
  private handlers: MockTransportHandlers;
  readonly calls: {
    get: string[];
    post: Array<{ path: string; body: unknown }>;
    moduleCall: ModuleCall[];
    submit: object[];
  } = { get: [], post: [], moduleCall: [], submit: [] };

  private nonces = new Map<string, number>();
  private balances = new Map<string, string>();

  constructor(
    opts: {
      chainId?: string;
      rpcUrl?: string;
      rpcUrls?: string[];
      timeoutMs?: number;
      retries?: number;
      handlers?: MockTransportHandlers;
    } = {},
  ) {
    this.chainId = opts.chainId ?? 'splendor-quantum-1';
    this.rpcUrls = opts.rpcUrls ?? [opts.rpcUrl ?? 'http://mock.local'];
    this.rpcUrl = this.rpcUrls[0]!;
    this.timeoutMs = opts.timeoutMs ?? 20_000;
    this.retries = opts.retries ?? 0;
    this.handlers = opts.handlers ?? {};
  }

  setHandlers(handlers: MockTransportHandlers): void {
    this.handlers = { ...this.handlers, ...handlers };
  }

  /** Seed balance for default getBalance path. */
  setBalance(address: string, raw: string): void {
    this.balances.set(assertAddress(address), raw);
  }

  setNonce(address: string, nonce: number): void {
    this.nonces.set(assertAddress(address), nonce);
  }

  async get<T>(path: string): Promise<T> {
    this.calls.get.push(path);
    if (!this.handlers.get) missing(`get(${path})`);
    return (await this.handlers.get(path)) as T;
  }

  async post<T>(path: string, body: unknown, _stickyOnSuccess = false): Promise<T> {
    this.calls.post.push({ path, body });
    if (!this.handlers.post) missing(`post(${path})`);
    return (await this.handlers.post(path, body)) as T;
  }

  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    this.calls.moduleCall.push(envelope);
    if (!this.handlers.moduleCall) {
      missing(`moduleCall(${envelope.module}.${envelope.action})`);
    }
    return (await this.handlers.moduleCall(envelope)) as T;
  }

  async getBalance(address: string): Promise<BalanceResult> {
    if (this.handlers.getBalance) return this.handlers.getBalance(address);
    const canonical = assertAddress(address);
    const balance = this.balances.get(canonical) ?? '0';
    return {
      address: canonical,
      balance,
      balance_spld: toSPLD(balance),
      unit: 'wei',
    };
  }

  async getNonce(address: string): Promise<NonceResult> {
    if (this.handlers.getNonce) return this.handlers.getNonce(address);
    const canonical = assertAddress(address);
    return { address: canonical, nonce: this.nonces.get(canonical) ?? 0 };
  }

  async getStatus(): Promise<Record<string, unknown>> {
    if (this.handlers.getStatus) return this.handlers.getStatus();
    return { chain_id: this.chainId };
  }

  async assertChainId(): Promise<void> {
    const status = await this.getStatus();
    const remote = String(status.chain_id ?? '').trim();
    if (!remote) throw BridgeError.chainMismatch('(missing)', this.chainId);
    if (remote !== this.chainId) throw BridgeError.chainMismatch(remote, this.chainId);
  }

  async getLatestBlock(): Promise<unknown> {
    return this.get('/block/latest');
  }

  async getBlockByHeight(height: number | string): Promise<unknown> {
    return this.get(`/block/${height}`);
  }

  async getBlockByHash(hash: string): Promise<unknown> {
    return this.get(`/block/hash/${encodeURIComponent(hash)}`);
  }

  async getTx(hash: string): Promise<Record<string, unknown>> {
    return this.get(`/tx/${encodeURIComponent(hash.replace(/^0x/i, ''))}`);
  }

  async getTxReceipt(hash: string): Promise<Record<string, unknown>> {
    return this.get(`/tx/${encodeURIComponent(hash.replace(/^0x/i, ''))}/receipt`);
  }

  async getContractInfo(address: string): Promise<Record<string, unknown>> {
    return this.get(`/contract/${encodeURIComponent(assertAddress(address))}`);
  }

  async getSyncStatus(): Promise<Record<string, unknown>> {
    return this.get('/sync');
  }

  async call(opts: CallRequest): Promise<Record<string, unknown>> {
    if (this.handlers.call) return this.handlers.call(opts);
    return this.post('/call', opts, false);
  }

  async estimateGas(_opts: EstimateGasRequest): Promise<EstimateGasResponse> {
    return { gas: 0, fee: '1000', fee_is_floor: true };
  }

  async getLogs(_filter?: LogFilter): Promise<GetLogsResponse> {
    return { logs: [], count: 0, from_block: 0, to_block: 0, truncated: false };
  }

  async getContractCode(address: string): Promise<Record<string, unknown>> {
    return this.get(`/contract/${encodeURIComponent(assertAddress(address))}/code`);
  }

  async getContractStorage(address: string, slot: string): Promise<Record<string, unknown>> {
    return this.get(
      `/contract/${encodeURIComponent(assertAddress(address))}/storage/${encodeURIComponent(slot)}`,
    );
  }

  async verifyContract(address: string, source: string): Promise<VerifyContractResponse> {
    return this.post('/contract/verify', { address: assertAddress(address), source }, false);
  }

  async getAiStats(): Promise<Record<string, unknown>> {
    return this.get('/ai/stats');
  }

  async submitTransaction(signedTx: string | object): Promise<SubmitResult> {
    const body = typeof signedTx === 'string' ? JSON.parse(signedTx) : signedTx;
    this.calls.submit.push(body as object);
    if (this.handlers.submitTransaction) return this.handlers.submitTransaction(body as object);
    const from = typeof (body as { from?: string }).from === 'string'
      ? assertAddress((body as { from: string }).from)
      : undefined;
    if (from) {
      const n = this.nonces.get(from) ?? 0;
      this.nonces.set(from, n + 1);
    }
    return { status: 'ok', hash: '0x' + 'ab'.repeat(32) };
  }

  async x402Verify(requirements: object, payload: object) {
    if (this.handlers.x402Verify) return this.handlers.x402Verify(requirements, payload);
    return { valid: true };
  }

  async x402Settle(requirements: object, payload: object, settlementTx: object) {
    if (this.handlers.x402Settle) {
      return this.handlers.x402Settle(requirements, payload, settlementTx);
    }
    return { success: true, tx_hash: '0x' + 'cd'.repeat(32) };
  }

  async waitForConfirmation(opts: ConfirmationOpts): Promise<ConfirmationResult> {
    if (this.handlers.waitForConfirmation) return this.handlers.waitForConfirmation(opts);
    const { nonce } = await this.getNonce(opts.from);
    return { confirmed: true, nonce: Math.max(nonce, opts.nonceBefore + 1) };
  }

  subscribe(
    _events: WsEventType | WsEventType[],
    _onEvent: (ev: WsEvent) => void,
    _onError?: (err: unknown) => void,
  ): Subscription {
    return { close: () => undefined };
  }
}
