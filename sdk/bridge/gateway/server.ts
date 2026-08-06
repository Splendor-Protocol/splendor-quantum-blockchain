/**
 * Separately deployable PQ bridge gateway.
 * Modes: proxy (non-custodial relay) | service-wallet (holds derived PQ key).
 *
 * Network defaults from sqc.networks.json (override with SQC_NETWORK / SQC_RPC / PORT).
 * Errors follow SQDP-1: { code, message }.
 */
import Fastify from 'fastify';
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
  if (/proxy mode rejects evmPrivateKey/i.test(message)) {
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

/** Posting seed to a public gateway is custodial for that request (SQDP-1). */
const SEED_WARNING =
  'Posting Dilithium seed to a gateway is custodial for this request. Prefer client-side WASM + signedTx on public deployments.';

async function connectEphemeral(creds: Creds): Promise<SplendorPQBridge> {
  if (MODE === 'proxy' && creds.evmPrivateKey) {
    throw Object.assign(new Error(
      'proxy mode rejects evmPrivateKey — use signedTx, seed, or evmSignature',
    ), { code: 'USER_REJECTED' });
  }
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
      'provide seed or evmSignature' + (MODE === 'service-wallet' ? ' or evmPrivateKey' : ''),
    );
  }
  return tmp;
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
  } else {
    console.log('[gateway] MODE=proxy (non-custodial — clients sign, gateway relays)');
    console.log(`[gateway] SQDP-${SQDP_VERSION} network=${net.id} rpc=${SQC_RPC}`);
  }

  const app = Fastify({ logger: true });

  app.get('/health', async () => ({
    ok: true,
    mode: MODE,
    network: net.id,
    sqc_rpc: SQC_RPC,
    sqdp: SQDP_VERSION,
    protocol: {
      chainId: PROTOCOL.chainId,
      confirmation: PROTOCOL.confirmation,
      minFee: PROTOCOL.minFee,
    },
    service_address:
      MODE === 'service-wallet' && bridge.getConnectionState() === 'connected'
        ? bridge.getAddress()
        : null,
  }));

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
      if (MODE === 'proxy' && req.body.evmPrivateKey && !req.body.seed && !req.body.evmSignature) {
        return reply.code(400).send(
          apiError('USER_REJECTED', 'proxy mode rejects evmPrivateKey — use seed or evmSignature'),
        );
      }
      tmp = await connectEphemeral(req.body);
      const info = tmp.getDeriveInfo();
      tmp.disconnect();
      const out: Record<string, unknown> = { ...info };
      if (req.body.seed && MODE === 'proxy') {
        out.warning = SEED_WARNING;
      }
      return out;
    } catch (e: any) {
      tmp?.disconnect();
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  app.post<{ Body: { signedTx: object | string } }>('/v1/tx/submit', async (req, reply) => {
    try {
      return await bridge.rpc.submitTransaction(req.body.signedTx);
    } catch (e: any) {
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
      if (req.body.seed && MODE === 'proxy') {
        return { ...result, warning: SEED_WARNING };
      }
      return result;
    } catch (e: any) {
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
      if (req.body.seed && MODE === 'proxy') {
        return { ...result, warning: SEED_WARNING };
      }
      return result;
    } catch (e: any) {
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
      if (req.body.seed && MODE === 'proxy') {
        return { ...result, warning: SEED_WARNING };
      }
      return result;
    } catch (e: any) {
      ephemeral?.disconnect();
      return reply.code(400).send(errorFromCaught(e));
    }
  });

  await app.listen({ port: PORT, host: '0.0.0.0' });
  console.log(`[gateway] listening on :${PORT} mode=${MODE} network=${net.id} rpc=${SQC_RPC}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
