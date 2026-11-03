import { afterEach, describe, expect, it, vi } from 'vitest';
import { MIN_FEE, SplendorPQBridge } from '../../index.js';
import { connect } from '../../wallet/connect.js';
import { buildBridgeOptions } from '../../connect.js';
import { resolveNetwork } from '../../networks.js';
import { getPool } from '../../defi/index.js';
import { Connection } from '../../core/connection.js';
import { MockTransport } from '../../core/transport/mock-transport.js';
import { CHAIN_ID } from '../../derive.js';
import golden from '../../../testdata/golden-vectors.json';

const SEED_A = golden.seedHex;
const SEED_B = '02'.repeat(32);
const originalFetch = globalThis.fetch;
const open: Array<{ disconnect: () => void }> = [];

afterEach(() => {
  globalThis.fetch = originalFetch;
  for (const b of open.splice(0)) {
    try {
      b.disconnect();
    } catch {
      /* already gone */
    }
  }
  vi.restoreAllMocks();
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

/** One in-memory node: per-address nonces, a slow /nonce, and a submit log. */
function fakeNode(opts: { nonceDelayMs?: number } = {}) {
  const nonces = new Map<string, number>();
  const submitted: Array<{ from: string; nonce: number }> = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes('/status')) return json({ chain_id: CHAIN_ID });
    const nonceMatch = url.match(/\/nonce\/([^/?]+)/);
    if (nonceMatch) {
      const addr = decodeURIComponent(nonceMatch[1]!);
      await new Promise((r) => setTimeout(r, opts.nonceDelayMs ?? 10));
      return json({ address: addr, nonce: nonces.get(addr) ?? 0 });
    }
    if (url.includes('/tx/submit')) {
      const body = JSON.parse(String(init?.body || '{}')) as { from?: number[] | string; nonce?: number };
      const from = Array.isArray(body.from)
        ? 'SQ:' + body.from.map((b) => b.toString(16).padStart(2, '0')).join('')
        : String(body.from);
      submitted.push({ from, nonce: Number(body.nonce) });
      nonces.set(from, Number(body.nonce) + 1);
      return json({ status: 'ok', hash: `h-${from.slice(-4)}-${body.nonce}` });
    }
    return json({ error: `unexpected ${url}` }, 404);
  }) as typeof fetch;
  return { submitted, nonces };
}

function bridge() {
  const b = new SplendorPQBridge({ rpcUrl: 'https://rpc-matrix.test', chainId: CHAIN_ID, retries: 0 });
  open.push(b);
  return b;
}

describe('connect matrix prod — options', () => {
  it('[L1] a gatewayUrl override reaches the resolved network', () => {
    const { network } = buildBridgeOptions({ network: 'local', gatewayUrl: 'https://gw.example.org' });
    expect(network.gatewayUrl).toBe('https://gw.example.org');
  });

  it('[L2] requireGateway refuses an empty or plain-http remote gateway', () => {
    const rpcUrls = ['https://rpc.example.org'];
    expect(() => resolveNetwork('testnet', { rpcUrls, gatewayUrl: '', requireGateway: true })).toThrow(/gatewayUrl/);
    expect(() =>
      resolveNetwork('testnet', { rpcUrls, gatewayUrl: 'http://gw.example.org', requireGateway: true }),
    ).toThrow(/https/);
    expect(
      resolveNetwork('testnet', { rpcUrls, gatewayUrl: 'https://gw.example.org', requireGateway: true }).gatewayUrl,
    ).toBe('https://gw.example.org');
  });

  it('[L2] a remote plain-http RPC is refused unless explicitly allowed', () => {
    expect(() => buildBridgeOptions({ network: 'testnet', rpcUrls: ['http://rpc.example.org'] })).toThrow(/https/);
  });

  it('[L2] seed mode without a seed fails with NOT_CONNECTED', async () => {
    await expect(connect({ network: 'local', mode: 'seed' })).rejects.toMatchObject({ code: 'NOT_CONNECTED' });
  });

  it('[L2] an unknown mode is refused', async () => {
    await expect(connect({ network: 'local', mode: 'telepathy' as never })).rejects.toThrow(/Unknown connect mode/);
  });
});

describe('connect matrix prod — wallet behaviour', () => {
  it('[L3] RPC down: unlock still works offline, preflight fails with a network code', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('fetch failed');
    }) as typeof fetch;
    const b = new SplendorPQBridge({ rpcUrl: 'https://rpc-down.test', chainId: CHAIN_ID, retries: 0, timeoutMs: 50 });
    open.push(b);
    const info = await b.connectFromSeed(SEED_A);
    expect(info.address).toBe(golden.address);
    const err = await b.preflight({ amount: '1', fee: MIN_FEE }).catch((e: unknown) => e);
    expect((err as { code?: string }).code).toMatch(/^(RPC_UNAVAILABLE|NETWORK)$/);
  }, 30_000);

  it('[L3] disconnect during a send: later sends refuse, nothing hangs', async () => {
    fakeNode({ nonceDelayMs: 30 });
    const b = bridge();
    await b.connectFromSeed(SEED_A);
    const inflight = b.sendTransfer({ to: golden.address, amount: '1', fee: MIN_FEE }).then(
      () => 'settled',
      () => 'rejected',
    );
    b.disconnect();
    expect(['settled', 'rejected']).toContain(await inflight);
    expect(b.getConnectionState()).toBe('disconnected');
    expect(b.account).toBeNull();
    await expect(b.sendTransfer({ to: golden.address, amount: '1', fee: MIN_FEE })).rejects.toMatchObject({
      code: 'NOT_CONNECTED',
    });
  }, 30_000);

  it('[L3] lock clears the account but keeps the session reusable', async () => {
    fakeNode();
    const b = bridge();
    await b.connectFromSeed(SEED_A);
    b.lock();
    expect(b.account).toBeNull();
    expect(() => b.getAddress()).toThrow();
    expect((await b.connectFromSeed(SEED_A)).address).toBe(golden.address);
  }, 30_000);

  it('[L4] two wallets sending in parallel each keep their own nonce sequence', async () => {
    const node = fakeNode({ nonceDelayMs: 15 });
    const a = bridge();
    const b = bridge();
    const addrA = (await a.connectFromSeed(SEED_A)).address;
    const addrB = (await b.connectFromSeed(SEED_B)).address;
    expect(addrA).not.toBe(addrB);
    await Promise.all([
      ...[1, 2, 3].map((n) => a.sendTransfer({ to: addrB, amount: String(n), fee: MIN_FEE })),
      ...[1, 2, 3].map((n) => b.sendTransfer({ to: addrA, amount: String(n), fee: MIN_FEE })),
    ]);
    const seq = (addr: string) => node.submitted.filter((s) => s.from === addr).map((s) => s.nonce);
    expect(seq(addrA)).toEqual([0, 1, 2]);
    expect(seq(addrB)).toEqual([0, 1, 2]);
  }, 60_000);

  it('[L4] ten connect/disconnect cycles derive the same address and end disconnected', async () => {
    fakeNode();
    const b = bridge();
    for (let i = 0; i < 10; i++) {
      expect((await b.connectFromSeed(SEED_A)).address).toBe(golden.address);
      expect(b.getConnectionState()).toBe('connected');
      b.disconnect();
      expect(b.getConnectionState()).toBe('disconnected');
    }
  }, 60_000);
});

describe('connect matrix prod — DeFi stays fail-closed', () => {
  it('[L3] an unrouted DEX adapter does not report success from the node', async () => {
    const t = new MockTransport({
      handlers: {
        moduleCall: (env) => {
          throw new Error(`moduleCall reverted: unknown module "${env.module}"`);
        },
      },
    });
    const pool = getPool(Connection.fromTransport(t), 'SQ:' + 'aa'.repeat(32));
    await expect(pool.reserves()).rejects.toThrow(/unknown module/);
    await expect(pool.poolInfo()).rejects.toThrow(/unknown module/);
  });
});
