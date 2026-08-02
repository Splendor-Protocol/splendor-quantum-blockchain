/**
 * Separately deployable PQ bridge gateway.
 * Modes: proxy (non-custodial relay) | service-wallet (holds derived PQ key).
 */
import Fastify from 'fastify';
import { SplendorPQBridge, fromSPLD } from '../dist/index.js';

const PORT = Number(process.env.PORT || 3090);
const SQC_RPC = process.env.SQC_RPC || 'http://localhost:8080';
const MODE = (process.env.MODE || 'proxy').toLowerCase(); // proxy | service-wallet
const SERVICE_SEED = process.env.SERVICE_SEED || '';
const SERVICE_EVM_KEY = process.env.SERVICE_EVM_KEY || '';

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
    service_address: MODE === 'service-wallet' ? bridge.getAddress() : null,
  }));

  app.get('/v1/status', async () => bridge.getStatus());

  app.get<{ Params: { address: string } }>('/v1/balance/:address', async (req) => {
    return bridge.rpc.getBalance(req.params.address);
  });

  app.get<{ Params: { address: string } }>('/v1/nonce/:address', async (req) => {
    return bridge.rpc.getNonce(req.params.address);
  });

  /** Derive PQ address from EVM signature or seed (does not unlock gateway wallet in proxy mode). */
  app.post<{
    Body: { evmSignature?: string; seed?: string; evmPrivateKey?: string };
  }>('/v1/derive', async (req, reply) => {
    const tmp = new SplendorPQBridge({ rpcUrl: SQC_RPC });
    await tmp.init();
    try {
      let info;
      if (req.body.seed) {
        info = await tmp.connectFromSeed(req.body.seed);
      } else if (req.body.evmSignature) {
        info = await tmp.connectFromEvmSignature(req.body.evmSignature);
      } else if (req.body.evmPrivateKey) {
        if (MODE !== 'service-wallet') {
          // Allow one-shot derive without persisting on gateway
          info = await tmp.connectFromEvmPrivateKey(req.body.evmPrivateKey);
        } else {
          info = await tmp.connectFromEvmPrivateKey(req.body.evmPrivateKey);
        }
      } else {
        return reply.code(400).send({
          error: 'Provide evmSignature, seed, or evmPrivateKey',
        });
      }
      tmp.lock();
      return info;
    } catch (e: any) {
      return reply.code(400).send({ error: e.message });
    }
  });

  /** Submit a pre-signed wire transaction (proxy mode). */
  app.post<{ Body: { signedTx: object | string } }>('/v1/tx/submit', async (req, reply) => {
    try {
      return await bridge.rpc.submitTransaction(req.body.signedTx);
    } catch (e: any) {
      return reply.code(400).send({ error: e.message });
    }
  });

  /**
   * Build+sign+submit transfer.
   * service-wallet: uses gateway key.
   * proxy: requires signedTx OR (seed/evmSignature + to/amount) for one-shot.
   */
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
    try {
      if (req.body.signedTx) {
        return await bridge.rpc.submitTransaction(req.body.signedTx);
      }

      let signer = bridge;
      let ephemeral: SplendorPQBridge | null = null;

      if (MODE === 'proxy') {
        ephemeral = new SplendorPQBridge({ rpcUrl: SQC_RPC });
        await ephemeral.init();
        if (req.body.seed) await ephemeral.connectFromSeed(req.body.seed);
        else if (req.body.evmSignature)
          await ephemeral.connectFromEvmSignature(req.body.evmSignature);
        else if (req.body.evmPrivateKey)
          await ephemeral.connectFromEvmPrivateKey(req.body.evmPrivateKey);
        else {
          return reply.code(400).send({
            error:
              'proxy mode: provide signedTx, or seed/evmSignature/evmPrivateKey with to+amount',
          });
        }
        signer = ephemeral;
      }

      if (!req.body.to) {
        return reply.code(400).send({ error: 'to is required' });
      }
      const amount =
        req.body.amountSPLD != null
          ? fromSPLD(req.body.amountSPLD)
          : req.body.amount;
      if (!amount) {
        return reply.code(400).send({ error: 'amount or amountSPLD required' });
      }

      const result = await signer.sendTransfer({
        to: req.body.to,
        amount,
        fee: req.body.fee,
        memo: req.body.memo,
      });
      ephemeral?.lock();
      return result;
    } catch (e: any) {
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
    try {
      if (req.body.signedTx) {
        return await bridge.rpc.submitTransaction(req.body.signedTx);
      }
      let signer = bridge;
      let ephemeral: SplendorPQBridge | null = null;
      if (MODE === 'proxy') {
        ephemeral = new SplendorPQBridge({ rpcUrl: SQC_RPC });
        await ephemeral.init();
        if (req.body.seed) await ephemeral.connectFromSeed(req.body.seed);
        else if (req.body.evmSignature)
          await ephemeral.connectFromEvmSignature(req.body.evmSignature);
        else if (req.body.evmPrivateKey)
          await ephemeral.connectFromEvmPrivateKey(req.body.evmPrivateKey);
        else {
          return reply.code(400).send({ error: 'proxy: need credentials or signedTx' });
        }
        signer = ephemeral;
      }
      const calldata = req.body.calldata_hex || req.body.calldata_b64 || '';
      const result = await signer.callContract({
        contract: req.body.contract,
        calldata,
        value: req.body.value,
        fee: req.body.fee,
      });
      ephemeral?.lock();
      return result;
    } catch (e: any) {
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
    try {
      let signer = bridge;
      let ephemeral: SplendorPQBridge | null = null;
      if (MODE === 'proxy') {
        ephemeral = new SplendorPQBridge({ rpcUrl: SQC_RPC });
        await ephemeral.init();
        if (req.body.seed) await ephemeral.connectFromSeed(req.body.seed);
        else if (req.body.evmSignature)
          await ephemeral.connectFromEvmSignature(req.body.evmSignature);
        else if (req.body.evmPrivateKey)
          await ephemeral.connectFromEvmPrivateKey(req.body.evmPrivateKey);
        else {
          return reply.code(400).send({ error: 'proxy: need credentials' });
        }
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
      ephemeral?.lock();
      return result;
    } catch (e: any) {
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
