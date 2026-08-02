/**
 * Splendor QC RPC client (read + submit). Addresses use SQ: prefix in URL paths.
 */

export type RpcOptions = {
  rpcUrl?: string;
  timeoutMs?: number;
  chainId?: string;
};

export class SqRpc {
  readonly rpcUrl: string;
  readonly timeoutMs: number;
  readonly chainId: string;

  constructor(options: RpcOptions = {}) {
    this.rpcUrl = (options.rpcUrl || 'http://localhost:8080').replace(/\/$/, '');
    this.timeoutMs = options.timeoutMs ?? 15_000;
    this.chainId = options.chainId || 'splendor-quantum-1';
  }

  async getBalance(address: string): Promise<{
    address: string;
    balance: string;
    balance_spld: string;
    unit: string;
  }> {
    const data = await this.get(`/balance/${encodeURIComponent(address)}`);
    return { ...data, balance_spld: toSPLD(data.balance) };
  }

  async getNonce(address: string): Promise<{ address: string; nonce: number }> {
    return this.get(`/nonce/${encodeURIComponent(address)}`);
  }

  async getStatus(): Promise<Record<string, unknown>> {
    return this.get('/status');
  }

  async getLatestBlock(): Promise<unknown> {
    return this.get('/block/latest');
  }

  async getBlockByHeight(height: number | string): Promise<unknown> {
    return this.get(`/block/${height}`);
  }

  /**
   * Submit a wire-format signed transaction (already JSON-serialized by Go signer).
   * Pass either a JSON string or a parsed object.
   */
  async submitTransaction(signedTx: string | object): Promise<{ status: string; hash: string }> {
    const body = typeof signedTx === 'string' ? JSON.parse(signedTx) : signedTx;
    return this.post('/tx/submit', body);
  }

  async x402Verify(requirements: object, payload: object): Promise<{ valid: boolean; reason?: string }> {
    return this.post('/x402/verify', { requirements, payload });
  }

  async x402Settle(requirements: object, payload: object): Promise<{
    success?: boolean;
    tx_hash?: string;
    error?: string;
  }> {
    return this.post('/x402/settle', { requirements, payload });
  }

  private async get(path: string): Promise<any> {
    const res = await fetchWithTimeout(
      this.rpcUrl + path,
      { method: 'GET', headers: { Accept: 'application/json', 'X-SQC-Bridge': '1.0.0' } },
      this.timeoutMs,
    );
    return parseResponse(res, path);
  }

  private async post(path: string, body: unknown): Promise<any> {
    const res = await fetchWithTimeout(
      this.rpcUrl + path,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Accept: 'application/json',
          'X-SQC-Bridge': '1.0.0',
        },
        body: JSON.stringify(body),
      },
      this.timeoutMs,
    );
    return parseResponse(res, path);
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

async function fetchWithTimeout(
  url: string,
  options: RequestInit,
  timeoutMs: number,
): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

async function parseResponse(res: Response, path: string): Promise<any> {
  const text = await res.text();
  let json: any;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error(`SQC RPC ${path}: non-JSON (${res.status}): ${text.slice(0, 200)}`);
  }
  if (!res.ok) {
    throw new Error(`SQC RPC ${path}: ${res.status} ${json.error || text.slice(0, 200)}`);
  }
  return json;
}
