/**
 * Separately deployable PQ bridge gateway.
 * Modes: proxy (non-custodial relay) | service-wallet (holds derived PQ key).
 *
 * Each request that signs uses its own SplendorPQBridge session (WASM multi-session).
 * In proxy mode, evmPrivateKey is rejected on public routes.
 */
import Fastify from 'fastify';
import { SplendorPQBridge, fromSPLD } from '../dist/index.js';

const PORT = Number(process.env.PORT || 3090);
const SQC_RPC = process.env.SQC_RPC || 'http://localhost:8080';
const MODE = (process.env.MODE || 'proxy').toLowerCase(); // proxy | service-wallet
const SERVICE_SEED = process.env.SERVICE_SEED || '';
const SERVICE_EVM_KEY = process.env.SERVICE_EVM_KEY || '';

type Creds = {
  seed?: string;
  evmSignature?: string;
  evmPrivateKey?: string;
};

async function connectEphemeral(creds: Creds): Promise<SplendorPQBridge> {
  if (MODE === 'proxy' && creds.evmPrivateKey) {
    throw new Error(
      'proxy mode rejects evmPrivateKey — use signedTx, seed, or evmSignature',
    );
  }
  const tmp = new SplendorPQBridge({ rpcUrl: SQC_RPC });
  await tmp.init();
  if (creds.seed) {
    await tmp.connectFromSeed(creds.seed);
  } else if (creds.evmSignature) {
    await tmp.connectFromEvmSignature(creds.evmSignature);
  } else if (creds.evmPrivateKey && MODE === 'service-wallet') {
    await tmp.connectFromEvmPrivateKey(creds.evmPrivateKey);
  } else {
    throw new Error('provide seed or evmSignature' + (MODE === 'service-wallet' ? ' or evmPrivateKey' : ''));
  }
  return tmp;
}

async function main() {
  const bridge = new SplendorPQBridge({ rpcUrl: SQC_RPC });
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
  }

  const app = Fastify({ logger: true });

  app.get('/health', async () => ({
    ok: true,
    mode: MODE,
    sqc_rpc: SQC_RPC,
    service_address:
      MODE === 'service-wallet' && bridge.getConnectionState() === 'connected'
        ? bridge.getAddress()
        : null,
  }));

  app.get('/v1/status', async () => bridge.getStatus());

  app.get<{ Params: { address: string } }>('/v1/balance/:address', async (req) => {
    return bridge.rpc.getBalance(req.params.address);
  });

  app.get<{ Params: { address: string } }>('/v1/nonce/:address', async (req) => {
    return bridge.rpc.getNonce(req.params.address);
  });

  app.post<{
    Body: { evmSignature?: string; seed?: string; evmPrivateKey?: string };
  }>('/v1/derive', async (req, reply) => {
    let tmp: SplendorPQBridge | null = null;
    try {
      if (MODE === 'proxy' && req.body.evmPrivateKey && !req.body.seed && !req.body.evmSignature) {
        return reply.code(400).send({
          error: 'proxy mode rejects evmPrivateKey — use seed or evmSignature',
        });
      }
      tmp = await connectEphemeral(req.body);
      const info = tmp.getDeriveInfo();
      tmp.disconnect();
      return info;
    } catch (e: any) {
      tmp?.disconnect();
      return reply.code(400).send({ error: e.message });
    }
  });

  app.post<{ Body: { signedTx: object | string } }>('/v1/tx/submit', async (req, reply) => {
    try {
      return await bridge.rpc.submitTransaction(req.body.signedTx);
    } catch (e: any) {
      return reply.code(400).send({ error: e.message });
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
        return reply.code(400).send({ error: 'to is required' });
      }
      const amount =
        req.body.amountSPLD != null ? fromSPLD(req.body.amountSPLD) : req.body.amount;
      if (!amount) {
        return reply.code(400).send({ error: 'amount or amountSPLD required' });
      }

      const result = await signer.sendTransfer({
        to: req.body.to,
        amount,
        fee: req.body.fee,
        memo: req.body.memo,
      });
      ephemeral?.disconnect();
      return result;
    } catch (e: any) {
      ephemeral?.disconnect();
      return reply.code(400).send({ error: e.message });
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
      return result;
    } catch (e: any) {
      ephemeral?.disconnect();
      return reply.code(400).send({ error: e.message });
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
    } catch (e: any) {
      ephemeral?.disconnect();
      return reply.code(400).send({ error: e.message });
    }
  });

  await app.listen({ port: PORT, host: '0.0.0.0' });
  console.log(`[gateway] listening on :${PORT} mode=${MODE} rpc=${SQC_RPC}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
