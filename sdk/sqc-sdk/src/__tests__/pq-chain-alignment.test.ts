import { describe, expect, it, afterEach, beforeEach, vi } from 'vitest';
import { SplendorPQBridge } from '../index.js';
import { MODULE_ROUTER_ADDRESS } from '../addresses/index.js';
import {
  assertQSolSignature,
  encodeFunctionData,
} from '../abi.js';
import { BridgeError } from '../errors.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const GOLDEN_SEED = golden.seedHex;
const bridges: SplendorPQBridge[] = [];
let originalFetch: typeof fetch;
let mockRemoteChainId = golden.chainId;

beforeEach(() => {
  originalFetch = globalThis.fetch;
  mockRemoteChainId = golden.chainId;
  // Off-chain sign paths now assert remote chain_id — stub /status for unit tests.
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/status')) {
      return new Response(JSON.stringify({ chain_id: mockRemoteChainId }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response(JSON.stringify({ error: 'unexpected' }), { status: 500 });
  }) as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  for (const b of bridges) {
    try {
      b.disconnect();
    } catch {
      /* ignore */
    }
  }
  bridges.length = 0;
});

function track(b: SplendorPQBridge) {
  bridges.push(b);
  return b;
}

describe('PQ chain alignment', () => {
  it('pins WASM module router to Splendor PQ constant', async () => {
    const b = track(new SplendorPQBridge());
    await b.init();
    expect(b.getModuleRouterAddress()).toBe(MODULE_ROUTER_ADDRESS);
    expect(b.getModuleRouterAddress()).toBe(golden.moduleRouterAddress);
  }, 30_000);

  it('syncs WASM chain_id with RPC options', async () => {
    const devnetId = 'splendor-quantum-devnet-1';
    mockRemoteChainId = devnetId;
    const b = track(new SplendorPQBridge({ chainId: devnetId }));
    await b.connectFromSeed(GOLDEN_SEED);
    const signed = await b.signPermit({
      token: golden.permit.contract,
      spender: golden.permit.spender,
      amount: golden.permit.amount,
      nonce: golden.permit.nonce,
      deadline: golden.permit.deadline,
    });
    expect(signed.chain_id).toBe(devnetId);
  }, 30_000);

  it('rejects Solidity address in QSol ABI signatures', () => {
    expect(() => assertQSolSignature('transfer(address,uint256)')).toThrow(BridgeError);
    expect(() =>
      encodeFunctionData('transfer(address,uint256)', [
        'SQ:' + 'ab'.repeat(32),
        1n,
      ]),
    ).toThrow(BridgeError);
  });

  it('rejects EVM 0x addresses as ABI args', () => {
    expect(() =>
      encodeFunctionData('transfer(sqaddress,uint256)', [
        '0x' + 'ab'.repeat(20),
        1n,
      ]),
    ).toThrow(BridgeError);
  });

  it('signOrder matches golden fromGoldenSeed hash', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const o = golden.order;
    const signed = await b.signOrder({
      exchange: o.exchange,
      salt: o.salt,
      condition: o.condition,
      outcome: o.outcome,
      side: o.side,
      share_amount: o.share_amount,
      price: o.price,
      expiration: o.expiration,
      nonce: o.nonce,
      recipient: o.recipient,
    });
    expect(signed.hash).toBe(o.fromGoldenSeed.hash);
    expect(signed.maker).toBe(golden.address);
    expect(signed.chain_id).toBe(golden.chainId);
  }, 30_000);

  it('signCancel matches golden cancel digest', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const o = golden.order;
    const signed = await b.signCancel({
      exchange: o.exchange,
      salt: o.salt,
      condition: o.condition,
      outcome: o.outcome,
      side: o.side,
      share_amount: o.share_amount,
      price: o.price,
      expiration: o.expiration,
      nonce: o.nonce,
      recipient: o.recipient,
    });
    expect(signed.digest_hex).toBe(o.fromGoldenSeed.cancelDigestHex);
  }, 30_000);

  it('signIncrementNonce matches golden digest', async () => {
    const b = track(new SplendorPQBridge());
    await b.connectFromSeed(GOLDEN_SEED);
    const o = golden.order;
    const signed = await b.signIncrementNonce({
      exchange: o.exchange,
      expected_nonce: o.nonce,
    });
    expect(signed.digest_hex).toBe(o.fromGoldenSeed.incrementNonceDigestHex);
    expect(signed.maker).toBe(golden.address);
  }, 30_000);
});
