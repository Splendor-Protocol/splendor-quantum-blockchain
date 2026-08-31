/**
 * Route tests, driven through the Worker's own `fetch` with a fake KV and a
 * fake network — the faucet's harness pattern.
 *
 * The properties defended: strict input validation on the only parameterised
 * route, graceful 503 when unconfigured, stats that degrade to null instead
 * of breaking the page, correct Burned-event decoding, and a per-IP cooldown
 * that actually fires.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import worker, { _resetStatsCache } from '../src/index';
import { BURNED_TOPIC0 } from '../src/selectors';
import type { Env } from '../src/config';

const BURN_CONTRACT = '0x' + 'ab'.repeat(20);
const SPLV = '0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689';
const BRIDGE = 'SQ:' + 'cd'.repeat(32);
const TX = '0x' + '11'.repeat(32);
const RECIPIENT_HEX = 'ef'.repeat(32);

const EVM_RPC = 'https://mainnet-rpc.splendor.org';
const QRB = 'https://quantum-rpc.example.org';

class FakeKV {
  readonly data = new Map<string, string>();
  async get(key: string) {
    return this.data.get(key) ?? null;
  }
  async put(key: string, value: string) {
    this.data.set(key, value);
  }
  async delete(key: string) {
    this.data.delete(key);
  }
}

interface Call {
  url: string;
  body: string;
}

let kv: FakeKV;
let calls: Call[];
/** Receipt the fake EVM node returns; null = unknown tx. */
let receipt: unknown;
/** Head block, hex. */
let head: string;
let bridgeInfoStatus: number;
let claimResponse: unknown;

function env(over: Partial<Env> = {}): Env {
  return {
    MIGRATION_KV: kv as unknown as KVNamespace,
    EVM_RPC_URL: EVM_RPC,
    BURN_CONTRACT_ADDRESS: BURN_CONTRACT,
    SPLV_TOKEN_ADDRESS: SPLV,
    QUANTUM_RPC_BASE: QRB,
    QUANTUM_BRIDGE_ADDRESS: BRIDGE,
    ...over,
  } as Env;
}

function get(path: string, headers: Record<string, string> = {}): Request {
  return new Request(`https://quantum-migration.splendor.org${path}`, {
    headers: { 'cf-connecting-ip': '203.0.113.7', ...headers },
  });
}

/** A receipt holding one canonical Burned log (id 1, spld, 1e18, recipient). */
function burnedReceipt(): Record<string, unknown> {
  return {
    status: '0x1',
    blockNumber: '0x32', // 50
    logs: [
      {
        address: BURN_CONTRACT,
        topics: [
          BURNED_TOPIC0,
          '0x' + '1'.padStart(64, '0'), // id = 1
          '0x' + 'ab'.repeat(20).padStart(64, '0'), // sender
          '0x' + '0'.padStart(64, '0'), // asset = 0 (spld)
        ],
        data:
          '0x' +
          BigInt(10n ** 18n).toString(16).padStart(64, '0') + // amount = 1e18
          RECIPIENT_HEX, // quantumRecipient
      },
    ],
  };
}

beforeEach(() => {
  kv = new FakeKV();
  calls = [];
  receipt = null;
  head = '0x64'; // 100
  bridgeInfoStatus = 200;
  claimResponse = { claimed: false };
  _resetStatsCache();

  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const body = typeof init?.body === 'string' ? init.body : '';
    calls.push({ url, body });

    if (url.includes('/claim/')) {
      return new Response(JSON.stringify(claimResponse), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    if (url.includes('/bridge/')) {
      // The node wraps every module read as {module, action, info: {...}}
      // (see rpc/bridge_rpc.go + vm/module_router_read.go).
      return new Response(
        JSON.stringify({
          module: 'bridge',
          action: 'bridge_info',
          info: {
            address: 'SQ:' + '22'.repeat(32),
            owner: 'SQ:' + '00'.repeat(32),
            paused: false,
            evm_chain_id: 2691,
            evm_contract: BURN_CONTRACT,
            splv_token: 'SQ:' + '11'.repeat(32),
            max_claim_amount: '1000000000000000000000000',
            claimed_total_spld: '5000000000000000000',
            claimed_total_splv: '7000000000000000000',
            pool_balance: '900000000000000000000000',
            operators: [],
          },
        }),
        { status: bridgeInfoStatus, headers: { 'content-type': 'application/json' } },
      );
    }
    if (url.startsWith(EVM_RPC)) {
      const req = JSON.parse(body) as { method: string; id: number };
      const result =
        req.method === 'eth_blockNumber'
          ? head
          : req.method === 'eth_getTransactionReceipt'
            ? receipt
            : undefined;
      if (result === undefined) throw new Error(`unexpected RPC method ${req.method}`);
      return new Response(JSON.stringify({ jsonrpc: '2.0', id: req.id, result }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    throw new Error(`unexpected fetch to ${url}`);
  });
});

afterEach(() => vi.unstubAllGlobals());

describe('GET /health', () => {
  it('answers without touching config or the network', async () => {
    const res = await worker.fetch(get('/health'), env({ BURN_CONTRACT_ADDRESS: undefined }));
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true, service: 'quantum-migration' });
    expect(calls).toHaveLength(0);
  });
});

describe('GET /api/info', () => {
  it('returns the full shape with bridge stats mapped', async () => {
    const res = await worker.fetch(get('/api/info'), env());
    expect(res.status).toBe(200);
    const data = (await res.json()) as Record<string, any>;
    expect(data.ok).toBe(true);
    expect(data.network).toEqual({
      chainIdHex: '0xa83',
      chainName: 'Splendor Mainnet',
      rpcUrl: 'https://mainnet-rpc.splendor.org',
      explorer: 'https://explorer.splendor.org',
    });
    expect(data.burnContract).toBe(BURN_CONTRACT.toLowerCase());
    expect(data.splvToken).toBe(SPLV);
    expect(data.minBurn).toBe('1000000000000000000');
    expect(data.confirmations).toBe(120);
    expect(data.quantum).toEqual({
      explorer: 'https://quantum-explorer.splendor.org',
      bridgeAddress: BRIDGE,
    });
    expect(data.stats).toEqual({
      poolBalance: '900000000000000000000000',
      claimedSpld: '5000000000000000000',
      claimedSplv: '7000000000000000000',
      maxClaimAmount: '1000000000000000000000000',
      paused: false,
    });
    // The per-burn ceiling is surfaced at top level for the UI clamp.
    expect(data.maxBurn).toBe('1000000000000000000000000');
    // Asked exactly the bridge REST endpoint.
    expect(calls.map((c) => c.url)).toEqual([`${QRB}/bridge/${BRIDGE}`]);
  });

  it('degrades to stats:null and maxBurn:null when the quantum node is dark — never a 5xx', async () => {
    bridgeInfoStatus = 502;
    const res = await worker.fetch(get('/api/info'), env());
    expect(res.status).toBe(200);
    const data = (await res.json()) as Record<string, any>;
    expect(data.ok).toBe(true);
    expect(data.stats).toBeNull();
    expect(data.maxBurn).toBeNull();
  });

  it('degrades to stats:null when fetch itself throws', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new Error('network down');
    });
    const res = await worker.fetch(get('/api/info'), env());
    expect(res.status).toBe(200);
    expect(((await res.json()) as Record<string, any>).stats).toBeNull();
  });

  it('caches bridge stats for 10 seconds', async () => {
    await worker.fetch(get('/api/info'), env());
    await worker.fetch(get('/api/info'), env());
    expect(calls.filter((c) => c.url.includes('/bridge/'))).toHaveLength(1);
  });

  it('answers 503 unconfigured when required vars are unset', async () => {
    for (const over of [
      { BURN_CONTRACT_ADDRESS: undefined },
      { BURN_CONTRACT_ADDRESS: '' },
      { BURN_CONTRACT_ADDRESS: 'not-an-address' },
      { QUANTUM_RPC_BASE: undefined },
      { QUANTUM_BRIDGE_ADDRESS: undefined },
      { QUANTUM_BRIDGE_ADDRESS: 'SQ:zz' },
    ] as Partial<Env>[]) {
      const res = await worker.fetch(get('/api/info'), env(over));
      expect(res.status, JSON.stringify(over)).toBe(503);
      expect(((await res.json()) as { code: string }).code).toBe('unconfigured');
    }
    expect(calls).toHaveLength(0);
  });

  it('rejects a POST', async () => {
    const res = await worker.fetch(
      new Request('https://quantum-migration.splendor.org/api/info', { method: 'POST' }),
      env(),
    );
    expect(res.status).toBe(405);
  });
});

describe('GET /api/status/{txhash} — input validation', () => {
  it('rejects everything that is not 0x + 64 hex', async () => {
    const bad = [
      '11'.repeat(32), // missing 0x
      '0x' + '11'.repeat(31), // too short
      '0x' + '11'.repeat(33), // too long
      '0x' + 'zz'.repeat(32), // non-hex
      'SQ:' + 'ab'.repeat(32), // quantum address, wrong chain
      'latest',
    ];
    for (const h of bad) {
      const res = await worker.fetch(get(`/api/status/${h}`), env());
      expect(res.status, h).toBe(400);
      expect(((await res.json()) as { code: string }).code).toBe('bad-hash');
    }
    expect(calls).toHaveLength(0);
  });

  it('accepts an uppercase hash by normalising it', async () => {
    receipt = null;
    const mixed = '0x' + 'AB'.repeat(32);
    const res = await worker.fetch(get(`/api/status/${mixed}`), env());
    expect(res.status).toBe(200);
    // The node was asked with the lowercase form.
    const rpc = calls.find((c) => c.url.startsWith(EVM_RPC) && c.body.includes('eth_getTransactionReceipt'));
    expect(rpc!.body).toContain('0x' + 'ab'.repeat(32));
    expect(rpc!.body).not.toContain(mixed);
  });

  it('answers 503 unconfigured before validating anything else', async () => {
    const res = await worker.fetch(get('/api/status/nonsense'), env({ QUANTUM_RPC_BASE: undefined }));
    expect(res.status).toBe(503);
  });

  it('rejects a POST', async () => {
    const res = await worker.fetch(
      new Request(`https://quantum-migration.splendor.org/api/status/${TX}`, { method: 'POST' }),
      env(),
    );
    expect(res.status).toBe(405);
  });
});

describe('GET /api/status/{txhash} — chain state', () => {
  it('reports found:false for an unknown transaction', async () => {
    receipt = null;
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      ok: true,
      found: false,
      confirmations: 0,
      required: 120,
      burns: [],
      claim: null,
    });
  });

  it('decodes the Burned event and reports confirmations + claim state', async () => {
    receipt = burnedReceipt();
    claimResponse = { claimed: true, height: 4242 };
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    expect(res.status).toBe(200);
    const data = (await res.json()) as Record<string, any>;
    expect(data.found).toBe(true);
    expect(data.status).toBe('success');
    expect(data.confirmations).toBe(51); // head 100, mined 50 → 51
    expect(data.required).toBe(120);
    expect(data.burns).toEqual([
      {
        id: '1',
        asset: 'spld',
        amount: '1000000000000000000',
        quantumRecipient: '0x' + RECIPIENT_HEX,
        recipientSq: 'SQ:' + RECIPIENT_HEX,
        claimed: true,
        claimHeight: 4242,
      },
    ]);
    expect(data.claim).toEqual({ claimed: true, height: 4242 });
    // The claim slot was asked for burn id 1 on the configured bridge.
    expect(calls.some((c) => c.url === `${QRB}/bridge/${BRIDGE}/claim/1`)).toBe(true);
  });

  it('ignores logs from other contracts and other topics', async () => {
    const r = burnedReceipt();
    (r.logs as unknown[]).unshift(
      // Same topic, WRONG emitter — must not be credited as a burn.
      { ...(burnedReceipt().logs as any[])[0], address: '0x' + 'ee'.repeat(20) },
      // Right emitter, wrong topic.
      {
        address: BURN_CONTRACT,
        topics: ['0x' + 'aa'.repeat(32), '0x' + '1'.padStart(64, '0')],
        data: '0x',
      },
    );
    receipt = r;
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    const data = (await res.json()) as Record<string, any>;
    expect(data.burns).toHaveLength(1);
    expect(data.burns[0].id).toBe('1');
  });

  it('reports a reverted burn as failed with no burns', async () => {
    receipt = { status: '0x0', blockNumber: '0x32', logs: [] };
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    const data = (await res.json()) as Record<string, any>;
    expect(data.found).toBe(true);
    expect(data.status).toBe('failed');
    expect(data.burns).toEqual([]);
  });

  it('marks claim state unknown (null) when the quantum node is dark', async () => {
    receipt = burnedReceipt();
    const realFetch = globalThis.fetch;
    vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : (input as Request).url;
      if (url.includes('/claim/')) throw new Error('quantum node down');
      return realFetch(input, init);
    });
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    const data = (await res.json()) as Record<string, any>;
    expect(data.burns[0].claimed).toBeNull();
    expect(data.claim).toEqual({ claimed: null });
  });

  it('answers 502, not 500, when the EVM RPC is unreachable', async () => {
    vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : (input as Request).url;
      if (url.startsWith(EVM_RPC)) throw new Error('connect timeout');
      return new Response('{}', { status: 200 });
    });
    const res = await worker.fetch(get(`/api/status/${TX}`), env());
    expect(res.status).toBe(502);
    expect(((await res.json()) as { code: string }).code).toBe('rpc-unavailable');
  });
});

describe('rate limiting', () => {
  it('fires on the second immediate poll from one IP', async () => {
    receipt = null;
    const first = await worker.fetch(get(`/api/status/${TX}`), env());
    expect(first.status).toBe(200);
    const second = await worker.fetch(get(`/api/status/${TX}`), env());
    expect(second.status).toBe(429);
    expect(second.headers.get('retry-after')).toBeTruthy();
    expect(((await second.json()) as { code: string }).code).toBe('rate-limited');
  });

  it('does not throttle two different IPs together', async () => {
    receipt = null;
    const first = await worker.fetch(get(`/api/status/${TX}`), env());
    const second = await worker.fetch(get(`/api/status/${TX}`, { 'cf-connecting-ip': '198.51.100.9' }), env());
    expect(first.status).toBe(200);
    expect(second.status).toBe(200);
  });

  it('fails OPEN when the limit store is broken — status stays readable', async () => {
    receipt = null;
    const broken = {
      get: async () => {
        throw new Error('KV unavailable');
      },
      put: async () => {},
      delete: async () => {},
    };
    const res = await worker.fetch(get(`/api/status/${TX}`), env({ MIGRATION_KV: broken as unknown as KVNamespace }));
    expect(res.status).toBe(200);
  });
});

describe('request hygiene', () => {
  it('rejects an oversized declared body up front', async () => {
    const res = await worker.fetch(
      new Request('https://quantum-migration.splendor.org/api/info', {
        headers: { 'content-length': '999999' },
      }),
      env(),
    );
    expect(res.status).toBe(413);
    expect(calls).toHaveLength(0);
  });

  it('404s unknown paths', async () => {
    const res = await worker.fetch(get('/nope'), env());
    expect(res.status).toBe(404);
  });
});
