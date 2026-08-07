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
import { timingSafeEqual } from 'node:crypto';
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

export type GatewayConfig = {
  networkId?: string;
  rpcUrl?: string;
  mode?: GatewayMode;
  serviceSeed?: string;
  serviceEvmKey?: string;
  allowCustodialProxy?: boolean;
  apiKey?: string;
  corsOrigins?: string[];
  publicHealth?: boolean;
  rateMax?: number;
  bodyLimit?: number;
  /** When false, skip listen (for inject tests). Default true in main(). */
  listen?: boolean;
  port?: number;
  logger?: boolean;
};

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
  if (/proxy mode rejects|custodial credentials/i.test(message)) {
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

/** Constant-time string compare; length mismatch is always a fail. */
export function safeEqualString(a: string, b: string): boolean {
  const ba = Buffer.from(a, 'utf8');
  const bb = Buffer.from(b, 'utf8');
  if (ba.length !== bb.length) return false;
  return timingSafeEqual(ba, bb);
}

function configFromEnv(): GatewayConfig {
  const net = resolveNetwork(process.env.SQC_NETWORK || process.env.NETWORK || undefined);
  return {
    networkId: process.env.SQC_NETWORK || process.env.NETWORK || undefined,
    rpcUrl: (process.env.SQC_RPC || net.rpcUrls[0]).replace(/\/$/, ''),
    mode: ((process.env.MODE || 'proxy').toLowerCase() as GatewayMode),
    serviceSeed: process.env.SERVICE_SEED || '',
    serviceEvmKey: process.env.SERVICE_EVM_KEY || '',
    allowCustodialProxy: process.env.ALLOW_CUSTODIAL_PROXY === '1',
    apiKey: process.env.GATEWAY_API_KEY || '',
    corsOrigins: (process.env.CORS_ORIGINS || '*')
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
    publicHealth: process.env.PUBLIC_HEALTH !== '0',
    rateMax: Number(process.env.RATE_LIMIT_MAX || 120),
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
      | 'bodyLimit'
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
  const net = resolveNetwork(overrides.networkId ?? env.networkId);
  const MODE = (overrides.mode ?? env.mode ?? 'proxy').toLowerCase() as GatewayMode;
  const SQC_RPC = (overrides.rpcUrl ?? env.rpcUrl ?? net.rpcUrls[0]).replace(/\/$/, '');
  const ALLOW_CUSTODIAL_PROXY =
    overrides.allowCustodialProxy ?? env.allowCustodialProxy ?? false;
  const API_KEY = overrides.apiKey ?? env.apiKey ?? '';
  const SERVICE_SEED = overrides.serviceSeed ?? env.serviceSeed ?? '';
  const SERVICE_EVM_KEY = overrides.serviceEvmKey ?? env.serviceEvmKey ?? '';
  const CORS_ORIGINS = overrides.corsOrigins ?? env.corsOrigins ?? ['*'];
  const PUBLIC_HEALTH = overrides.publicHealth ?? env.publicHealth ?? true;
  const RATE_MAX = overrides.rateMax ?? env.rateMax ?? 120;
  const BODY_LIMIT = overrides.bodyLimit ?? env.bodyLimit ?? 1_048_576;
  const logger = overrides.logger ?? false;

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
          'proxy mode rejects Dilithium seed — sign client-side and POST signedTx' +
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
      rpcUrl: SQC_RPC,
      chainId: net.chainId,
      timeoutMs: net.timeoutMs,
      retries: net.retries,
    });
    await tmp.init();
    if (creds.seed) {
      await tmp.connectFromSeed(creds.seed);
    } else if (creds.evmSignature) {
      await tmp.connectFromEvmSignature(creds.evmSignature);
    } else if (creds.evmPrivateKey && MODE === 'service-wallet') {
      await tmp.connectFromEvmPrivateKey(creds.evmPrivateKey);
    } else {
      throw new Error(
        MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY
          ? 'proxy requires signedTx (custodial credentials disabled)'
          : 'provide seed or evmSignature' +
              (MODE === 'service-wallet' ? ' or evmPrivateKey' : ''),
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
    rpcUrl: SQC_RPC,
    chainId: net.chainId,
    timeoutMs: net.timeoutMs,
    retries: net.retries,
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
    if (!API_KEY && logger) {
      console.warn(
        '[gateway] WARNING: service-wallet without GATEWAY_API_KEY — set GATEWAY_API_KEY in production',
      );
    }
  } else if (logger) {
    console.log('[gateway] MODE=proxy (non-custodial — clients sign, gateway relays signedTx)');
    if (ALLOW_CUSTODIAL_PROXY) {
      console.warn('[gateway] ALLOW_CUSTODIAL_PROXY=1 — seed/evmSignature accepted (private use only)');
    }
    console.log(`[gateway] SQDP-${SQDP_VERSION} network=${net.id} rpc=${SQC_RPC}`);
  }

  const app = Fastify({
    logger,
    bodyLimit: BODY_LIMIT,
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

  app.addHook('onRequest', async (req, reply) => {
    if (MODE === 'service-wallet' && API_KEY) {
      const path = req.url.split('?')[0];
      if (path === '/health') return;
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
        body.sqc_rpc = SQC_RPC;
      }
    }
    return body;
  });

  app.get('/v1/status', async () => bridge.getStatus());

  app.get<{ Params: { address: string } }>('/v1/balance/:address', async (req, reply) => {
    try {
      return await bridge.rpc.getBalance(req.params.address);
    } catch (e) {
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  app.get<{ Params: { address: string } }>('/v1/nonce/:address', async (req, reply) => {
    try {
      return await bridge.rpc.getNonce(req.params.address);
    } catch (e) {
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  app.post<{
    Body: { evmSignature?: string; seed?: string; evmPrivateKey?: string };
  }>('/v1/derive', async (req, reply) => {
    let tmp: SplendorPQBridge | null = null;
    try {
      if (MODE === 'proxy' && !ALLOW_CUSTODIAL_PROXY) {
        return reply.code(400).send(
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
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  app.post<{ Body: { signedTx: object | string } }>('/v1/tx/submit', async (req, reply) => {
    try {
      await bridge.rpc.assertChainId();
      return await bridge.rpc.submitTransaction(req.body.signedTx);
    } catch (e: unknown) {
      return reply.code(400).send(errorFromCaught(e));
    }
  });

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
  }>('/v1/tx/transfer', async (req, reply) => {
    let ephemeral: SplendorPQBridge | null = null;
    try {
      if (req.body.signedTx) {
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
      return reply.code(400).send(errorFromCaught(e));
    }
  });

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
  }>('/v1/tx/call', async (req, reply) => {
    let ephemeral: SplendorPQBridge | null = null;
    try {
      if (req.body.signedTx) {
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
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  app.post<{
    Body: {
      to: string;
      amount?: string;
      amountSPLD?: string;
      requirements?: object;
      settle?: boolean;
      seed?: string;
      evmSignature?: string;
      evmPrivateKey?: string;
    };
  }>('/v1/x402/pay', async (req, reply) => {
    let ephemeral: SplendorPQBridge | null = null;
    try {
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
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  const shouldListen = overrides.listen ?? false;
  if (shouldListen) {
    const port = overrides.port ?? env.port ?? net.gatewayPort;
    await app.listen({ port, host: '0.0.0.0' });
    if (logger) {
      console.log(`[gateway] listening on :${port} mode=${MODE} network=${net.id}`);
    }
  }

  return {
    app,
    bridge,
    config: {
      networkId: net.id,
      chainId: net.chainId,
      rpcUrl: SQC_RPC,
      mode: MODE,
      allowCustodialProxy: ALLOW_CUSTODIAL_PROXY,
      apiKey: API_KEY,
      corsOrigins: CORS_ORIGINS,
      publicHealth: PUBLIC_HEALTH,
      rateMax: RATE_MAX,
      bodyLimit: BODY_LIMIT,
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
