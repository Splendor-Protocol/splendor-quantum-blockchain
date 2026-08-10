/**
 * Splendor QC RPC client (read + submit) with multi-URL retry / failover.
 */
import { BridgeError, wrapUnknown } from './errors.js';
import { assertAddress } from './address.js';
import { BRIDGE_CLIENT_VERSION } from './protocol.js';
import { CHAIN_ID } from './derive.js';
import { getDefaultNetwork } from './networks.js';

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

  async x402Verify(requirements: object, payload: object): Promise<{ valid: boolean; reason?: string }> {
    return this.post('/x402/verify', { requirements, payload }, false);
  }

  async x402Settle(requirements: object, payload: object): Promise<{
    success?: boolean;
    tx_hash?: string;
    error?: string;
  }> {
    return this.post('/x402/settle', { requirements, payload }, true);
  }

  /**
   * Best-effort confirmation: wait until account nonce > nonceBefore.
   * Node tx-by-hash is not implemented — `hash` only appears in timeout errors
   * for operator correlation (does not prove inclusion).
   * Caveat: another tx from the same account can advance the nonce.
   */
  async waitForConfirmation(opts: {
    from: string;
    nonceBefore: number;
    timeoutMs?: number;
    pollMs?: number;
    /** Correlates timeout errors only; not used for receipt lookup. */
    hash?: string;
  }): Promise<{ confirmed: true; nonce: number }> {
    const from = assertAddress(opts.from);
    const timeoutMs = opts.timeoutMs ?? 60_000;
    const pollMs = opts.pollMs ?? 1_000;
    const deadline = Date.now() + timeoutMs;

    while (Date.now() < deadline) {
      const { nonce } = await this.getNonce(from);
      if (nonce > opts.nonceBefore) {
        return { confirmed: true, nonce };
      }
      await new Promise((r) => setTimeout(r, pollMs));
    }
    throw BridgeError.txNotConfirmed(
      `nonce for ${from} did not advance past ${opts.nonceBefore} within ${timeoutMs}ms` +
        (opts.hash ? ` (submitted hash ${opts.hash}; receipt index not available)` : ''),
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

const SPLD_DIVISOR = 10n ** 18n;

export function toSPLD(units: string | number | bigint): string {
  try {
    const u = BigInt(units);
    const whole = u / SPLD_DIVISOR;
    const frac = u % SPLD_DIVISOR;
    const fracStr = frac.toString().padStart(18, '0').replace(/0+$/, '') || '0';
    return `${whole}.${fracStr.slice(0, 6)}`;
  } catch {
    return '0.000000';
  }
}

export function fromSPLD(spld: string | number): string {
  try {
    const parts = String(spld).split('.');
    const whole = BigInt(parts[0] || '0');
    const fracStr = (parts[1] || '').padEnd(18, '0').slice(0, 18);
    const frac = BigInt(fracStr);
    return String(whole * SPLD_DIVISOR + frac);
  } catch {
    return '0';
  }
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
