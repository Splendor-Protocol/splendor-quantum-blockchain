/**
 * quantum-migration — the Splendor Quantum Migration dapp.
 *
 * One Worker serves the page (GET /) and a read-only API:
 *   GET /api/info            — network + contract addresses + bridge stats
 *   GET /api/status/{txhash} — burn confirmations + decoded Burned events +
 *                              quantum-side claim state
 *   GET /health
 *
 * The Worker holds no keys and submits nothing, ever. Burns are signed in the
 * user's own wallet against the legacy chain; claims are submitted by the
 * bridge relayer. This service only READS: the legacy chain over JSON-RPC
 * (receipt + head) and the quantum node over its REST API (bridge info +
 * per-burn claim slots).
 */
import { appConfig, type AppConfig, type Env } from './config';
import { checkStatusCooldown } from './ratelimit';
import { BURNED_TOPIC0 } from './selectors';
import { renderPage } from './page';

const MAX_BODY_BYTES = 4096;
const UPSTREAM_TIMEOUT_MS = 8000;
const STATS_CACHE_MS = 10_000;
/** Decode at most this many Burned events out of one receipt. */
const MAX_BURNS = 4;

const TX_HASH = /^0x[0-9a-f]{64}$/;

function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
      ...headers,
    },
  });
}

/**
 * CSP: the page runs exactly one inline script (nonce-gated) and talks to
 * exactly two things — this origin (`/api/*`) and the injected wallet
 * provider, which is an extension API and needs no connect-src entry. No
 * external scripts, fonts, styles or images of any kind.
 */
function securityHeaders(nonce: string): Record<string, string> {
  return {
    'content-security-policy': [
      "default-src 'self'",
      `script-src 'nonce-${nonce}'`,
      "style-src 'unsafe-inline'",
      "img-src 'self' data:",
      "connect-src 'self'",
      "form-action 'none'",
      "base-uri 'none'",
      "frame-ancestors 'none'",
    ].join('; '),
    'referrer-policy': 'strict-origin-when-cross-origin',
    'x-content-type-options': 'nosniff',
    'x-frame-options': 'DENY',
    'permissions-policy': 'geolocation=(), microphone=(), camera=()',
  };
}

function makeNonce(): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return btoa(String.fromCharCode(...b)).replace(/=+$/, '');
}

/**
 * Every route here is a GET, but a public endpoint still must not buffer an
 * arbitrary POST body. The declared content-length is rejected up front and —
 * because a header can lie or be absent (chunked upload) — any actual stream
 * is cut the moment it exceeds the cap. Same defence as the faucet's bounded
 * body reader, applied globally since no route wants a body at all.
 */
async function rejectOversizedBody(request: Request): Promise<Response | null> {
  const declared = request.headers.get('content-length');
  if (declared !== null) {
    const n = Number(declared);
    if (!Number.isFinite(n) || n < 0 || n > MAX_BODY_BYTES) {
      return json({ ok: false, code: 'bad-request', error: 'Request body too large.' }, 413);
    }
  }
  const stream = request.body;
  if (!stream) return null;
  const reader = stream.getReader();
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;
    total += value.byteLength;
    if (total > MAX_BODY_BYTES) {
      await reader.cancel().catch(() => {});
      return json({ ok: false, code: 'bad-request', error: 'Request body too large.' }, 413);
    }
  }
  return null;
}

/** Bounded fetch: aborts after UPSTREAM_TIMEOUT_MS, never throws, null on any failure. */
async function fetchJson(url: string, init?: RequestInit): Promise<unknown | null> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), UPSTREAM_TIMEOUT_MS);
  try {
    const res = await fetch(url, { ...init, signal: ctrl.signal });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

// ── quantum bridge REST ──────────────────────────────────────────────────────

interface BridgeStats {
  poolBalance: string;
  claimedSpld: string;
  claimedSplv: string;
  maxClaimAmount: string;
  paused: boolean;
}

/**
 * 10-second module-scope cache for GET /bridge/{addr}. Every page load and
 * every status poll wants these numbers; one isolate asking the node at most
 * every 10s is plenty fresh for a metrics strip. Failures are cached too, so
 * a down node is asked once per window, not once per visitor.
 */
let statsCache: { key: string; atMs: number; stats: BridgeStats | null } | null = null;

/** Test hook: clears the module-scope stats cache. */
export function _resetStatsCache(): void {
  statsCache = null;
}

function asDecimalString(v: unknown): string | null {
  if (typeof v === 'string' && /^[0-9]+$/.test(v)) return v;
  if (typeof v === 'number' && Number.isSafeInteger(v) && v >= 0) return String(v);
  return null;
}

async function fetchBridgeStats(cfg: AppConfig, nowMs: number): Promise<BridgeStats | null> {
  const key = `${cfg.quantumRpcBase}/bridge/${cfg.quantumBridgeAddress}`;
  if (statsCache && statsCache.key === key && nowMs - statsCache.atMs < STATS_CACHE_MS) {
    return statsCache.stats;
  }

  const raw = await fetchJson(key, { headers: { accept: 'application/json' } });
  let stats: BridgeStats | null = null;
  // The node wraps every module read as {module, action, info: {...}}.
  const info =
    raw !== null && typeof raw === 'object' && !Array.isArray(raw)
      ? (raw as { info?: unknown }).info
      : null;
  if (info !== null && typeof info === 'object' && !Array.isArray(info)) {
    const o = info as Record<string, unknown>;
    const poolBalance = asDecimalString(o.pool_balance);
    const claimedSpld = asDecimalString(o.claimed_total_spld);
    const claimedSplv = asDecimalString(o.claimed_total_splv);
    const maxClaimAmount = asDecimalString(o.max_claim_amount);
    if (poolBalance !== null && claimedSpld !== null && claimedSplv !== null && maxClaimAmount !== null) {
      stats = { poolBalance, claimedSpld, claimedSplv, maxClaimAmount, paused: o.paused === true };
    }
  }
  statsCache = { key, atMs: nowMs, stats };
  return stats;
}

// ── legacy-chain JSON-RPC ────────────────────────────────────────────────────

class RpcError extends Error {}

let rpcId = 0;
async function evmRpc(rpcUrl: string, method: string, params: unknown[]): Promise<unknown> {
  const raw = await fetchJson(rpcUrl, {
    method: 'POST',
    headers: { 'content-type': 'application/json', accept: 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: ++rpcId, method, params }),
  });
  if (raw === null || typeof raw !== 'object') {
    throw new RpcError(`${method}: node unreachable or non-JSON`);
  }
  const o = raw as { error?: { message?: string }; result?: unknown };
  if (o.error) throw new RpcError(`${method}: ${o.error.message ?? 'rpc error'}`);
  if (!('result' in o)) throw new RpcError(`${method}: malformed response`);
  return o.result;
}

const QUANTITY = /^0x[0-9a-fA-F]{1,64}$/;

function hexToBigInt(v: unknown): bigint | null {
  if (typeof v !== 'string' || !QUANTITY.test(v)) return null;
  return BigInt(v);
}

interface DecodedBurn {
  /** Decimal string — the monotonically increasing on-chain burn counter. */
  id: string;
  asset: 'spld' | 'splv' | 'unknown';
  /** Smallest-unit decimal string. */
  amount: string;
  /** The 32 recipient bytes, 0x-prefixed (as emitted). */
  quantumRecipient: string;
  /** The same bytes in the wallet's SQ: presentation. */
  recipientSq: string;
}

/**
 * Pull Burned events out of a receipt: only logs emitted BY the burn contract
 * whose topic0 matches. `topics = [topic0, id, sender, asset]`; the data word
 * layout is `amount ‖ quantumRecipient` (both 32 bytes).
 */
function decodeBurns(receipt: Record<string, unknown>, burnContract: string): DecodedBurn[] {
  const logs = Array.isArray(receipt.logs) ? receipt.logs : [];
  const burns: DecodedBurn[] = [];
  for (const entry of logs) {
    if (burns.length >= MAX_BURNS) break;
    if (entry === null || typeof entry !== 'object') continue;
    const log = entry as { address?: unknown; topics?: unknown; data?: unknown };
    if (typeof log.address !== 'string' || log.address.toLowerCase() !== burnContract) continue;
    const topics = Array.isArray(log.topics) ? log.topics : [];
    if (topics.length !== 4 || topics[0] !== BURNED_TOPIC0) continue;

    const id = hexToBigInt(topics[1]);
    const assetN = hexToBigInt(topics[3]);
    const data = typeof log.data === 'string' ? log.data.replace(/^0x/, '') : '';
    if (id === null || assetN === null || data.length < 128 || !/^[0-9a-fA-F]+$/.test(data)) {
      continue;
    }
    const amount = BigInt('0x' + data.slice(0, 64));
    const recipientHex = data.slice(64, 128).toLowerCase();
    burns.push({
      id: id.toString(),
      asset: assetN === 0n ? 'spld' : assetN === 1n ? 'splv' : 'unknown',
      amount: amount.toString(),
      quantumRecipient: '0x' + recipientHex,
      recipientSq: 'SQ:' + recipientHex,
    });
  }
  return burns;
}

interface ClaimState {
  /** true / false, or null when the quantum node could not be asked. */
  claimed: boolean | null;
  height?: number;
}

async function fetchClaimState(cfg: AppConfig, id: string): Promise<ClaimState> {
  const raw = await fetchJson(
    `${cfg.quantumRpcBase}/bridge/${cfg.quantumBridgeAddress}/claim/${id}`,
    { headers: { accept: 'application/json' } },
  );
  if (raw === null || typeof raw !== 'object') return { claimed: null };
  const o = raw as { claimed?: unknown; height?: unknown };
  if (typeof o.claimed !== 'boolean') return { claimed: null };
  return {
    claimed: o.claimed,
    ...(typeof o.height === 'number' && Number.isSafeInteger(o.height)
      ? { height: o.height }
      : {}),
  };
}

// ── routes ───────────────────────────────────────────────────────────────────

function unconfigured(err: unknown): Response {
  console.error('migration dapp misconfigured:', (err as Error).message);
  return json(
    {
      ok: false,
      code: 'unconfigured',
      error: 'The migration bridge is not configured on this deployment yet.',
    },
    503,
  );
}

async function handleInfo(env: Env): Promise<Response> {
  let cfg: AppConfig;
  try {
    cfg = appConfig(env);
  } catch (err) {
    return unconfigured(err);
  }

  // Stats are decoration: a dark quantum node means dashes on the metrics
  // strip, never a broken page. `null` is the documented "unreachable" shape.
  const stats = await fetchBridgeStats(cfg, Date.now());

  return json({
    ok: true,
    network: {
      chainIdHex: cfg.chainIdHex,
      chainName: cfg.chainName,
      rpcUrl: cfg.evmRpcUrl,
      explorer: cfg.evmExplorerUrl,
    },
    burnContract: cfg.burnContract,
    splvToken: cfg.splvToken,
    minBurn: cfg.minBurn,
    // The per-burn ceiling (== the burn contract's immutable maxBurn), for the
    // UI to clamp against. null when the quantum node is unreachable — the
    // contract still rejects an over-cap burn on-chain, so this is UX only.
    maxBurn: stats ? stats.maxClaimAmount : null,
    confirmations: cfg.confirmations,
    quantum: {
      explorer: cfg.quantumExplorerUrl,
      bridgeAddress: cfg.quantumBridgeAddress,
    },
    stats,
  });
}

async function handleStatus(request: Request, env: Env, rawHash: string): Promise<Response> {
  let cfg: AppConfig;
  try {
    cfg = appConfig(env);
  } catch (err) {
    return unconfigured(err);
  }

  const hash = rawHash.trim().toLowerCase();
  if (!TX_HASH.test(hash)) {
    return json(
      { ok: false, code: 'bad-hash', error: 'Not a transaction hash — expected 0x + 64 hex characters.' },
      400,
    );
  }

  // Fail OPEN on a broken limit store: this endpoint spends nothing, and
  // status polling must not die with KV. (The faucet fails closed because its
  // KV guards real spend; here it only guards upstream load.)
  try {
    const decision = await checkStatusCooldown(
      env.MIGRATION_KV,
      request.headers.get('cf-connecting-ip'),
      Date.now(),
      cfg.statusCooldownMs,
    );
    if (!decision.allowed) {
      return json(
        { ok: false, code: 'rate-limited', error: 'Polling too fast — try again in a moment.' },
        429,
        { 'retry-after': String(decision.retryAfterSeconds) },
      );
    }
  } catch (err) {
    console.error(JSON.stringify({ event: 'limit_store_error', message: String(err) }));
  }

  let receipt: unknown;
  let headHex: unknown;
  try {
    [receipt, headHex] = await Promise.all([
      evmRpc(cfg.evmRpcUrl, 'eth_getTransactionReceipt', [hash]),
      evmRpc(cfg.evmRpcUrl, 'eth_blockNumber', []),
    ]);
  } catch (err) {
    console.error(JSON.stringify({ event: 'evm_rpc_error', message: String(err) }));
    return json(
      { ok: false, code: 'rpc-unavailable', error: 'The Splendor Mainnet RPC is unreachable. Try again shortly.' },
      502,
    );
  }

  if (receipt === null || typeof receipt !== 'object') {
    // Unknown to the node: not yet mined (or never sent). The page keeps polling.
    return json({
      ok: true,
      found: false,
      confirmations: 0,
      required: cfg.confirmations,
      burns: [],
      claim: null,
    });
  }

  const r = receipt as Record<string, unknown>;
  const head = hexToBigInt(headHex);
  const mined = hexToBigInt(r.blockNumber);
  let confirmations = 0;
  if (head !== null && mined !== null && head >= mined) {
    const c = head - mined + 1n;
    confirmations = c > 1_000_000_000n ? 1_000_000_000 : Number(c);
  }
  const status = r.status === '0x1' ? 'success' : 'failed';

  const burns = decodeBurns(r, cfg.burnContract);

  // Ask the quantum node about each burn's idempotency slot (bounded: at most
  // MAX_BURNS lookups). The typical receipt has exactly one burn.
  const claims = await Promise.all(burns.map((b) => fetchClaimState(cfg, b.id)));
  const burnsOut = burns.map((b, i) => ({
    ...b,
    claimed: claims[i].claimed,
    ...(claims[i].height !== undefined ? { claimHeight: claims[i].height } : {}),
  }));

  return json({
    ok: true,
    found: true,
    status,
    confirmations,
    required: cfg.confirmations,
    burns: burnsOut,
    // Top-level claim mirrors the first burn (the common single-burn case);
    // per-burn state lives on each entry.
    claim: burnsOut.length > 0 ? { claimed: burnsOut[0].claimed, ...(burnsOut[0].claimHeight !== undefined ? { height: burnsOut[0].claimHeight } : {}) } : null,
  });
}

// ── entry ────────────────────────────────────────────────────────────────────

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // Nothing may escape: an uncaught throw is a Cloudflare 1101 error page
    // with a stack trace instead of a response we control.
    try {
      return await route(request, env);
    } catch (err) {
      console.error('unhandled error:', err instanceof Error ? err.stack : String(err));
      return json({ ok: false, code: 'internal', error: 'Unexpected error.' }, 500);
    }
  },
} satisfies ExportedHandler<Env>;

async function route(request: Request, env: Env): Promise<Response> {
  const oversized = await rejectOversizedBody(request);
  if (oversized) return oversized;

  const url = new URL(request.url);
  const path = url.pathname;
  const isRead = request.method === 'GET' || request.method === 'HEAD';

  if (path === '/health') {
    return json({ ok: true, service: 'quantum-migration' });
  }

  if (path === '/api/info') {
    if (!isRead) return json({ ok: false, code: 'method', error: 'Use GET.' }, 405, { allow: 'GET' });
    return handleInfo(env);
  }

  if (path.startsWith('/api/status/')) {
    if (!isRead) return json({ ok: false, code: 'method', error: 'Use GET.' }, 405, { allow: 'GET' });
    return handleStatus(request, env, decodeURIComponent(path.slice('/api/status/'.length)));
  }

  if (path === '/' && isRead) {
    const nonce = makeNonce();
    const html = renderPage({ nonce });
    return new Response(request.method === 'HEAD' ? null : html, {
      headers: {
        'content-type': 'text/html; charset=utf-8',
        'cache-control': 'no-store',
        ...securityHeaders(nonce),
      },
    });
  }

  return new Response('Not found', { status: 404, headers: { 'content-type': 'text/plain' } });
}
