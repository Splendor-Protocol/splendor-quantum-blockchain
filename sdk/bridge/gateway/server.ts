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
import Fastify from 'fastify';
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

const NETWORK_ID = process.env.SQC_NETWORK || process.env.NETWORK || undefined;
const net = resolveNetwork(NETWORK_ID);
const PORT = Number(process.env.PORT || net.gatewayPort);
const SQC_RPC = (process.env.SQC_RPC || net.rpcUrls[0]).replace(/\/$/, '');
const MODE = (process.env.MODE || 'proxy').toLowerCase(); // proxy | service-wallet
const SERVICE_SEED = process.env.SERVICE_SEED || '';
const SERVICE_EVM_KEY = process.env.SERVICE_EVM_KEY || '';
/** Escape hatch: allow seed/evmSignature on proxy (private backends only). */
const ALLOW_CUSTODIAL_PROXY = process.env.ALLOW_CUSTODIAL_PROXY === '1';
/** Required for service-wallet mutating routes when set. */
const API_KEY = process.env.GATEWAY_API_KEY || '';
const CORS_ORIGINS = (process.env.CORS_ORIGINS || '*')
  .split(',')
  .map((s) => s.trim())
  .filter(Boolean);
const PUBLIC_HEALTH = process.env.PUBLIC_HEALTH !== '0';
const RATE_MAX = Number(process.env.RATE_LIMIT_MAX || 120);
const BODY_LIMIT = Number(process.env.BODY_LIMIT_BYTES || 1_048_576);

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

/**
 * Public proxy MUST reject custodial credentials unless ALLOW_CUSTODIAL_PROXY=1.
 * Prefer client-side WASM + signedTx.
 */
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
  if (token !== API_KEY) {
    return apiError('USER_REJECTED', 'invalid or missing API key');
  }
  return null;
}

async function main() {
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
      console.log(`[gateway] service-wallet from SERVICE_SEED → ${bridge.getAddress()}`);
    } else if (SERVICE_EVM_KEY) {
      await bridge.connectFromEvmPrivateKey(SERVICE_EVM_KEY);
      console.log(`[gateway] service-wallet from SERVICE_EVM_KEY → ${bridge.getAddress()}`);
    } else {
      console.error('MODE=service-wallet requires SERVICE_SEED or SERVICE_EVM_KEY');
      process.exit(1);
    }
    if (!API_KEY) {
      console.warn(
        '[gateway] WARNING: service-wallet without GATEWAY_API_KEY — set GATEWAY_API_KEY in production',
      );
    }
  } else {
    console.log('[gateway] MODE=proxy (non-custodial — clients sign, gateway relays signedTx)');
    if (ALLOW_CUSTODIAL_PROXY) {
      console.warn('[gateway] ALLOW_CUSTODIAL_PROXY=1 — seed/evmSignature accepted (private use only)');
    }
    console.log(`[gateway] SQDP-${SQDP_VERSION} network=${net.id} rpc=${SQC_RPC}`);
  }

  const app = Fastify({
    logger: true,
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
      // Internal RPC URL only when not locked down; public proxy hides it by default via PUBLIC_HEALTH=0
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

  await app.listen({ port: PORT, host: '0.0.0.0' });
  console.log(`[gateway] listening on :${PORT} mode=${MODE} network=${net.id}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
