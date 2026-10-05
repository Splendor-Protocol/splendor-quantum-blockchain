/**
 * Splendor QC RPC client (read + submit) with multi-URL retry / failover.
 *
 * @deprecated Prefer {@link Connection} / {@link Transport}. SqRpc is the private
 * adapter behind RpcTransport — do not construct it in application code.
 */
import { BridgeError, wrapUnknown } from './errors.js';
import { assertAddress } from './address.js';
import { BRIDGE_CLIENT_VERSION } from './protocol.js';
import { CHAIN_ID } from './derive.js';
import { getDefaultNetwork, isLoopbackOrLocalhostUrl } from './networks.js';
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
  /**
   * Allow link-local / cloud-metadata hosts (Node only). Default false.
   * Browser CORS still applies client-side.
   */
  allowPrivateRpcHosts?: boolean;
  /** Allow plain `http://` to non-loopback hosts. Default false. */
  allowInsecureHttp?: boolean;
};

/** Origin only — drops credentials, path and query (which may carry API keys) from error details. */
export function redactRpcUrl(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return '[invalid url]';
  }
}

/**
 * IPv4 octets for a dotted host or an IPv4-mapped / compatible IPv6 host
 * (`::ffff:169.254.169.254`, which WHATWG URL normalizes to `::ffff:a9fe:a9fe`).
 */
function ipv4Octets(host: string): number[] | null {
  const dotted = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (dotted) return dotted.slice(1).map(Number);
  const mappedDotted = /^::(?:ffff:)?(\d{1,3}(?:\.\d{1,3}){3})$/.exec(host);
  if (mappedDotted) return ipv4Octets(mappedDotted[1]!);
  const mappedHex = /^::(?:ffff:)?([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(host);
  if (mappedHex) {
    const hi = parseInt(mappedHex[1]!, 16);
    const lo = parseInt(mappedHex[2]!, 16);
    return [hi >> 8, hi & 0xff, lo >> 8, lo & 0xff];
  }
  return null;
}

const METADATA_HOSTS = new Set([
  'metadata',
  'metadata.google.internal',
  'metadata.aws.internal',
  'instance-data',
  'fd00:ec2::254',
]);

/** Block SSRF-prone targets in Node (cloud metadata, link-local, file:). Localhost is allowed for local nodes. */
export function assertSafeRpcUrl(url: string, opts?: { allowPrivateRpcHosts?: boolean }): void {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    throw BridgeError.rpc(`Invalid RPC URL: ${redactRpcUrl(url)}`);
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    throw BridgeError.rpc(`RPC URL scheme blocked (${u.protocol})`);
  }
  const host = u.hostname.toLowerCase().replace(/^\[|\]$/g, '');
  if (opts?.allowPrivateRpcHosts) return;
  const blocked = () => BridgeError.rpc(`RPC host blocked (SSRF/metadata): ${host}`);
  if (METADATA_HOSTS.has(host) || host.endsWith('.internal') || /^0x[0-9a-f]+$/i.test(host)) {
    throw blocked();
  }
  const v4 = ipv4Octets(host);
  if (v4) {
    const [a, b, c, d] = v4;
    // Link-local (AWS/GCP/Azure metadata live here) and Alibaba Cloud metadata.
    if ((a === 169 && b === 254) || (a === 100 && b === 100 && c === 100 && d === 200)) throw blocked();
    return;
  }
  // IPv6 link-local fe80::/10.
  if (/^fe[89ab][0-9a-f]:/.test(host)) throw blocked();
}

export class SqRpc {
  readonly rpcUrls: string[];
  readonly timeoutMs: number;
  readonly chainId: string;
  readonly retries: number;
  /** Sticky URL after a successful submit (`stickyOnSuccess`). */
  private stickyUrl: string | null = null;
  /** Consecutive failovers that started on sticky — clear sticky after threshold. */
  private stickyFailStreak = 0;
  /** Coalesce identical in-flight GETs. */
  private inflight = new Map<string, Promise<any>>();

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
    const isNode =
      typeof process !== 'undefined' &&
      process.versions != null &&
      process.versions.node != null;
    if (isNode) {
      for (const u of list) assertSafeRpcUrl(u, options);
    }
    if (!options.allowInsecureHttp) {
      for (const u of list) {
        if (u.startsWith('https://') || isLoopbackOrLocalhostUrl(u)) continue;
        throw BridgeError.rpc(
          `RPC URL must use https:// for non-loopback hosts (${redactRpcUrl(u)}). Pass allowInsecureHttp: true to override.`,
        );
      }
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

  async getNonce(
    address: string,
    timeoutMs?: number,
  ): Promise<{ address: string; nonce: number }> {
    const canonical = assertAddress(address);
    return this.get(`/nonce/${encodeURIComponent(canonical)}`, timeoutMs);
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
    const h = typeof height === 'number' ? (Number.isSafeInteger(height) && height >= 0 ? String(height) : '') : String(height).trim();
    if (!/^(0|[1-9]\d*)$/.test(h)) throw BridgeError.invalidParams('block height must be a non-negative integer');
    return this.get(`/block/${h}`);
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

  async verifyCheck(instruction: object): Promise<{
    valid: boolean;
    reason?: string;
    digest?: string;
    signer?: string;
    instruction_id?: string;
    origin?: string;
    settlement_hint?: string;
    action?: string;
  }> {
    return this.post('/verify/check', instruction, false);
  }

  async verifyProof(instructionId: string, instance: string): Promise<Record<string, unknown>> {
    const id = String(instructionId).replace(/^0x/i, '');
    return this.get(
      `/verify/proof/${encodeURIComponent(id)}?instance=${encodeURIComponent(instance)}`,
    );
  }

  async verifyAttest(signedTx: object): Promise<{ status: string; hash: string }> {
    return this.post('/verify/attest', { tx: signedTx }, true);
  }

  async verifySettle(signedTx: object): Promise<{ status: string; hash: string }> {
    return this.post('/verify/settle', { tx: signedTx }, true);
  }

  async listValidators(): Promise<{ validators: unknown[]; count: number; bond_spld: number }> {
    return this.get('/validators');
  }

  async getValidator(address: string): Promise<Record<string, unknown>> {
    return this.get(`/validators/${encodeURIComponent(address)}`);
  }

  async x402Verify(
    requirements: object,
    payload: object,
  ): Promise<{ valid: boolean; reason?: string; nonce_used?: boolean }> {
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
    status?: 'submitted' | 'confirmed' | 'failed' | string;
    tx_hash?: string;
    error?: string;
  }> {
    return this.post('/x402/settle', { requirements, payload, settlement_tx: settlementTx }, true);
  }

  async getTx(hash: string): Promise<Record<string, unknown>> {
    const h = String(hash).replace(/^0x/i, '');
    return this.get(`/tx/${encodeURIComponent(h)}`);
  }

  async getTxReceipt(hash: string, timeoutMs?: number): Promise<Record<string, unknown>> {
    const h = String(hash).replace(/^0x/i, '');
    return this.get(`/tx/${encodeURIComponent(h)}/receipt`, timeoutMs);
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
    const urls = this.orderedUrls();
    return subscribe(urls, events, onEvent, onError);
  }

  /** Soft readiness probe — assert chain id against any healthy RPC. */
  async probeNetwork(): Promise<{ ok: true; chainId: string } | { ok: false; error: BridgeError }> {
    try {
      await this.assertChainId();
      return { ok: true, chainId: this.chainId };
    } catch (e) {
      const err = wrapUnknown(e, 'RPC');
      return { ok: false, error: err };
    }
  }

  /**
   * Confirmation: prefer receipt index when `hash` is set; fall back to nonce advance.
   * Receipt path requires status === 1 (success); status === 0 throws TX_FAILED.
   * Transport failures (NETWORK/TIMEOUT/RPC_UNAVAILABLE) fail fast after a few consecutive polls —
   * they are not treated as “not indexed yet”.
   * Caveat (nonce-only path): another tx from the same account can advance the nonce.
   */
  async waitForConfirmation(opts: {
    from: string;
    nonceBefore: number;
    timeoutMs?: number;
    pollMs?: number;
    /** When set, poll GET /tx/{hash}/receipt first. */
    hash?: string;
    /** With `hash`: only a successful receipt confirms (no nonce-advance fallback). */
    requireReceipt?: boolean;
  }): Promise<{ confirmed: true; nonce: number; receipt?: Record<string, unknown> }> {
    const from = assertAddress(opts.from);
    const timeoutMs = opts.timeoutMs ?? 60_000;
    const pollMs = opts.pollMs ?? 1_000;
    const deadline = Date.now() + timeoutMs;
    const hash = opts.hash?.replace(/^0x/i, '');
    /** Cap each poll RPC so confirm wall-clock stays near timeoutMs. */
    const pollRequestTimeoutMs = Math.min(5_000, Math.max(1_500, Math.floor(timeoutMs / 12)));
    const MAX_INFRA = 3;
    const NONCE_FALLBACK_AFTER = 5;
    let consecutiveInfra = 0;
    let receiptMissingStreak = 0;

    const isInfra = (e: unknown): boolean => {
      if (!(e instanceof BridgeError)) return false;
      return (
        e.code === 'NETWORK' ||
        e.code === 'TIMEOUT' ||
        e.code === 'RPC_UNAVAILABLE' ||
        (e.code === 'RPC' && rpcHttpStatus(e) !== undefined && rpcHttpStatus(e)! >= 500)
      );
    };

    const isNotFound = (e: unknown): boolean => {
      if (!(e instanceof BridgeError) || e.code !== 'RPC') return false;
      const status = rpcHttpStatus(e);
      return status === 404;
    };

    while (Date.now() < deadline) {
      const remaining = deadline - Date.now();
      if (remaining <= 0) break;
      const attemptTimeout = Math.min(pollRequestTimeoutMs, remaining);

      if (hash) {
        try {
          const receipt = await this.getTxReceipt(hash, attemptTimeout);
          consecutiveInfra = 0;
          const statusRaw = receipt.status;
          let status: number | undefined;
          if (typeof statusRaw === 'number') {
            status = statusRaw;
          } else if (typeof statusRaw === 'string' && statusRaw !== '') {
            const lower = statusRaw.toLowerCase();
            if (lower === 'failed' || lower === 'failure' || lower === 'error' || lower === 'reverted') {
              status = 0;
            } else if (lower === 'success' || lower === 'ok' || lower === 'confirmed') {
              status = 1;
            } else {
              const n = Number(statusRaw);
              status = Number.isFinite(n) ? n : undefined;
            }
          }

          if (status === 0) {
            const errMsg =
              typeof receipt.error === 'string' && receipt.error
                ? receipt.error
                : 'transaction execution failed';
            throw BridgeError.txFailed(errMsg, receipt);
          }
          if (status === 1) {
            const { nonce } = await this.getNonce(from, attemptTimeout);
            return { confirmed: true, nonce, receipt };
          }
          receiptMissingStreak += 1;
        } catch (e) {
          if (e instanceof BridgeError && e.code === 'TX_FAILED') throw e;
          if (isInfra(e)) {
            consecutiveInfra += 1;
            if (consecutiveInfra >= MAX_INFRA) {
              throw BridgeError.rpcUnavailable(
                `RPC unavailable while confirming ${hash}: ${e instanceof Error ? e.message : String(e)}`,
                { cause: e, hash },
              );
            }
          } else if (isNotFound(e)) {
            consecutiveInfra = 0;
            receiptMissingStreak += 1;
          } else {
            // Other RPC (e.g. malformed) — treat as not indexed yet.
            consecutiveInfra = 0;
            receiptMissingStreak += 1;
          }
        }

        if (!opts.requireReceipt && receiptMissingStreak >= NONCE_FALLBACK_AFTER) {
          try {
            const { nonce } = await this.getNonce(from, attemptTimeout);
            consecutiveInfra = 0;
            if (nonce > opts.nonceBefore) {
              return { confirmed: true, nonce };
            }
          } catch (e) {
            if (isInfra(e)) {
              consecutiveInfra += 1;
              if (consecutiveInfra >= MAX_INFRA) {
                throw BridgeError.rpcUnavailable(
                  `RPC unavailable while confirming (nonce fallback): ${e instanceof Error ? e.message : String(e)}`,
                  { cause: e, hash },
                );
              }
            }
          }
        }
      } else {
        try {
          const { nonce } = await this.getNonce(from, attemptTimeout);
          consecutiveInfra = 0;
          if (nonce > opts.nonceBefore) {
            return { confirmed: true, nonce };
          }
        } catch (e) {
          if (isInfra(e)) {
            consecutiveInfra += 1;
            if (consecutiveInfra >= MAX_INFRA) {
              throw BridgeError.rpcUnavailable(
                `RPC unavailable while confirming: ${e instanceof Error ? e.message : String(e)}`,
                { cause: e },
              );
            }
          } else {
            throw wrapUnknown(e, 'RPC');
          }
        }
      }

      const sleepMs = Math.min(pollMs, Math.max(0, deadline - Date.now()));
      if (sleepMs <= 0) break;
      await new Promise((r) => setTimeout(r, sleepMs));
    }
    throw BridgeError.txNotConfirmed(
      `confirmation for ${from} did not complete within ${timeoutMs}ms` +
        (hash ? ` (hash ${hash})` : ` (nonce still <= ${opts.nonceBefore})`),
    );
  }

  private orderedUrls(): string[] {
    if (this.stickyUrl) {
      return [this.stickyUrl, ...this.rpcUrls.filter((u) => u !== this.stickyUrl)];
    }
    return [...this.rpcUrls];
  }

  private async get(path: string, timeoutMs?: number): Promise<any> {
    if (timeoutMs != null) {
      return this.request('GET', path, undefined, false, timeoutMs);
    }
    const key = `GET:${path}`;
    const existing = this.inflight.get(key);
    if (existing) return existing;
    const p = this.request('GET', path, undefined, false).finally(() => {
      this.inflight.delete(key);
    });
    this.inflight.set(key, p);
    return p;
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
    timeoutOverrideMs?: number,
  ): Promise<any> {
    const urls = this.orderedUrls();
    let lastError: unknown;
    const timeoutMs = timeoutOverrideMs ?? this.timeoutMs;
    const stickyAtStart = this.stickyUrl;

    for (const base of urls) {
      for (let attempt = 0; attempt <= this.retries; attempt++) {
        try {
          const init: RequestInit = {
            method,
            // A redirect could bounce a vetted RPC URL to a metadata or internal host.
            redirect: 'error',
            headers: {
              Accept: 'application/json',
              'X-SQC-Bridge': BRIDGE_CLIENT_VERSION,
              ...(method === 'POST'
                ? { 'Content-Type': 'application/json' }
                : {}),
            },
          };
          if (method === 'POST') init.body = JSON.stringify(body);

          const res = await fetchWithTimeout(base + path, init, timeoutMs);

          if (res.status >= 500 && attempt < this.retries) {
            await sleep(100 * (attempt + 1));
            continue;
          }

          const json = await parseResponse(res, path);
          if (stickyOnSuccess) {
            this.stickyUrl = base;
            this.stickyFailStreak = 0;
          } else if (!stickyAtStart || base === stickyAtStart) {
            // Success on sticky (or no sticky) clears the fail streak.
            // Success after failover away from sticky must NOT reset the streak —
            // three consecutive sticky failovers clear sticky preference.
            this.stickyFailStreak = 0;
          }
          return json;
        } catch (e) {
          lastError = e;
          if (e instanceof BridgeError && e.code === 'RPC') {
            const status = rpcHttpStatus(e);
            if (status !== undefined && status >= 400 && status < 500) {
              throw e;
            }
            if (status !== undefined && status >= 500) {
              if (attempt < this.retries) {
                await sleep(100 * (attempt + 1));
                continue;
              }
              break;
            }
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
            break;
          }
          throw wrapUnknown(e, 'RPC');
        }
      }
      // Failed over away from sticky — track streak.
      if (stickyAtStart && base === stickyAtStart) {
        this.stickyFailStreak += 1;
        if (this.stickyFailStreak >= 3) {
          this.stickyUrl = null;
          this.stickyFailStreak = 0;
        }
      }
    }
    throw BridgeError.rpcUnavailable(
      `All RPC endpoints failed for ${method} ${path}` +
        (lastError instanceof Error ? `: ${lastError.message}` : ''),
      { tried: urls.map(redactRpcUrl), cause: lastError },
    );
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
      throw BridgeError.timeout(`RPC request timed out after ${timeoutMs}ms`);
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
    throw BridgeError.rpc(`SQC RPC ${path} failed (${res.status})`, {
      status: res.status,
    });
  }
  if (!res.ok) {
    const short =
      json && typeof json === 'object' && typeof json.error === 'string'
        ? String(json.error).slice(0, 120)
        : undefined;
    throw BridgeError.rpc(
      short ? `SQC RPC ${path}: ${res.status} ${short}` : `SQC RPC ${path} failed (${res.status})`,
      { status: res.status },
    );
  }
  return json;
}
