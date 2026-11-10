/**
 * A node is not a trusted party: verify and validators reads must not pass malformed
 * responses through as if they were well-formed.
 */
import { describe, expect, it } from 'vitest';
import { getVerify, type VerifyInstruction } from '../verify.js';
import { getValidators } from '../validators.js';
import { MockTransport } from '../core/transport/mock-transport.js';

const ID = 'ff'.repeat(32);
const ADDR = 'SQ:' + 'ab'.repeat(32);
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

function verifyWith(post: unknown, get: unknown = {}) {
  return getVerify(new MockTransport({ handlers: { post: () => post, get: () => get } }));
}

describe('VerifyClient response shapes', () => {
  it('only a literal true counts as valid', async () => {
    for (const valid of ['true', 1, 'yes', null, undefined]) {
      expect((await verifyWith({ valid }).check(instruction)).valid).toBe(false);
    }
    expect((await verifyWith({ valid: true }).check(instruction)).valid).toBe(true);
  });

  it('drops non-string fields from a check result', async () => {
    const res = await verifyWith({ valid: true, digest: { evil: 1 }, signer: 42, reason: 'ok' }).check(instruction);
    expect(res).toEqual({ valid: true, reason: 'ok' });
  });

  it('rejects a check response that is not an object', async () => {
    for (const bad of [null, 'ok', 7, [true]]) {
      await expect(verifyWith(bad).check(instruction)).rejects.toMatchObject({ code: 'RPC' });
    }
  });

  it('attest and settle require a tx hash', async () => {
    await expect(verifyWith({ status: 'submitted' }).attest({})).rejects.toMatchObject({ code: 'RPC' });
    await expect(verifyWith({ status: 'submitted', hash: 9 }).settle({})).rejects.toMatchObject({ code: 'RPC' });
    expect(await verifyWith({ hash: 'h' }).settle({})).toEqual({ status: '', hash: 'h' });
  });

  it('proof rejects a non-object body', async () => {
    await expect(verifyWith({}, ['x']).proof(ID, ADDR)).rejects.toMatchObject({ code: 'RPC' });
  });
});

describe('ValidatorsClient response shapes', () => {
  function validatorsWith(decoded: Record<string, unknown>) {
    return getValidators(
      new MockTransport({
        handlers: {
          moduleCall: (call) => decoded[call.action],
        },
      }),
    );
  }

  it('list keeps only object entries', async () => {
    const res = await validatorsWith({ list: [{ address: ADDR }, 'junk', null, 3, ['x']], bond: { spld: '2500' } }).list();
    expect(res.validators).toEqual([{ address: ADDR }]);
    expect(res.count).toBe(1);
  });

  it('get rejects a non-object validator record', async () => {
    await expect(validatorsWith({ validator: 'not-a-record' }).get(ADDR)).rejects.toMatchObject({ code: 'RPC' });
  });
});
