/**
 * Separately deployable PQ bridge gateway.
 * Modes: proxy (non-custodial relay) | service-wallet (holds derived PQ key).
 *
 * Public proxy accepts pre-signed `signedTx` only.
 * Custodial unlock is seed-only: ALLOW_CUSTODIAL_PROXY=1 (private backends) or
 * MODE=service-wallet + SERVICE_SEED. EVM credentials (evmSignature / evmPrivateKey /
 * SERVICE_EVM_KEY) are rejected — clients must submit signedTx or use a seed.
 *
 * Network defaults from sqc.networks.json (override with SQC_NETWORK / SQC_RPC / PORT).
 * Errors follow SQDP-1: { code, message }.
 */
import { randomUUID, timingSafeEqual } from 'node:crypto';
import { readFileSync } from 'node:fs';
import {
  connect,
  createServer,
  type ConnectionOptions as TlsConnectOptions,
  type Server,
  type TlsOptions as TlsServerOptions,
} from 'node:tls';
import Fastify, { type FastifyInstance } from 'fastify';
import cors from '@fastify/cors';
import rateLimit from '@fastify/rate-limit';
import helmet from '@fastify/helmet';
import { HYBRID_TLS_GROUP, decideTlsGroups } from './tls-policy.ts';

export { CLASSICAL_TLS_GROUPS, HYBRID_TLS_GROUP, decideTlsGroups } from './tls-policy.ts';
export type { TlsGroupDecision } from './tls-policy.ts';
import {
  SplendorPQBridge,
  fromSPLD,
  isBridgeError,
  PROTOCOL,
  SQDP_VERSION,
  resolveNetwork,
  type BridgeErrorCode,
} from '../dist/index.js';

export type GatewayMode = 'proxy' | 'service-wallet';

const DEFAULT_CORS_ORIGINS = ['http://localhost:3000', 'http://127.0.0.1:3000'];

export type GatewayConfig = {
  networkId?: string;
  rpcUrl?: string;
  mode?: GatewayMode;
  serviceSeed?: string;
  allowCustodialProxy?: boolean;
  apiKey?: string;
  /** Bypass API key requirement for service-wallet (dev only). Env: ALLOW_INSECURE=1. */
  allowInsecure?: boolean;
  corsOrigins?: string[];
  publicHealth?: boolean;
  rateMax?: number;
  /** Stricter max for /v1/tx/*, /v1/derive, /v1/x402/pay. Env: RATE_LIMIT_TX_MAX. */
  rateTxMax?: number;
  bodyLimit?: number;
  /**
   * Trust X-Forwarded-* from a known LB only. Default false (env TRUST_PROXY=1).
   * Enabling without a trusted reverse proxy allows rate-limit bypass via spoofed headers.
   */
  trustProxy?: boolean;
  /** When false, skip listen (for inject tests). Default true in main(). */
  listen?: boolean;
  port?: number;
  /**
   * Bind address. Defaults to 127.0.0.1 so starting the gateway does not by
   * itself put it on the internet. Set 0.0.0.0 deliberately, behind TLS.
   * Env: GATEWAY_HOST.
   */
  host?: string;
  /** PEM cert path for TLS. Env: TLS_CERT_FILE. */
  tlsCertFile?: string;
  /** PEM private key path for TLS. Env: TLS_KEY_FILE. */
  tlsKeyFile?: string;
  logger?: boolean;
};

/**
 * TLS key-exchange policy.
 *
 * X25519MLKEM768 is a hybrid: the session key is safe unless BOTH X25519 and
 * ML-KEM-768 are broken, so it is never weaker than plain X25519. It matters
 * here because the chain's ML-DSA signatures protect transactions but not the
 * channel they cross — an adversary recording gateway traffic today could
 * decrypt it later under a classical-only handshake, exposing pre-broadcast
 * transactions and API keys.
 *
 * A successful probe handshake is required before the hybrid group is served,
 * and then it is the only group offered. OpenSSL 3.5+ is necessary but not
 * sufficient. See ALLOW_CLASSICAL_TLS for an explicit classical opt-in.
 */

/**
 * Opt out of the hybrid requirement and serve classical TLS anyway.
 *
 * This exists for development and for operators who knowingly terminate PQ TLS
 * somewhere else. It is deliberately an explicit opt-in rather than a silent
 * fallback: a warning in a log nobody reads is not a decision, and the
 * difference it hides — whether recorded traffic is decryptable by a future
 * quantum adversary — is the entire property this gateway claims to provide.
 */
function classicalTlsAllowed(): boolean {
  const v = (process.env.ALLOW_CLASSICAL_TLS ?? '').toLowerCase();
  return v === '1' || v === 'true' || v === 'yes';
}

/** ML-KEM hybrid key exchange landed in OpenSSL 3.5. */
function opensslHasMlkem(): boolean {
  const [major, minor] = (process.versions.openssl ?? '0.0')
    .split('.')
    .map((n) => Number.parseInt(n, 10) || 0);
  return major > 3 || (major === 3 && minor >= 5);
}

/**
 * Report the key exchange group a real handshake actually settles on when the
 * hybrid group is the only one offered.
 *
 * There is no API that answers "does this build support X25519MLKEM768", and
 * the obvious substitutes do not work. `createSecureContext({ groups })` accepts
 * any string at all, including nonsense, so it cannot reject an unsupported
 * group — and OpenSSL silently drops group names it does not recognise instead
 * of failing, so even a successful handshake proves nothing on its own. The
 * only honest signal is what came out the other side.
 */
function negotiatedGroup(cert: Buffer, key: Buffer): Promise<string | null> {
  return new Promise((resolve) => {
    let settled = false;
    const done = (v: string | null) => {
      if (!settled) {
        settled = true;
        resolve(v);
      }
    };

    let server: Server;
    try {
      server = createServer(
        { cert, key, minVersion: 'TLSv1.3', groups: HYBRID_GROUP } as TlsServerOptions,
        (s) => s.end(),
      );
    } catch {
      return done(null);
    }
    server.on('error', () => {
      done(null);
      server.close();
    });
    // Never let a probe hold up startup.
    const timer = setTimeout(() => {
      done(null);
      server.close();
    }, 2000);
    timer.unref?.();

    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      const socket = connect(
        {
          port,
          host: '127.0.0.1',
          rejectUnauthorized: false,
          minVersion: 'TLSv1.3',
          groups: HYBRID_GROUP,
        } as TlsConnectOptions,
        () => {
          const info = socket.getEphemeralKeyInfo() as { name?: string } | null;
          done(info?.name ?? null);
          clearTimeout(timer);
          socket.end();
          server.close();
        },
      );
      socket.on('error', () => {
        done(null);
        clearTimeout(timer);
        server.close();
      });
    });
  });
}

/** The hybrid group on its own, for the capability probe. */
const HYBRID_GROUP = HYBRID_TLS_GROUP;

/**
 * Build Fastify's https options with post-quantum hybrid key exchange, or
 * return null to serve plain HTTP.
 *
 * Both cert and key must be present; configuring one without the other is a
 * misconfiguration that would otherwise silently downgrade to HTTP, so it
 * throws instead. A runtime that cannot do hybrid key exchange is refused on
 * the same grounds unless ALLOW_CLASSICAL_TLS says otherwise.
 */
async function buildHttpsOptions(
  certFile: string,
  keyFile: string,
  logger: boolean,
): Promise<{ cert: Buffer; key: Buffer; minVersion: 'TLSv1.3'; groups?: string } | null> {
  if (!certFile && !keyFile) return null;
  if (!certFile || !keyFile) {
    throw new Error(
      'TLS misconfigured: TLS_CERT_FILE and TLS_KEY_FILE must both be set (refusing to fall back to plaintext)',
    );
  }

  const base = {
    cert: readFileSync(certFile),
    key: readFileSync(keyFile),
    minVersion: 'TLSv1.3' as const,
  };

  const group = await negotiatedGroup(base.cert, base.key);
  const detail =
    `OpenSSL ${process.versions.openssl} (mlkem-capable=${opensslHasMlkem()}); ` +
    `a hybrid-only handshake negotiated ${group ?? 'nothing'} instead of X25519MLKEM768`;
  const decision = decideTlsGroups({
    probeGroup: group,
    allowClassical: classicalTlsAllowed(),
  });
  if (!decision.ok) {
    throw new Error(
      `TLS refused: ${decision.reason}. ${detail}. ` +
        'OpenSSL version alone is not treated as proof the group exists, and unknown group ' +
        'names are dropped rather than rejected. Serving the hybrid name plus classical ' +
        'fallbacks would advertise post-quantum key exchange while connections could stay ' +
        'classical. Upgrade Node so a probe handshake negotiates X25519MLKEM768, or set ' +
        'ALLOW_CLASSICAL_TLS=1 to accept classical key exchange deliberately.',
    );
  }
  if (decision.mode === 'hybrid-enforced') {
    if (logger) {
      console.log(
        `[gateway] TLS 1.3 enabled, key exchange enforced: ${decision.groups} (probe negotiated ${group})`,
      );
    }
    return { ...base, groups: decision.groups };
  }
  console.warn(
    `[gateway] WARNING: ALLOW_CLASSICAL_TLS is set and this build cannot do X25519MLKEM768 (${detail}). ` +
      `Serving TLS with classical groups only (${decision.groups}) — recorded traffic is decryptable ` +
      'by a future quantum adversary. This is opportunistic TLS, not post-quantum confidentiality.',
  );
  return { ...base, groups: decision.groups };
}

const signedTxSchema = {
  oneOf: [
    { type: 'object', additionalProperties: true },
    { type: 'string', minLength: 2 },
  ],
} as const;

const transferBodySchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    to: { type: 'string' },
    amount: { type: 'string' },
    amountSPLD: { type: 'string' },
    fee: { type: 'string' },
    memo: { type: 'string' },
    signedTx: signedTxSchema,
    seed: { type: 'string' },
    evmSignature: { type: 'string' },
    evmPrivateKey: { type: 'string' },
  },
} as const;

const callBodySchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    contract: { type: 'string' },
    calldata_hex: { type: 'string' },
    calldata_b64: { type: 'string' },
    value: { type: 'string' },
    fee: { type: 'string' },
    signedTx: signedTxSchema,
    seed: { type: 'string' },
    evmSignature: { type: 'string' },
    evmPrivateKey: { type: 'string' },
  },
} as const;

const submitBodySchema = {
  type: 'object',
  additionalProperties: false,
  required: ['signedTx'],
  properties: {
    signedTx: signedTxSchema,
  },
} as const;

const deriveBodySchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    seed: { type: 'string' },
    evmSignature: { type: 'string' },
    evmPrivateKey: { type: 'string' },
  },
} as const;

const x402BodySchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    to: { type: 'string' },
    amount: { type: 'string' },
    amountSPLD: { type: 'string' },
    requirements: { type: 'object', additionalProperties: true },
    settle: { type: 'boolean' },
    signedTx: signedTxSchema,
    seed: { type: 'string' },
    evmSignature: { type: 'string' },
    evmPrivateKey: { type: 'string' },
  },
} as const;

/** Fail closed when signedTx is present but chain_id is missing/empty. */
function assertSignedTxChainId(
  signedTx: object | string | undefined,
  expectedChainId: string,
): void {
  if (signedTx == null) return;
  const body = typeof signedTx === 'string' ? (JSON.parse(signedTx) as object) : signedTx;
  const remote =
    body && typeof body === 'object' && 'chain_id' in body
      ? String((body as { chain_id?: unknown }).chain_id ?? '').trim()
      : '';
  if (!remote) {
    throw Object.assign(new Error(`chain_id mismatch: got (missing), want ${expectedChainId}`), {
      code: 'CHAIN_MISMATCH',
    });
  }
  if (remote !== expectedChainId) {
    throw Object.assign(new Error(`chain_id mismatch: got ${remote}, want ${expectedChainId}`), {
      code: 'CHAIN_MISMATCH',
    });
  }
}

type Creds = {
  seed?: string;
  /** Accepted in schema only so we can return a clear removal error. */
  evmSignature?: string;
  /** Accepted in schema only so we can return a clear removal error. */
  evmPrivateKey?: string;
};

const EVM_UNLOCK_REMOVED =
  'EVM unlock removed — use seed or submit signedTx';

type ApiError = { code: string; message: string; warning?: string };

/** Stable public phrases — never echo upstream stacks, URLs, or body previews. */
const PUBLIC_CODE_MESSAGE: Record<string, string> = {
  NOT_CONNECTED: 'Not connected',
  USER_REJECTED: 'Request rejected',
  NO_PROVIDER: 'Wallet provider not found',
  RPC: 'Upstream RPC error',
  RPC_UNAVAILABLE: 'RPC unavailable',
  NETWORK: 'Upstream network error',
  WASM: 'Signer error',
  INVALID_ADDRESS: 'Invalid address',
  INVALID_AMOUNT: 'Invalid amount',
  INVALID_FEE: 'Invalid fee',
  TIMEOUT: 'Request timed out',
  TX_NOT_CONFIRMED: 'Transaction not confirmed',
  TX_FAILED: 'Transaction failed',
  CHAIN_MISMATCH: 'Chain ID mismatch',
  UNKNOWN: 'Request failed',
};

const SAFE_PUBLIC_PHRASES = [
  EVM_UNLOCK_REMOVED,
  'invalid or missing API key',
  'to is required',
  'amount or amountSPLD required',
  'provide seed (EVM derive removed — submit signedTx otherwise)',
];

function apiError(
  code: BridgeErrorCode | string,
  message: string,
  extra?: { warning?: string },
): ApiError {
  return { code, message, ...extra };
}

function sanitizePublicMessage(code: string, raw: string): string {
  const trimmed = raw.trim();
  if (SAFE_PUBLIC_PHRASES.some((p) => trimmed === p || trimmed.includes(p))) {
    for (const p of SAFE_PUBLIC_PHRASES) {
      if (trimmed === p || trimmed.includes(p)) return p;
    }
  }
  if (/EVM unlock removed/i.test(trimmed)) return EVM_UNLOCK_REMOVED;
  if (/proxy mode rejects|custodial credentials|proxy requires signedTx/i.test(trimmed)) {
    return 'Proxy requires signedTx — custodial credentials rejected';
  }
  return PUBLIC_CODE_MESSAGE[code] ?? PUBLIC_CODE_MESSAGE.UNKNOWN;
}

function errorFromCaught(e: unknown): ApiError {
  if (isBridgeError(e)) {
    const hint = typeof e.userHint === 'string' && e.userHint.trim() ? e.userHint.trim() : '';
    const msg = hint || sanitizePublicMessage(e.code, e.message);
    return apiError(e.code, msg);
  }
  const message = e instanceof Error ? e.message : String(e);
  if (
    /proxy mode rejects|custodial credentials|proxy requires signedTx|EVM unlock removed/i.test(
      message,
    )
  ) {
    return apiError('USER_REJECTED', sanitizePublicMessage('USER_REJECTED', message));
  }
  if (/invalid sq address/i.test(message)) {
    return apiError('INVALID_ADDRESS', PUBLIC_CODE_MESSAGE.INVALID_ADDRESS);
  }
  if (/fee/i.test(message) && /minimum|invalid/i.test(message)) {
    return apiError('INVALID_FEE', PUBLIC_CODE_MESSAGE.INVALID_FEE);
  }
  if (/chain_id mismatch/i.test(message)) {
    return apiError('CHAIN_MISMATCH', PUBLIC_CODE_MESSAGE.CHAIN_MISMATCH);
  }
  if (/timeout|abort/i.test(message)) {
    return apiError('TIMEOUT', PUBLIC_CODE_MESSAGE.TIMEOUT);
  }
  if (/fetch failed|ECONNREFUSED|ENOTFOUND|network/i.test(message)) {
    return apiError('NETWORK', PUBLIC_CODE_MESSAGE.NETWORK);
  }
  return apiError('UNKNOWN', PUBLIC_CODE_MESSAGE.UNKNOWN);
}

/** Map caught errors to HTTP status; SQDP codes stay in the body. */
function httpStatusFromCaught(e: unknown): number {
  if (isBridgeError(e)) {
    if (e.code === 'TIMEOUT') return 504;
    if (e.code === 'NETWORK' || e.code === 'RPC') return 502;
    if (e.code === 'USER_REJECTED') return 403;
    if (
      e.code === 'INVALID_ADDRESS' ||
      e.code === 'INVALID_AMOUNT' ||
      e.code === 'INVALID_FEE' ||
      e.code === 'CHAIN_MISMATCH' ||
      e.code === 'NOT_CONNECTED'
    ) {
      return 400;
    }
    return 400;
  }
  const message = e instanceof Error ? e.message : String(e);
  if (/EVM unlock removed/i.test(message)) {
    return 410;
  }
  if (/proxy mode rejects|custodial credentials|proxy requires signedTx/i.test(message)) {
    return 403;
  }
  if (/timeout|abort/i.test(message)) return 504;
  if (/fetch failed|ECONNREFUSED|ENOTFOUND|network/i.test(message)) return 502;
  return 400;
}

function replyError(reply: { code: (n: number) => { send: (b: unknown) => unknown } }, e: unknown) {
  return reply.code(httpStatusFromCaught(e)).send(errorFromCaught(e));
}

/** Constant-time string compare; length mismatch is always a fail. */
export function safeEqualString(a: string, b: string): boolean {
  const ba = Buffer.from(a, 'utf8');
  const bb = Buffer.from(b, 'utf8');
  if (ba.length !== bb.length) return false;
  return timingSafeEqual(ba, bb);
}

function configFromEnv(): GatewayConfig {
  const netKey = process.env.SQC_NETWORK || process.env.NETWORK || undefined;
  const net = resolveNetwork(netKey, {
    allowInsecureHttp: process.env.ALLOW_INSECURE_HTTP === '1',
  });
  const corsEnv = process.env.CORS_ORIGINS;
  const healthEnv = process.env.PUBLIC_HEALTH;
  return {
    networkId: netKey,
    rpcUrl: (process.env.SQC_RPC || net.rpcUrls[0]).replace(/\/$/, ''),
    mode: ((process.env.MODE || 'proxy').toLowerCase() as GatewayMode),
    serviceSeed: process.env.SERVICE_SEED || '',
    allowCustodialProxy: process.env.ALLOW_CUSTODIAL_PROXY === '1',
    apiKey: process.env.GATEWAY_API_KEY || '',
    allowInsecure: process.env.ALLOW_INSECURE === '1',
    corsOrigins: corsEnv
      ? corsEnv
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
      : undefined,
    publicHealth:
      healthEnv === '1' ? true : healthEnv === '0' ? false : undefined,
    rateMax: Number(process.env.RATE_LIMIT_MAX || 120),
    rateTxMax: Number(process.env.RATE_LIMIT_TX_MAX || 30),
    trustProxy: process.env.TRUST_PROXY === '1',
    bodyLimit: Number(process.env.BODY_LIMIT_BYTES || 1_048_576),
    port: Number(process.env.PORT || net.gatewayPort),
    listen: true,
    logger: true,
  };
}

export type BuiltGateway = {
  app: FastifyInstance;
  bridge: SplendorPQBridge;
  config: Required<
    Pick<
      GatewayConfig,
      | 'rpcUrl'
      | 'mode'
      | 'allowCustodialProxy'
      | 'apiKey'
      | 'corsOrigins'
      | 'publicHealth'
      | 'rateMax'
      | 'rateTxMax'
      | 'bodyLimit'
      | 'trustProxy'
    >
  > & { networkId: string; chainId: string };
  close: () => Promise<void>;
};

/**
 * Build a Fastify gateway app (no listen unless cfg.listen).
 * Exported for inject-based security tests.
 */
export async function buildServer(overrides: GatewayConfig = {}): Promise<BuiltGateway> {
  const env = configFromEnv();
  const net = resolveNetwork(overrides.networkId ?? env.networkId, {
    allowInsecureHttp:
      process.env.ALLOW_INSECURE_HTTP === '1' ||
      (overrides.networkId ?? env.networkId ?? 'local') === 'local',
  });
  const MODE = (overrides.mode ?? env.mode ?? 'proxy').toLowerCase() as GatewayMode;
  // Full network failover list unless an explicit rpcUrl override (tests / single-target).
  const bridgeRpcUrls =
    overrides.rpcUrl != null
      ? [overrides.rpcUrl.replace(/\/$/, '')]
      : net.rpcUrls.map((u) => u.replace(/\/$/, ''));
  const SQC_RPC = bridgeRpcUrls[0] || (env.rpcUrl ?? net.rpcUrls[0]).replace(/\/$/, '');
  const ALLOW_CUSTODIAL_PROXY =
    overrides.allowCustodialProxy ?? env.allowCustodialProxy ?? false;
  const API_KEY = overrides.apiKey ?? env.apiKey ?? '';
  const ALLOW_INSECURE =
    overrides.allowInsecure ?? env.allowInsecure ?? process.env.ALLOW_INSECURE === '1';
  const SERVICE_SEED = overrides.serviceSeed ?? env.serviceSeed ?? '';
  const SERVICE_EVM_KEY_ENV = (process.env.SERVICE_EVM_KEY || '').trim();
  const CORS_ORIGINS = overrides.corsOrigins ?? env.corsOrigins ?? [...DEFAULT_CORS_ORIGINS];
  const isLocalNet = net.id === 'local';
  const PUBLIC_HEALTH = overrides.publicHealth ?? env.publicHealth ?? isLocalNet;
  const RATE_MAX = overrides.rateMax ?? env.rateMax ?? 120;
  const RATE_TX_MAX = Math.min(
    overrides.rateTxMax ?? env.rateTxMax ?? 30,
    RATE_MAX,
  );
  const BODY_LIMIT = overrides.bodyLimit ?? env.bodyLimit ?? 1_048_576;
  const TRUST_PROXY = overrides.trustProxy ?? env.trustProxy ?? false;
  const logger = overrides.logger ?? false;

  if (MODE === 'service-wallet' && CORS_ORIGINS.includes('*')) {
    throw new Error(
      'MODE=service-wallet rejects CORS origin "*" — set explicit corsOrigins (or CORS_ORIGINS)',
    );
  }

  if (ALLOW_CUSTODIAL_PROXY && CORS_ORIGINS.includes('*')) {
    throw new Error(
      'ALLOW_CUSTODIAL_PROXY=1 refuses CORS origin "*" — set an explicit allowlist',
    );
  }

  if (MODE === 'service-wallet' && !API_KEY && !ALLOW_INSECURE) {
    throw new Error(
      'MODE=service-wallet requires GATEWAY_API_KEY (or allowInsecure / ALLOW_INSECURE=1)',
    );
  }

  if (ALLOW_CUSTODIAL_PROXY && !API_KEY && !ALLOW_INSECURE) {
    throw new Error(
      'ALLOW_CUSTODIAL_PROXY=1 requires GATEWAY_API_KEY (or allowInsecure / ALLOW_INSECURE=1)',
    );
  }

  const wasmPinOpts = {
    wasmSha256: net.wasmSha256 || undefined,
    execSha256: net.execSha256 || undefined,
    networkId: net.id,
  };

  function assertNoEvmCreds(creds: Creds): void {
    if (creds.evmSignature || creds.evmPrivateKey) {
      throw Object.assign(new Error(EVM_UNLOCK_REMOVED), { code: 'USER_REJECTED' });
    }
  }

  function assertProxyAllowsCreds(creds: Creds): void {
    if (MODE !== 'proxy') return;
    if (creds.seed && !ALLOW_CUSTODIAL_PROXY) {
      throw Object.assign(
        new Error(
          'proxy mode rejects ML-DSA-65 seed — sign client-side and POST signedTx',
        ),
        { code: 'USER_REJECTED' },
      );
    }
  }

  async function connectEphemeral(creds: Creds): Promise<SplendorPQBridge> {
    assertNoEvmCreds(creds);
    assertProxyAllowsCreds(creds);
    const tmp = new SplendorPQBridge({
      rpcUrls: bridgeRpcUrls,
      chainId: net.chainId,
      timeoutMs: net.timeoutMs,
      retries: net.retries,
      ...wasmPinOpts,
    });
    await tmp.init();
    if (creds.seed) {
      await tmp.connectFromSeed(creds.seed);
    } else {
      throw Object.assign(
        new Error(
          MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY
            ? 'proxy requires signedTx (custodial credentials disabled)'
            : 'provide seed (EVM unlock removed — submit signedTx otherwise)',
        ),
        { code: 'USER_REJECTED' },
      );
    }
    return tmp;
  }

  function requireApiKey(
    headers: Record<string, string | string[] | undefined>,
  ): ApiError | null {
    if (!API_KEY) return null;
    const got = headers['x-api-key'] ?? headers['authorization'];
    const raw = Array.isArray(got) ? got[0] : got;
    const token = raw?.startsWith('Bearer ') ? raw.slice(7) : raw;
    if (!token || !safeEqualString(token, API_KEY)) {
      return apiError('USER_REJECTED', 'invalid or missing API key');
    }
    return null;
  }

  const bridge = new SplendorPQBridge({
    rpcUrls: bridgeRpcUrls,
    chainId: net.chainId,
    timeoutMs: net.timeoutMs,
    retries: net.retries,
    ...wasmPinOpts,
  });
  await bridge.init();

  if (MODE === 'service-wallet') {
    if (SERVICE_SEED) {
      await bridge.connectFromSeed(SERVICE_SEED);
      if (logger) {
        console.log(`[gateway] service-wallet from SERVICE_SEED → ${bridge.getAddress()}`);
      }
    } else if (SERVICE_EVM_KEY_ENV) {
      throw new Error(
        'SERVICE_EVM_KEY is no longer supported — set SERVICE_SEED (EVM unlock removed)',
      );
    } else {
      throw new Error('MODE=service-wallet requires SERVICE_SEED');
    }
    if (!API_KEY && ALLOW_INSECURE && logger) {
      console.warn(
        '[gateway] WARNING: service-wallet running with ALLOW_INSECURE — set GATEWAY_API_KEY in production',
      );
    }
  } else if (logger) {
    console.log('[gateway] MODE=proxy (non-custodial — clients sign, gateway relays signedTx)');
    if (ALLOW_CUSTODIAL_PROXY) {
      console.warn(
        '[gateway] ALLOW_CUSTODIAL_PROXY=1 — seed unlock accepted only with GATEWAY_API_KEY (private use); prefer signedTx',
      );
    }
    console.log(`[gateway] SQDP-${SQDP_VERSION} network=${net.id} rpc=${bridgeRpcUrls.join(',')}`);
  }

  const TLS_CERT = overrides.tlsCertFile ?? process.env.TLS_CERT_FILE ?? '';
  const TLS_KEY = overrides.tlsKeyFile ?? process.env.TLS_KEY_FILE ?? '';
  const https = await buildHttpsOptions(TLS_CERT, TLS_KEY, logger);

  const app = Fastify({
    logger,
    bodyLimit: BODY_LIMIT,
    trustProxy: TRUST_PROXY,
    // Log as `reqId`. A caller's X-Request-Id is reused only when short and plain, so it cannot flood or forge log lines.
    requestIdHeader: false,
    genReqId: (req) => {
      const raw = req.headers['x-request-id'];
      const id = Array.isArray(raw) ? raw[0] : raw;
      return typeof id === 'string' && /^[\w.:-]{1,128}$/.test(id) ? id : randomUUID();
    },
    ...(https ? { https } : {}),
    ajv: {
      customOptions: {
        // Keep unknown fields so additionalProperties: false rejects custodial typos.
        removeAdditional: false,
        coerceTypes: 'array',
      },
    },
  });

  await app.register(helmet, {
    global: true,
    contentSecurityPolicy: false,
  });

  app.addHook('onSend', async (request, reply) => {
    reply.header('x-request-id', request.id);
  });

  await app.register(cors, {
    origin: CORS_ORIGINS.includes('*') ? true : CORS_ORIGINS,
  });

  await app.register(rateLimit, {
    max: RATE_MAX,
    timeWindow: '1 minute',
  });

  const txRateLimit = {
    config: {
      rateLimit: {
        max: RATE_TX_MAX,
        timeWindow: '1 minute',
      },
    },
  };

  app.addHook('onRequest', async (req, reply) => {
    const needsKey =
      (MODE === 'service-wallet' && API_KEY) || (ALLOW_CUSTODIAL_PROXY && API_KEY);
    if (!needsKey) return;
    const path = req.url.split('?')[0];
    if (path === '/health' || path === '/ready') return;
    const err = requireApiKey(req.headers as Record<string, string | string[] | undefined>);
    if (err) {
      return reply.code(401).send(err);
    }
  });

  app.get('/health', async () => {
    const body: Record<string, unknown> = {
      ok: true,
      mode: MODE,
      network: net.id,
      sqdp: SQDP_VERSION,
      protocol: {
        chainId: PROTOCOL.chainId,
        confirmation: PROTOCOL.confirmation,
        minFee: PROTOCOL.minFee,
      },
      custodialProxy: MODE === 'proxy' && ALLOW_CUSTODIAL_PROXY,
      service_address:
        MODE === 'service-wallet' && bridge.getConnectionState() === 'connected'
          ? bridge.getAddress()
          : null,
    };
    if (PUBLIC_HEALTH || MODE === 'service-wallet') {
      if (PUBLIC_HEALTH) {
        body.sqc_rpc = bridgeRpcUrls[0] || SQC_RPC;
      }
    }
    return body;
  });

  /** Readiness: probes upstream chain_id (fail closed). Liveness stays on /health. */
  app.get('/ready', async (_req, reply) => {
    try {
      await bridge.rpc.assertChainId();
      return { ok: true, ready: true, network: net.id, chainId: net.chainId };
    } catch (e) {
      return reply.code(503).send({
        ok: false,
        ready: false,
        ...errorFromCaught(e),
      });
    }
  });

  app.get('/v1/status', async () => bridge.getStatus());

  app.get<{ Params: { address: string } }>('/v1/balance/:address', async (req, reply) => {
    try {
      return await bridge.rpc.getBalance(req.params.address);
    } catch (e) {
      return replyError(reply, e);
    }
  });

  app.get<{ Params: { address: string } }>('/v1/nonce/:address', async (req, reply) => {
    try {
      return await bridge.rpc.getNonce(req.params.address);
    } catch (e) {
      return replyError(reply, e);
    }
  });

  app.post<{
    Body: { seed?: string; evmSignature?: string; evmPrivateKey?: string };
  }>(
    '/v1/derive',
    {
      ...txRateLimit,
      schema: { body: deriveBodySchema },
    },
    async (req, reply) => {
      let tmp: SplendorPQBridge | null = null;
      try {
        const body = req.body || {};
        if (body.evmSignature || body.evmPrivateKey) {
          return reply
            .code(410)
            .send(apiError('USER_REJECTED', EVM_UNLOCK_REMOVED));
        }
        if (MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY) {
          return reply.code(403).send(
            apiError(
              'USER_REJECTED',
              'proxy /v1/derive disabled — derive client-side with WASM (or set ALLOW_CUSTODIAL_PROXY=1)',
            ),
          );
        }
        if (!body.seed) {
          return reply
            .code(400)
            .send(apiError('USER_REJECTED', 'provide seed (EVM derive removed — submit signedTx otherwise)'));
        }
        tmp = await connectEphemeral({ seed: body.seed });
        const info = tmp.getDeriveInfo();
        tmp.disconnect();
        return { ...info };
      } catch (e: unknown) {
        tmp?.disconnect();
        return replyError(reply, e);
      }
    },
  );

  app.post<{ Body: { signedTx: object | string } }>(
    '/v1/tx/submit',
    {
      ...txRateLimit,
      schema: { body: submitBodySchema },
    },
    async (req, reply) => {
      try {
        assertSignedTxChainId(req.body.signedTx, net.chainId);
        await bridge.rpc.assertChainId();
        return await bridge.rpc.submitTransaction(req.body.signedTx);
      } catch (e: unknown) {
        return replyError(reply, e);
      }
    },
  );

  app.post<{
    Body: {
      to?: string;
      amount?: string;
      amountSPLD?: string;
      fee?: string;
      memo?: string;
      signedTx?: object;
      seed?: string;
      evmSignature?: string;
      evmPrivateKey?: string;
    };
  }>(
    '/v1/tx/transfer',
    {
      ...txRateLimit,
      schema: { body: transferBodySchema },
    },
    async (req, reply) => {
      let ephemeral: SplendorPQBridge | null = null;
      try {
        if (req.body.signedTx) {
          assertSignedTxChainId(req.body.signedTx, net.chainId);
          await bridge.rpc.assertChainId();
          return await bridge.rpc.submitTransaction(req.body.signedTx);
        }
        if (req.body.evmSignature || req.body.evmPrivateKey) {
          return reply.code(410).send(apiError('USER_REJECTED', EVM_UNLOCK_REMOVED));
        }

        let signer = bridge;
        if (MODE === 'proxy') {
          ephemeral = await connectEphemeral(req.body);
          signer = ephemeral;
        }

        if (!req.body.to) {
          return reply.code(400).send(apiError('INVALID_ADDRESS', 'to is required'));
        }
        const amount =
          req.body.amountSPLD != null ? fromSPLD(req.body.amountSPLD) : req.body.amount;
        if (!amount) {
          return reply.code(400).send(apiError('INVALID_AMOUNT', 'amount or amountSPLD required'));
        }

        const result = await signer.sendTransfer({
          to: req.body.to,
          amount,
          fee: req.body.fee,
          memo: req.body.memo,
        });
        ephemeral?.disconnect();
        return result;
      } catch (e: unknown) {
        ephemeral?.disconnect();
        return replyError(reply, e);
      }
    },
  );

  app.post<{
    Body: {
      contract: string;
      calldata_hex?: string;
      calldata_b64?: string;
      value?: string;
      fee?: string;
      signedTx?: object;
      seed?: string;
      evmSignature?: string;
      evmPrivateKey?: string;
    };
  }>(
    '/v1/tx/call',
    {
      ...txRateLimit,
      schema: { body: callBodySchema },
    },
    async (req, reply) => {
      let ephemeral: SplendorPQBridge | null = null;
      try {
        if (req.body.signedTx) {
          assertSignedTxChainId(req.body.signedTx, net.chainId);
          await bridge.rpc.assertChainId();
          return await bridge.rpc.submitTransaction(req.body.signedTx);
        }
        if (req.body.evmSignature || req.body.evmPrivateKey) {
          return reply.code(410).send(apiError('USER_REJECTED', EVM_UNLOCK_REMOVED));
        }
        let signer = bridge;
        if (MODE === 'proxy') {
          ephemeral = await connectEphemeral(req.body);
          signer = ephemeral;
        }
        const calldata = req.body.calldata_hex || req.body.calldata_b64 || '';
        const result = await signer.callContract({
          contract: req.body.contract,
          calldata,
          value: req.body.value,
          fee: req.body.fee,
        });
        ephemeral?.disconnect();
        return result;
      } catch (e: unknown) {
        ephemeral?.disconnect();
        return replyError(reply, e);
      }
    },
  );

  app.post<{
    Body: {
      to: string;
      amount?: string;
      amountSPLD?: string;
      requirements?: object;
      settle?: boolean;
      signedTx?: object;
      seed?: string;
      evmSignature?: string;
      evmPrivateKey?: string;
    };
  }>(
    '/v1/x402/pay',
    {
      ...txRateLimit,
      schema: { body: x402BodySchema },
    },
    async (req, reply) => {
      let ephemeral: SplendorPQBridge | null = null;
      try {
        if (req.body.signedTx) {
          assertSignedTxChainId(req.body.signedTx, net.chainId);
          await bridge.rpc.assertChainId();
          return await bridge.rpc.submitTransaction(req.body.signedTx);
        }
        if (req.body.evmSignature || req.body.evmPrivateKey) {
          return reply.code(410).send(apiError('USER_REJECTED', EVM_UNLOCK_REMOVED));
        }

        let signer = bridge;
        if (MODE === 'proxy') {
          ephemeral = await connectEphemeral(req.body);
          signer = ephemeral;
        }
        const amount =
          req.body.amountSPLD != null
            ? fromSPLD(req.body.amountSPLD)
            : req.body.amount || '0';
        const result = await signer.payX402({
          to: req.body.to,
          amount,
          requirements: req.body.requirements,
          settle: req.body.settle,
        });
        ephemeral?.disconnect();
        return result;
      } catch (e: unknown) {
        ephemeral?.disconnect();
        return replyError(reply, e);
      }
    },
  );

  const shouldListen = overrides.listen ?? false;
  if (shouldListen) {
    const port = overrides.port ?? env.port ?? net.gatewayPort;
    const host = overrides.host ?? process.env.GATEWAY_HOST ?? '127.0.0.1';
    await app.listen({ port, host });
    if (logger) {
      const scheme = https ? 'https' : 'http';
      console.log(
        `[gateway] listening on ${scheme}://${host}:${port} mode=${MODE} network=${net.id}`,
      );
      if (!https && host !== '127.0.0.1' && host !== 'localhost') {
        console.warn(
          '[gateway] WARNING: bound to a non-loopback address without TLS. ' +
            'Traffic — including API keys and unbroadcast transactions — is readable on the wire. ' +
            'Set TLS_CERT_FILE/TLS_KEY_FILE or terminate TLS in front of this process.',
        );
      }
    }
  }

  return {
    app,
    bridge,
    config: {
      networkId: net.id,
      chainId: net.chainId,
      rpcUrl: bridgeRpcUrls[0] || SQC_RPC,
      mode: MODE,
      allowCustodialProxy: ALLOW_CUSTODIAL_PROXY,
      apiKey: API_KEY,
      corsOrigins: CORS_ORIGINS,
      publicHealth: PUBLIC_HEALTH,
      rateMax: RATE_MAX,
      rateTxMax: RATE_TX_MAX,
      bodyLimit: BODY_LIMIT,
      trustProxy: TRUST_PROXY,
    },
    close: async () => {
      try {
        bridge.disconnect();
      } catch {
        /* ignore */
      }
      await app.close();
    },
  };
}

async function main() {
  await buildServer({ ...configFromEnv(), listen: true, logger: true });
}

const isDirectRun =
  typeof process !== 'undefined' &&
  process.argv[1] &&
  (process.argv[1].endsWith('server.ts') || process.argv[1].endsWith('server.js'));

if (isDirectRun) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
