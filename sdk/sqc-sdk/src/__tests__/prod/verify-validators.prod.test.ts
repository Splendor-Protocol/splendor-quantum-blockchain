import { describe, expect, it, vi } from 'vitest';
import { VerifyClient, getVerify, type VerifyInstruction } from '../../verify.js';
import { VALIDATOR_BOND_SPLD, getValidators } from '../../validators.js';
import { Connection } from '../../core/connection.js';
import { MockTransport } from '../../core/transport/mock-transport.js';
import { MODULE_ROUTER_ADDRESS } from '../../addresses/index.js';
import { BridgeError } from '../../errors.js';
import type { ModuleCall } from '../../core/module-call.js';

const VERIFY = 'SQ:' + '11'.repeat(32);
const VAL = 'SQ:' + 'ab'.repeat(32);
const ID = 'ff'.repeat(32);

const instruction: VerifyInstruction = {
  version: 1,
  instruction_id: ID,
  purpose: 'payment',
  origin: 'https://shop.example',
  settlement_hint: 'none',
  mldsa_pubkey: 'aa',
  sig_mldsa: 'bb',
  slh_pubkey: 'cc',
  sig_slh: 'dd',
};

function hexToJson(hex: string): ModuleCall {
  const bytes = hex.replace(/^0x/, '').match(/../g)!.map((b) => parseInt(b, 16));
  return JSON.parse(new TextDecoder().decode(new Uint8Array(bytes)));
}

describe('VerifyClient prod', () => {
  it('[L1] check, proof, attest, settle hit the node routes with the right bodies', async () => {
    const t = new MockTransport({
      handlers: {
        get: () => ({ instruction_id: ID, state: 'attested' }),
        post: (path) => (path === '/verify/check' ? { valid: true, digest: 'd' } : { status: 'submitted', hash: 'h' }),
      },
    });
    const v = getVerify(Connection.fromTransport(t));
    expect(await v.check(instruction)).toMatchObject({ valid: true });
    expect(await v.proof('0x' + ID, VERIFY)).toMatchObject({ state: 'attested' });
    await v.attest({ nonce: 1 });
    await v.settle({ nonce: 2 });
    expect(t.calls.get[0]).toBe(`/verify/proof/${ID}?instance=${encodeURIComponent(VERIFY)}`);
    expect(t.calls.post.map((p) => p.path)).toEqual(['/verify/check', '/verify/attest', '/verify/settle']);
    expect(t.calls.post[1]!.body).toEqual({ tx: { nonce: 1 } });
  });

  it('[L1] moduleCall builds a verify envelope routed to the module router', () => {
    const v = new VerifyClient(new MockTransport());
    expect(v.moduleCall('register', { name: 'x' })).toEqual({ module: 'verify', action: 'register', params: { name: 'x' } });
    expect(v.routerAddress()).toBe(MODULE_ROUTER_ADDRESS);
  });

  it('[L2] proof refuses ids that are not 32-byte hex and a bad instance, without a request', async () => {
    const t = new MockTransport({ handlers: { get: vi.fn() } });
    const v = getVerify(t);
    for (const bad of ['', 'zz'.repeat(32), 'ff'.repeat(31), 'ff'.repeat(33), '../admin']) {
      await expect(v.proof(bad, VERIFY)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    }
    await expect(v.proof(ID, '')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    await expect(v.proof(ID, 'not-an-address')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    expect(t.calls.get).toHaveLength(0);
  });

  it('[L3] a check that the node answers invalid is returned, not thrown', async () => {
    const t = new MockTransport({ handlers: { post: () => ({ valid: false, reason: 'bad ML-DSA signature' }) } });
    expect(await getVerify(t).check(instruction)).toEqual({ valid: false, reason: 'bad ML-DSA signature' });
  });

  it('[L3] a transport outage surfaces as RPC_UNAVAILABLE', async () => {
    const t = new MockTransport({
      handlers: {
        post: () => {
          throw BridgeError.rpcUnavailable('all RPC endpoints failed');
        },
      },
    });
    await expect(getVerify(t).attest({})).rejects.toMatchObject({ code: 'RPC_UNAVAILABLE' });
  });
});

describe('ValidatorsClient prod', () => {
  function withModules(answer: (env: ModuleCall) => unknown) {
    const t = new MockTransport({ handlers: { moduleCall: answer } });
    return { t, client: getValidators(Connection.fromTransport(t)) };
  }

  it('[L1] list reads the validators module and the bond, not a REST route', async () => {
    const { t, client } = withModules((env) =>
      env.action === 'list' ? [{ address: VAL, status: 1 }] : { bond: '2500000000000000000000', spld: '2500' },
    );
    expect(await client.list()).toEqual({ validators: [{ address: VAL, status: 1 }], count: 1, bond_spld: 2500 });
    expect(t.calls.get).toHaveLength(0);
    expect(t.calls.moduleCall.map((c) => c.action).sort()).toEqual(['bond', 'list']);
  });

  it('[L1] get and isApproved use the validator and approved reads', async () => {
    const { t, client } = withModules((env) =>
      env.action === 'validator' ? { address: VAL, status: 1 } : { approved: true },
    );
    expect(await client.get(VAL)).toMatchObject({ status: 1 });
    expect(await client.isApproved(VAL)).toEqual({ approved: true });
    expect(t.calls.moduleCall).toEqual([
      { module: 'validators', action: 'validator', params: { address: VAL } },
      { module: 'validators', action: 'approved', params: { address: VAL } },
    ]);
  });

  it('[L1] a bare get/post transport goes through POST /call to the module router', async () => {
    const post = vi.fn(async (_path: string, body: unknown) => {
      const env = hexToJson((body as { data: string }).data);
      return { decoded: env.action === 'approved' ? { approved: false } : null };
    });
    const client = getValidators({ get: vi.fn(), post } as never);
    expect(await client.isApproved(VAL)).toEqual({ approved: false });
    expect(post.mock.calls[0]![0]).toBe('/call');
    expect(post.mock.calls[0]![1]).toMatchObject({ to: MODULE_ROUTER_ADDRESS, encoding: 'hex' });
  });

  it('[L1] write calls build validators envelopes', () => {
    const client = getValidators(new MockTransport());
    expect(client.registerCall('ab')).toEqual({ module: 'validators', action: 'register', params: { pubkey: 'ab' } });
    expect(client.unregisterCall().action).toBe('unregister');
    expect(client.finalizeExitCall().action).toBe('finalize_exit');
    expect(client.approveCall(VAL).params).toEqual({ address: VAL });
    expect(client.revokeCall('SQ:' + 'AB'.repeat(32)).params).toEqual({ address: VAL });
  });

  it('[L2] an empty validator set reads as zero, with the default bond when the bond read fails', async () => {
    const { client } = withModules((env) => {
      if (env.action === 'list') throw BridgeError.rpc('moduleCall: decoded result is empty');
      throw BridgeError.rpc('validators: unknown read "bond"');
    });
    expect(await client.list()).toEqual({ validators: [], count: 0, bond_spld: VALIDATOR_BOND_SPLD });
  });

  it('[L2] bad addresses and empty pubkeys are refused before any call', async () => {
    const { t, client } = withModules(() => ({}));
    await expect(client.get('0xdead')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    await expect(client.isApproved('')).rejects.toMatchObject({ code: 'INVALID_ADDRESS' });
    expect(() => client.approveCall('nope')).toThrow(BridgeError);
    expect(() => client.revokeCall('')).toThrow(BridgeError);
    expect(() => client.registerCall('  ')).toThrow(BridgeError);
    expect(t.calls.moduleCall).toHaveLength(0);
  });

  it('[L3] a failing list read is surfaced, not reported as an empty set', async () => {
    const { client } = withModules(() => {
      throw BridgeError.rpcUnavailable('down');
    });
    await expect(client.list()).rejects.toMatchObject({ code: 'RPC_UNAVAILABLE' });
  });

  it('[L3] isApproved treats a malformed answer as not approved', async () => {
    const { client } = withModules(() => ({ approved: 'yes' }));
    expect(await client.isApproved(VAL)).toEqual({ approved: false });
  });

  it('[L4] 50 parallel approval checks resolve independently', async () => {
    const addrs = Array.from({ length: 50 }, (_, i) => 'SQ:' + i.toString(16).padStart(2, '0').repeat(32));
    const { client } = withModules(async (env) => {
      await new Promise((r) => setTimeout(r, 0));
      return { approved: String(env.params.address).endsWith('00') || String(env.params.address).endsWith('02') };
    });
    const out = await Promise.all(addrs.map((a) => client.isApproved(a)));
    expect(out.filter((o) => o.approved)).toHaveLength(2);
  });
});
