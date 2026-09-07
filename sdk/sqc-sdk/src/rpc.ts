/**
 * Splendor QC RPC client (read + submit) with multi-URL retry / failover.
 */
import { BridgeError, wrapUnknown } from './errors.js';
import { assertAddress } from './address.js';
import { BRIDGE_CLIENT_VERSION } from './protocol.js';
import { CHAIN_ID } from './derive.js';
import { getDefaultNetwork } from './networks.js';
import { MODULE_ROUTER_ADDRESS } from './addresses/index.js';
import type { ModuleCall } from './core/module-call.js';
import { encodeModuleCallHex } from './core/module-call.js';
import { subscribe } from './core/subscribe.js';
import { fromUnits, toUnits } from './units.js';
import type {
  EstimateGasRequest,
  EstimateGasResponse,
  GetLogsResponse,
  LogFilter,
  Subscription,
  VerifyContractResponse,
  WsEvent,
  WsEventType,
} from './core/explorer-types.js';

export type RpcOptions = {
  /** Primary RPC URL (used if rpcUrls omitted). */
  rpcUrl?: string;
  /** Failover list for reads; first successful URL sticks for submits after accept. */
  rpcUrls?: string[];
  timeoutMs?: number;
  chainId?: string;
  /** Retries per URL on timeout / 5xx (default from network config). */
  retries?: number;
};

export class SqRpc {
  readonly rpcUrls: string[];
  readonly timeoutMs: number;
  readonly chainId: string;
  readonly retries: number;
  /** Sticky URL after a successful submit (`stickyOnSuccess`). */
  private stickyUrl: string | null = null;

  constructor(options: RpcOptions = {}) {
    const defaults = getDefaultNetwork();
    let list: string[];
    if (options.rpcUrls !== undefined) {
      list = options.rpcUrls
        .map((u) => String(u).trim().replace(/\/$/, ''))
        .filter(Boolean);
      if (!list.length) {
        throw BridgeError.rpc('rpcUrls must contain at least one non-empty URL');
      }
    } else if (options.rpcUrl !== undefined) {
      list = [options.rpcUrl.trim().replace(/\/$/, '')];
      if (!list[0]) {
        throw BridgeError.rpc('rpcUrl must be a non-empty URL');
      }
    } else {
      list = [...defaults.rpcUrls];
    }
    this.rpcUrls = list;
    this.timeoutMs = options.timeoutMs ?? defaults.timeoutMs;
    this.chainId = options.chainId || defaults.chainId || CHAIN_ID;
    this.retries = options.retries ?? defaults.retries;
  }

  get rpcUrl(): string {
    return this.stickyUrl || this.rpcUrls[0];
  }

  async getBalance(address: string): Promise<{
    address: string;
    balance: string;
    balance_spld: string;
    unit: string;
  }> {
    const canonical = assertAddress(address);
    const data = await this.get(`/balance/${encodeURIComponent(canonical)}`);
    return { ...data, balance_spld: toSPLD(data.balance) };
  }

  async getNonce(address: string): Promise<{ address: string; nonce: number }> {
    const canonical = assertAddress(address);
    return this.get(`/nonce/${encodeURIComponent(canonical)}`);
  }

  async getStatus(): Promise<Record<string, unknown>> {
    return this.get('/status');
  }

  /** Connected peer list (Splendor node `/peers`). */
  async getPeers(): Promise<unknown> {
    return this.get('/peers');
  }

  async assertChainId(): Promise<void> {
    const status = await this.getStatus();
    const remote = String(status.chain_id ?? '').trim();
    // Fail-closed: missing/empty chain_id must not pass (SQDP-1).
    if (!remote) {
      throw BridgeError.chainMismatch('(missing)', this.chainId);
    }
    if (remote !== this.chainId) {
      throw BridgeError.chainMismatch(remote, this.chainId);
    }
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

  async submitTransaction(signedTx: string | object): Promise<{ status: string; hash: string }> {
    const body = typeof signedTx === 'string' ? JSON.parse(signedTx) : signedTx;
    const remote = body?.chain_id != null ? String(body.chain_id).trim() : '';
    if (!remote) {
      throw BridgeError.chainMismatch('(missing)', this.chainId);
    }
    if (remote !== this.chainId) {
      throw BridgeError.chainMismatch(remote, this.chainId);
    }
    return this.post('/tx/submit', body, true);
  }

  /** `POST /verify/check` — stateless dual-signature check of an SQVL instruction. */
  async verifyCheck(instruction: object): Promise<{
    valid: boolean;
    reason?: string;
    chain_id?: string;
    digest?: string;
    signer?: string;
    instruction_id?: string;
    purpose?: string;
    origin?: string;
    settlement_hint?: string;
    settlement_action?: string;
  }> {
    return this.post('/verify/check', instruction, false);
  }

  /** `GET /verify/proof/{id}?instance=` — the on-chain record of one instruction. */
  async verifyProof(instructionId: string, instance: string): Promise<Record<string, unknown>> {
    const id = String(instructionId).replace(/^0x/i, '');
    const inst = assertAddress(instance);
    return this.get(`/verify/proof/${encodeURIComponent(id)}?instance=${encodeURIComponent(inst)}`);
  }

  /** `POST /verify/attest` — admit a signed `verify.attest` TxCall. */
  async verifyAttest(signedTx: object): Promise<{ status: string; hash: string }> {
    return this.post('/verify/attest', { tx: signedTx }, true);
  }

  /** `POST /verify/settle` — admit a signed `verify.spend` or `verify.finalize` TxCall. */
  async verifySettle(signedTx: object): Promise<{ status: string; hash: string }> {
    return this.post('/verify/settle', { tx: signedTx }, true);
  }

  async x402Verify(requirements: object, payload: object): Promise<{ valid: boolean; reason?: string }> {
    return this.post('/x402/verify', { requirements, payload }, false);
  }

  /**
   * Settle a verified X402 payment.
   *
   * settlementTx is the payer-signed TxX402 that actually moves the funds. The
   * node admits it to the mempool rather than mutating balances directly, so
   * the payment lands in a block and in the state root like any other
   * transaction. It is required: without it the node returns 400.
   */
  async x402Settle(
    requirements: object,
    payload: object,
    settlementTx: object,
  ): Promise<{
    success?: boolean;
    tx_hash?: string;
    error?: string;
  }> {
    return this.post('/x402/settle', { requirements, payload, settlement_tx: settlementTx }, true);
  }

  async getTx(hash: string): Promise<Record<string, unknown>> {
    const h = String(hash).replace(/^0x/i, '');
    return this.get(`/tx/${encodeURIComponent(h)}`);
  }

  async getTxReceipt(hash: string): Promise<Record<string, unknown>> {
    const h = String(hash).replace(/^0x/i, '');
    return this.get(`/tx/${encodeURIComponent(h)}/receipt`);
  }

  async getContractInfo(address: string): Promise<Record<string, unknown>> {
    const canonical = assertAddress(address);
    return this.get(`/contract/${encodeURIComponent(canonical)}`);
  }

  async getSyncStatus(): Promise<Record<string, unknown>> {
    return this.get('/sync');
  }

  /** Read-only view call (eth_call analog). Native module reads return `decoded`. */
  async call(opts: {
    to: string;
    data: string | Uint8Array;
    from?: string;
    encoding?: 'hex' | 'base64';
    value?: string;
  }): Promise<{
    return_data?: string;
    gas_used?: number;
    reverted?: boolean;
    revert_reason?: string;
    decoded?: unknown;
  }> {
    let data: string;
    if (opts.data instanceof Uint8Array) {
      data =
        '0x' +
        Array.from(opts.data)
          .map((b) => b.toString(16).padStart(2, '0'))
          .join('');
    } else if (opts.data.startsWith('0x')) {
      data = opts.data;
    } else {
      data = '0x' + opts.data;
    }
    return this.post(
      '/call',
      {
        to: assertAddress(opts.to),
        data,
        from: opts.from ? assertAddress(opts.from) : undefined,
        encoding: opts.encoding || 'hex',
        value: opts.value || '0',
      },
      false,
    );
  }

  /** Execute a native module read via POST /call to the module router. */
  async moduleCall<T>(envelope: ModuleCall): Promise<T> {
    const res = await this.call({
      to: MODULE_ROUTER_ADDRESS,
      data: encodeModuleCallHex(envelope),
    });
    if (res && typeof res === 'object' && (res as { reverted?: boolean }).reverted === true) {
      const err =
        (res as { error?: string }).error ||
        (res as { revert_reason?: string }).revert_reason ||
        'module call reverted';
      throw BridgeError.rpc(`moduleCall reverted: ${err}`, res);
    }
    if (res == null || typeof res !== 'object' || !('decoded' in (res as object))) {
      throw BridgeError.rpc('moduleCall: missing decoded result', res);
    }
    const decoded = (res as { decoded: unknown }).decoded;
    if (decoded === undefined || decoded === null) {
      throw BridgeError.rpc('moduleCall: decoded result is empty', res);
    }
    return decoded as T;
  }

  async estimateGas(opts: EstimateGasRequest): Promise<EstimateGasResponse> {
    let data: string | undefined;
    if (opts.data instanceof Uint8Array) {
      data =
        '0x' +
        Array.from(opts.data)
          .map((b) => b.toString(16).padStart(2, '0'))
          .join('');
    } else {
      data = opts.data;
    }
    return this.post(
      '/estimate',
      {
        type: opts.type,
        from: opts.from ? assertAddress(opts.from) : undefined,
        to: opts.to ? assertAddress(opts.to) : undefined,
        data,
        encoding: opts.encoding || 'hex',
        value: opts.value || '0',
      },
      false,
    );
  }

  async getLogs(filter: LogFilter = {}): Promise<GetLogsResponse> {
    const params = new URLSearchParams();
    if (filter.address) params.set('address', assertAddress(filter.address));
    if (filter.from_block != null) params.set('from', String(filter.from_block));
    if (filter.to_block != null) params.set('to', String(filter.to_block));
    if (filter.topics?.length) params.set('topics', filter.topics.join(','));
    const qs = params.toString();
    return this.get(`/logs${qs ? `?${qs}` : ''}`);
  }

  async getContractCode(address: string): Promise<Record<string, unknown>> {
    const canonical = assertAddress(address);
    return this.get(`/contract/${encodeURIComponent(canonical)}/code`);
  }

  async getContractStorage(address: string, slot: string): Promise<Record<string, unknown>> {
    const canonical = assertAddress(address);
    const slotEnc = encodeURIComponent(slot.replace(/^0x/i, ''));
    return this.get(`/contract/${encodeURIComponent(canonical)}/storage/${slotEnc}`);
  }

  async verifyContract(
    address: string,
    source: string,
  ): Promise<VerifyContractResponse> {
    return this.post('/contract/verify', { address: assertAddress(address), source }, false);
  }

  async getAiStats(): Promise<Record<string, unknown>> {
    return this.get('/ai/stats');
  }

  subscribe(
    events: WsEventType | WsEventType[],
    onEvent: (ev: WsEvent) => void,
    onError?: (err: unknown) => void,
  ): Subscription {
    return subscribe(this.rpcUrl, events, onEvent, onError);
  }

  /**
   * Confirmation: prefer receipt index when `hash` is set; fall back to nonce advance.
   * Receipt path requires status === 1 (success); status === 0 throws TX_FAILED.
   * Caveat (nonce-only path, no hash): another tx from the same account can advance the
   * nonce — weaker confirmation, use hash when available.
   */
  async waitForConfirmation(opts: {
    from: string;
    nonceBefore: number;
    timeoutMs?: number;
    pollMs?: number;
    /** When set, poll GET /tx/{hash}/receipt first. */
    hash?: string;
  }): Promise<{ confirmed: true; nonce: number; receipt?: Record<string, unknown> }> {
    const from = assertAddress(opts.from);
    const timeoutMs = opts.timeoutMs ?? 60_000;
    const pollMs = opts.pollMs ?? 1_000;
    const deadline = Date.now() + timeoutMs;
    const hash = opts.hash?.replace(/^0x/i, '');

    while (Date.now() < deadline) {
      if (hash) {
        try {
          const receipt = await this.getTxReceipt(hash);
          const statusRaw = receipt.status;
          const status =
            typeof statusRaw === 'number'
              ? statusRaw
              : typeof statusRaw === 'string' && statusRaw !== ''
                ? Number(statusRaw)
                : undefined;

          if (status === 0) {
            const errMsg =
              typeof receipt.error === 'string' && receipt.error
                ? receipt.error
                : 'transaction execution failed';
            throw BridgeError.txFailed(errMsg, receipt);
          }
          if (status === 1) {
            const { nonce } = await this.getNonce(from);
            return { confirmed: true, nonce, receipt };
          }
          // Receipt without a usable status — keep polling.
        } catch (e) {
          if (e instanceof BridgeError && e.code === 'TX_FAILED') throw e;
          /* not indexed yet — keep polling */
        }
      } else {
        const { nonce } = await this.getNonce(from);
        if (nonce > opts.nonceBefore) {
          return { confirmed: true, nonce };
        }
      }
      await new Promise((r) => setTimeout(r, pollMs));
    }
    throw BridgeError.txNotConfirmed(
      `confirmation for ${from} did not complete within ${timeoutMs}ms` +
        (hash ? ` (hash ${hash})` : ` (nonce still <= ${opts.nonceBefore})`),
    );
  }

  private orderedUrls(preferSticky: boolean): string[] {
    if (preferSticky && this.stickyUrl) {
      return [this.stickyUrl, ...this.rpcUrls.filter((u) => u !== this.stickyUrl)];
    }
    return [...this.rpcUrls];
  }

  private async get(path: string): Promise<any> {
    return this.request('GET', path, undefined, false);
  }

  private async post(path: string, body: unknown, stickyOnSuccess: boolean): Promise<any> {
    return this.request('POST', path, body, stickyOnSuccess);
  }

  /** Public GET for Transport adapters. */
  async transportGet<T>(path: string): Promise<T> {
    return this.get(path) as Promise<T>;
  }

  /** Public POST for Transport adapters. */
  async transportPost<T>(path: string, body: unknown, stickyOnSuccess = false): Promise<T> {
    return this.post(path, body, stickyOnSuccess) as Promise<T>;
  }

  private async request(
    method: 'GET' | 'POST',
    path: string,
    body: unknown | undefined,
    stickyOnSuccess: boolean,
  ): Promise<any> {
    const urls = this.orderedUrls(stickyOnSuccess);
    let lastError: unknown;

    for (const base of urls) {
      for (let attempt = 0; attempt <= this.retries; attempt++) {
        try {
          const init: RequestInit = {
            method,
            headers: {
              Accept: 'application/json',
              'X-SQC-Bridge': BRIDGE_CLIENT_VERSION,
              ...(method === 'POST'
                ? { 'Content-Type': 'application/json' }
                : {}),
            },
          };
          if (method === 'POST') init.body = JSON.stringify(body);

          const res = await fetchWithTimeout(base + path, init, this.timeoutMs);

          if (res.status >= 500 && attempt < this.retries) {
            await sleep(100 * (attempt + 1));
            continue;
          }

          const json = await parseResponse(res, path);
          // Sticky only after successful submit — not on reads.
          if (stickyOnSuccess) {
            this.stickyUrl = base;
          }
          return json;
        } catch (e) {
          lastError = e;
          if (e instanceof BridgeError && e.code === 'RPC') {
            const status = rpcHttpStatus(e);
            // 4xx: fail closed — do not retry or failover.
            if (status !== undefined && status >= 400 && status < 500) {
              throw e;
            }
            // 5xx: retry same URL, then failover.
            if (status !== undefined && status >= 500) {
              if (attempt < this.retries) {
                await sleep(100 * (attempt + 1));
                continue;
              }
              break;
            }
            // Other RPC (no status): do not failover.
            throw e;
          }
          const retryable =
            e instanceof BridgeError &&
            (e.code === 'TIMEOUT' || e.code === 'NETWORK');
          if (retryable && attempt < this.retries) {
            await sleep(100 * (attempt + 1));
            continue;
          }
          if (retryable) {
            break; // try next URL
          }
          throw wrapUnknown(e, 'RPC');
        }
      }
    }
    throw wrapUnknown(lastError, 'RPC');
  }
}

export function toSPLD(units: string | number | bigint): string {
  // Display helper: up to 6 fraction digits (same trimming rules as pre-harden).
  const full = toUnits(units, 18);
  const [whole, frac = '0'] = full.split('.');
  const frac18 = frac.padEnd(18, '0').slice(0, 18);
  const fracStr = frac18.replace(/0+$/, '') || '0';
  return `${whole}.${fracStr.slice(0, 6)}`;
}

export function fromSPLD(spld: string | number): string {
  return fromUnits(spld, 18);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function fetchWithTimeout(
  url: string,
  options: RequestInit,
  timeoutMs: number,
): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') {
      throw BridgeError.timeout(`RPC request timed out after ${timeoutMs}ms: ${url}`);
    }
    throw BridgeError.network(e instanceof Error ? e.message : String(e));
  } finally {
    clearTimeout(timer);
  }
}

function rpcHttpStatus(e: BridgeError): number | undefined {
  const d = e.details;
  if (d && typeof d === 'object' && d !== null && 'status' in d) {
    const s = (d as { status: unknown }).status;
    if (typeof s === 'number') return s;
  }
  return undefined;
}

async function parseResponse(res: Response, path: string): Promise<any> {
  const text = await res.text();
  let json: any;
  try {
    json = JSON.parse(text);
  } catch {
    throw BridgeError.rpc(
      `SQC RPC ${path}: non-JSON (${res.status}): ${text.slice(0, 200)}`,
      { status: res.status },
    );
  }
  if (!res.ok) {
    const details =
      json && typeof json === 'object'
        ? { ...json, status: res.status }
        : { status: res.status, body: json };
    throw BridgeError.rpc(
      `SQC RPC ${path}: ${res.status} ${json.error || text.slice(0, 200)}`,
      details,
    );
  }
  return json;
}
