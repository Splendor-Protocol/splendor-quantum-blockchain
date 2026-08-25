/**
 * Separately deployable PQ bridge gateway.
 * Modes: proxy (non-custodial relay) | service-wallet (holds derived PQ key).
 *
 * Public proxy accepts pre-signed `signedTx` only.
 * Custodial credentials (seed / evmSignature / evmPrivateKey) require
 * ALLOW_CUSTODIAL_PROXY=1 (private backends) or MODE=service-wallet.
 *
 * Network defaults from sqc.networks.json (override with SQC_NETWORK / SQC_RPC / PORT).
 * Errors follow SQDP-1: { code, message }.
 */
import { createSecureContext, timingSafeEqual } from 'node:crypto';
import { readFileSync } from 'node:fs';
import Fastify, { type FastifyInstance } from 'fastify';
import cors from '@fastify/cors';
import rateLimit from '@fastify/rate-limit';
import helmet from '@fastify/helmet';
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
  serviceEvmKey?: string;
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
 * TLS key-exchange groups, most preferred first.
 *
 * X25519MLKEM768 is a hybrid: the session key is safe unless BOTH X25519 and
 * ML-KEM-768 are broken, so it is never weaker than plain X25519. It matters
 * here because the chain's ML-DSA signatures protect transactions but not the
 * channel they cross — an adversary recording gateway traffic today could
 * decrypt it later under a classical-only handshake, exposing pre-broadcast
 * transactions and API keys.
 *
 * Requires an OpenSSL 3.5+ Node build; we fall back to classical groups rather
 * than refuse to start.
 */
const PQ_TLS_GROUPS = 'X25519MLKEM768:X25519:P-256';

/**
 * Build Fastify's https options with post-quantum hybrid key exchange, or
 * return null to serve plain HTTP.
 *
 * Both cert and key must be present; configuring one without the other is a
 * misconfiguration that would otherwise silently downgrade to HTTP, so it
 * throws instead.
 */
function buildHttpsOptions(
  certFile: string,
  keyFile: string,
  logger: boolean,
): { cert: Buffer; key: Buffer; minVersion: 'TLSv1.3'; groups?: string } | null {
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

  // Older Node builds reject an unknown group name at handshake setup. Probe
  // once so an unsupported runtime loses the PQ property instead of the
  // ability to serve TLS at all.
  try {
    createSecureContext({ ...base, groups: PQ_TLS_GROUPS });
    if (logger) {
      console.log(`[gateway] TLS 1.3 enabled, key exchange: ${PQ_TLS_GROUPS}`);
    }
    return { ...base, groups: PQ_TLS_GROUPS };
  } catch {
    console.warn(
      '[gateway] WARNING: this Node build does not support X25519MLKEM768 (needs OpenSSL 3.5+). ' +
        'Serving TLS with classical key exchange only — recorded traffic is decryptable by a future ' +
        'quantum adversary. Upgrade Node to restore post-quantum confidentiality.',
    );
    return base;
  }
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
  evmSignature?: string;
  evmPrivateKey?: string;
};

type ApiError = { code: string; message: string; warning?: string };

function apiError(
  code: BridgeErrorCode | string,
  message: string,
  extra?: { warning?: string },
): ApiError {
  return { code, message, ...extra };
}

function errorFromCaught(e: unknown): ApiError {
  if (isBridgeError(e)) {
    return apiError(e.code, e.message);
  }
  const message = e instanceof Error ? e.message : String(e);
  if (/proxy mode rejects|custodial credentials|proxy requires signedTx/i.test(message)) {
    return apiError('USER_REJECTED', message);
  }
  if (/invalid sq address/i.test(message)) {
    return apiError('INVALID_ADDRESS', message);
  }
  if (/fee/i.test(message) && /minimum|invalid/i.test(message)) {
    return apiError('INVALID_FEE', message);
  }
  if (/chain_id mismatch/i.test(message)) {
    return apiError('CHAIN_MISMATCH', message);
  }
  if (/timeout|abort/i.test(message)) {
    return apiError('TIMEOUT', message);
  }
  return apiError('UNKNOWN', message);
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
    serviceEvmKey: process.env.SERVICE_EVM_KEY || '',
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
  const SERVICE_EVM_KEY = overrides.serviceEvmKey ?? env.serviceEvmKey ?? '';
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

  const wasmPinOpts = {
    wasmSha256: net.wasmSha256 || undefined,
    execSha256: net.execSha256 || undefined,
    networkId: net.id,
  };

  function assertProxyAllowsCreds(creds: Creds): void {
    if (MODE !== 'proxy') return;
    if (creds.evmPrivateKey) {
      throw Object.assign(
        new Error('proxy mode rejects evmPrivateKey — submit signedTx only'),
        { code: 'USER_REJECTED' },
      );
    }
    if (creds.seed) {
      throw Object.assign(
        new Error(
          'proxy mode rejects ML-DSA-65 seed — sign client-side and POST signedTx' +
            (ALLOW_CUSTODIAL_PROXY ? '' : ' (set ALLOW_CUSTODIAL_PROXY=1 for private backends)'),
        ),
        { code: 'USER_REJECTED' },
      );
    }
    if (creds.evmSignature && !ALLOW_CUSTODIAL_PROXY) {
      throw Object.assign(
        new Error(
          'proxy mode rejects evmSignature (key material) — submit signedTx, or set ALLOW_CUSTODIAL_PROXY=1',
        ),
        { code: 'USER_REJECTED' },
      );
    }
  }

  async function connectEphemeral(creds: Creds): Promise<SplendorPQBridge> {
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
    } else if (creds.evmSignature) {
      await tmp.connectFromEvmSignature(creds.evmSignature);
    } else if (creds.evmPrivateKey && MODE === 'service-wallet') {
      await tmp.connectFromEvmPrivateKey(creds.evmPrivateKey);
    } else {
      throw Object.assign(
        new Error(
          MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY
            ? 'proxy requires signedTx (custodial credentials disabled)'
            : 'provide seed or evmSignature' +
                (MODE === 'service-wallet' ? ' or evmPrivateKey' : ''),
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
    } else if (SERVICE_EVM_KEY) {
      await bridge.connectFromEvmPrivateKey(SERVICE_EVM_KEY);
      if (logger) {
        console.log(`[gateway] service-wallet from SERVICE_EVM_KEY → ${bridge.getAddress()}`);
      }
    } else {
      throw new Error('MODE=service-wallet requires SERVICE_SEED or SERVICE_EVM_KEY');
    }
    if (!API_KEY && ALLOW_INSECURE && logger) {
      console.warn(
        '[gateway] WARNING: service-wallet running with ALLOW_INSECURE — set GATEWAY_API_KEY in production',
      );
    }
  } else if (logger) {
    console.log('[gateway] MODE=proxy (non-custodial — clients sign, gateway relays signedTx)');
    if (ALLOW_CUSTODIAL_PROXY) {
      console.warn('[gateway] ALLOW_CUSTODIAL_PROXY=1 — seed/evmSignature accepted (private use only)');
    }
    console.log(`[gateway] SQDP-${SQDP_VERSION} network=${net.id} rpc=${bridgeRpcUrls.join(',')}`);
  }

  const TLS_CERT = overrides.tlsCertFile ?? process.env.TLS_CERT_FILE ?? '';
  const TLS_KEY = overrides.tlsKeyFile ?? process.env.TLS_KEY_FILE ?? '';
  const https = buildHttpsOptions(TLS_CERT, TLS_KEY, logger);

  const app = Fastify({
    logger,
    bodyLimit: BODY_LIMIT,
    trustProxy: TRUST_PROXY,
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
    if (MODE === 'service-wallet' && API_KEY) {
      const path = req.url.split('?')[0];
      if (path === '/health' || path === '/ready') return;
      const err = requireApiKey(req.headers as Record<string, string | string[] | undefined>);
      if (err) {
        return reply.code(401).send(err);
      }
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
    Body: { evmSignature?: string; seed?: string; evmPrivateKey?: string };
  }>(
    '/v1/derive',
    {
      ...txRateLimit,
      schema: { body: deriveBodySchema },
    },
    async (req, reply) => {
      let tmp: SplendorPQBridge | null = null;
      try {
        if (MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY) {
          return reply.code(403).send(
            apiError(
              'USER_REJECTED',
              'proxy /v1/derive disabled — derive client-side with WASM (or set ALLOW_CUSTODIAL_PROXY=1)',
            ),
          );
        }
        tmp = await connectEphemeral(req.body || {});
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
