/**
 * Gateway security table tests (Fastify inject — no listen).
 * Run from sdk/bridge: npm run test:gateway
 */
import { afterEach, describe, expect, it } from 'vitest';
import { buildServer, safeEqualString, type BuiltGateway } from './server.ts';

const GOLDEN_SEED = '01'.repeat(32);

let gw: BuiltGateway | null = null;

afterEach(async () => {
  if (gw) {
    await gw.close();
    gw = null;
  }
});

describe('gateway safeEqualString', () => {
  it('matches equal strings', () => {
    expect(safeEqualString('abc', 'abc')).toBe(true);
  });
  it('rejects unequal same-length', () => {
    expect(safeEqualString('abc', 'abd')).toBe(false);
  });
  it('rejects length mismatch', () => {
    expect(safeEqualString('ab', 'abc')).toBe(false);
  });
});

describe('gateway proxy custodial rejection', () => {
  it('rejects seed / evmPrivateKey / evmSignature on /v1/tx/transfer', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      logger: false,
      listen: false,
      rateMax: 10_000,
    });

    for (const body of [
      { seed: GOLDEN_SEED, to: 'SQ:' + 'ab'.repeat(32), amount: '1' },
      { evmPrivateKey: '11'.repeat(32), to: 'SQ:' + 'ab'.repeat(32), amount: '1' },
      { evmSignature: 'aa'.repeat(65), to: 'SQ:' + 'ab'.repeat(32), amount: '1' },
    ]) {
      const res = await gw.app.inject({
        method: 'POST',
        url: '/v1/tx/transfer',
        payload: body,
      });
      expect(res.statusCode).toBe(403);
      const json = res.json() as { code: string; message: string };
      expect(json.code).toBe('USER_REJECTED');
    }
  }, 60_000);

  it('disables /v1/derive in public proxy', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      logger: false,
      listen: false,
      rateMax: 10_000,
    });
    const res = await gw.app.inject({
      method: 'POST',
      url: '/v1/derive',
      payload: { seed: GOLDEN_SEED },
    });
    expect(res.statusCode).toBe(403);
    expect(res.json()).toMatchObject({ code: 'USER_REJECTED' });
    expect(String((res.json() as { message: string }).message)).toMatch(/derive disabled/i);
  }, 60_000);

  it('accepts signedTx path shape (fails on RPC, not custodial reject)', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      rpcUrl: 'http://127.0.0.1:1', // guaranteed closed
      logger: false,
      listen: false,
      rateMax: 10_000,
    });
    const res = await gw.app.inject({
      method: 'POST',
      url: '/v1/tx/submit',
      payload: {
        signedTx: {
          chain_id: gw.config.chainId,
          type: 'transfer',
          amount: '1',
        },
      },
    });
    const json = res.json() as { code: string; message: string };
    expect(json.message || '').not.toMatch(/rejects ML-DSA-65 seed|rejects evm/i);
    // Upstream network/timeout → 502/504 (SQDP code retained in body).
    expect([502, 504]).toContain(res.statusCode);
  }, 60_000);
});

describe('gateway service-wallet API key', () => {
  it('throws on buildServer without API key unless allowInsecure', async () => {
    await expect(
      buildServer({
        mode: 'service-wallet',
        serviceSeed: GOLDEN_SEED,
        logger: false,
        listen: false,
        rateMax: 10_000,
      }),
    ).rejects.toThrow(/GATEWAY_API_KEY|ALLOW_INSECURE/i);

    gw = await buildServer({
      mode: 'service-wallet',
      serviceSeed: GOLDEN_SEED,
      allowInsecure: true,
      logger: false,
      listen: false,
      rateMax: 10_000,
    });
    expect(gw.config.mode).toBe('service-wallet');
  }, 60_000);

  it('rejects CORS * for service-wallet', async () => {
    await expect(
      buildServer({
        mode: 'service-wallet',
        serviceSeed: GOLDEN_SEED,
        apiKey: 'correct-key-32chars-xxxxxxxxxxxx',
        corsOrigins: ['*'],
        logger: false,
        listen: false,
        rateMax: 10_000,
      }),
    ).rejects.toThrow(/CORS origin/i);
  }, 60_000);

  it('returns 401 on missing or wrong API key', async () => {
    gw = await buildServer({
      mode: 'service-wallet',
      serviceSeed: GOLDEN_SEED,
      apiKey: 'correct-key-32chars-xxxxxxxxxxxx',
      logger: false,
      listen: false,
      rateMax: 10_000,
    });

    const missing = await gw.app.inject({ method: 'GET', url: '/v1/status' });
    expect(missing.statusCode).toBe(401);

    const wrong = await gw.app.inject({
      method: 'GET',
      url: '/v1/status',
      headers: { 'x-api-key': 'wrong-key-32chars-xxxxxxxxxxxxx' },
    });
    expect(wrong.statusCode).toBe(401);

    const health = await gw.app.inject({ method: 'GET', url: '/health' });
    expect(health.statusCode).toBe(200);
  }, 60_000);
});

describe('gateway body limit and rate limit', () => {
  it('rejects oversized body with 413', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      bodyLimit: 256,
      logger: false,
      listen: false,
      rateMax: 10_000,
      rateTxMax: 10_000,
    });
    const big = 'x'.repeat(2000);
    const res = await gw.app.inject({
      method: 'POST',
      url: '/v1/tx/submit',
      headers: { 'content-type': 'application/json' },
      payload: JSON.stringify({
        signedTx: { chain_id: gw.config.chainId, pad: big },
      }),
    });
    expect(res.statusCode).toBe(413);
  }, 60_000);

  it('returns 429 under burst over rate limit', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      rateMax: 3,
      rateTxMax: 3,
      logger: false,
      listen: false,
    });
    let saw429 = false;
    for (let i = 0; i < 20; i++) {
      const res = await gw.app.inject({ method: 'GET', url: '/health' });
      if (res.statusCode === 429) {
        saw429 = true;
        break;
      }
    }
    expect(saw429).toBe(true);
  }, 60_000);

  it('applies stricter per-route limit on /v1/tx/submit', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      rateMax: 10_000,
      rateTxMax: 2,
      logger: false,
      listen: false,
      rpcUrl: 'http://127.0.0.1:1',
    });
    let saw429 = false;
    for (let i = 0; i < 10; i++) {
      const res = await gw.app.inject({
        method: 'POST',
        url: '/v1/tx/submit',
        payload: {
          signedTx: { chain_id: gw.config.chainId, type: 'transfer', amount: '1' },
        },
      });
      if (res.statusCode === 429) {
        saw429 = true;
        break;
      }
    }
    expect(saw429).toBe(true);
  }, 60_000);
});

describe('gateway custodial + CORS and chain_id', () => {
  it('refuses ALLOW_CUSTODIAL_PROXY with CORS *', async () => {
    await expect(
      buildServer({
        mode: 'proxy',
        allowCustodialProxy: true,
        corsOrigins: ['*'],
        logger: false,
        listen: false,
        rateMax: 10_000,
      }),
    ).rejects.toThrow(/ALLOW_CUSTODIAL_PROXY.*\*/i);
  }, 60_000);

  it('rejects signedTx missing chain_id', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      logger: false,
      listen: false,
      rateMax: 10_000,
      rateTxMax: 10_000,
      rpcUrl: 'http://127.0.0.1:1',
    });
    const res = await gw.app.inject({
      method: 'POST',
      url: '/v1/tx/submit',
      payload: { signedTx: { type: 'transfer', amount: '1' } },
    });
    expect(res.statusCode).toBe(400);
    expect(res.json()).toMatchObject({ code: 'CHAIN_MISMATCH' });
  }, 60_000);

  it('rejects unknown body fields via schema', async () => {
    gw = await buildServer({
      mode: 'proxy',
      allowCustodialProxy: false,
      logger: false,
      listen: false,
      rateMax: 10_000,
      rateTxMax: 10_000,
    });
    const res = await gw.app.inject({
      method: 'POST',
      url: '/v1/tx/transfer',
      payload: { to: 'SQ:' + 'ab'.repeat(32), amount: '1', mnemonic: 'leak' },
    });
    expect(res.statusCode).toBe(400);
  }, 60_000);

  it('defaults trustProxy to false', async () => {
    gw = await buildServer({
      mode: 'proxy',
      logger: false,
      listen: false,
      rateMax: 10_000,
    });
    expect(gw.config.trustProxy).toBe(false);
  }, 60_000);

  it('/ready returns 503 when upstream chain_id probe fails', async () => {
    gw = await buildServer({
      mode: 'proxy',
      rpcUrl: 'http://127.0.0.1:1',
      logger: false,
      listen: false,
      rateMax: 10_000,
    });
    const res = await gw.app.inject({ method: 'GET', url: '/ready' });
    expect(res.statusCode).toBe(503);
    expect(res.json()).toMatchObject({ ready: false, ok: false });
  }, 60_000);
});
