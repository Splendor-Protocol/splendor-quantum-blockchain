import { describe, expect, it, vi } from 'vitest';
import { verifyAddress, bridgeAddress } from '../addresses/index.js';
import { VerifyModule, deployVerify, getVerify } from '../verify.js';
import { modules, bindModules } from '../modules.js';
import { listModules } from '../core/module-registry.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const OWNER = golden.address;
const OP = 'SQ:' + 'cc'.repeat(32);
const ID = golden.verify.instruction.instruction_id;

function mockWallet(): SplendorPQBridge {
  return {
    getAddress: () => OWNER,
    getNonce: async () => ({ nonce: 2 }),
    getLatestBlock: async () => ({ height: 100 }),
    connection: { getLatestBlock: async () => ({ height: 100 }), moduleCall: vi.fn() } as unknown as Connection,
    sendModuleCall: vi.fn(async (opts) => ({ status: 'ok', hash: '0xh', signedTx: opts })),
  } as unknown as SplendorPQBridge;
}

describe('verify (SQVL) module client', () => {
  it('verifyAddress is deterministic, height-sensitive and distinct from the bridge tag', () => {
    expect(verifyAddress(OWNER, 1, 5)).toBe(verifyAddress(OWNER, 1, 5));
    expect(verifyAddress(OWNER, 1, 5)).not.toBe(verifyAddress(OWNER, 1, 6));
    expect(verifyAddress(OWNER, 1, 5)).not.toBe(bridgeAddress(OWNER, 1, 5));
    expect(verifyAddress(OWNER, 1, 5).startsWith('SQ:')).toBe(true);
  });

  it('deployVerify predicts the next-height address and sends the deploy envelope', async () => {
    const wallet = mockWallet();
    const { address, predictedAddress, confirmed, height } = await deployVerify(wallet, {
      threshold: 1,
      operators: [OP],
    });
    expect(height).toBe(101);
    expect(address).toBe(VerifyModule.predictAddress(OWNER, 2, 101));
    expect(predictedAddress).toBe(address);
    expect(confirmed).toBe(false);
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[0][0]).toMatchObject({
      module: 'verify',
      action: 'deploy',
      params: { threshold: 1, operators: [OP] },
    });
  });

  it('register / attest / finalize / spend carry the instance under `verify` and a hex id', async () => {
    const wallet = mockWallet();
    const inst = verifyAddress(OWNER, 2, 101);
    const v = getVerify({ moduleCall: vi.fn() } as unknown as Connection, inst);
    const instruction = {
      ...golden.verify.instruction,
      instruction_id: '0x' + ID,
      mldsa_pubkey: 'AA==',
      sig_mldsa: 'AA==',
      slh_pubkey: 'AA==',
      sig_slh: 'AA==',
    };
    await v.register(wallet, instruction);
    await v.attest(wallet, ID, { pubkey: 'p', slh_pubkey: 's', sig_mldsa: 'm', sig_slh: 'h' });
    await v.finalize(wallet, ID);
    await v.spend(wallet, ID);
    await v.setOperator(wallet, OP, false);
    await v.setThreshold(wallet, 2);
    await v.pause(wallet);
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    const calls = send.mock.calls.map((c) => c[0]);
    expect(calls[0]).toMatchObject({
      module: 'verify',
      action: 'register',
      params: { verify: inst, instruction: { instruction_id: ID, purpose: 'payment' } },
    });
    expect(calls[1]).toMatchObject({
      action: 'attest',
      params: {
        verify: inst,
        instruction_id: ID,
        attestation: { pubkey: 'p', slh_pubkey: 's', sig_mldsa: 'm', sig_slh: 'h' },
      },
    });
    expect(calls[2]).toMatchObject({ action: 'finalize', params: { verify: inst, instruction_id: ID } });
    expect(calls[3]).toMatchObject({ action: 'spend', params: { verify: inst, instruction_id: ID } });
    expect(calls[4]).toMatchObject({ action: 'set_operator', params: { verify: inst, operator: OP, active: false } });
    expect(calls[5]).toMatchObject({ action: 'set_threshold', params: { verify: inst, threshold: 2 } });
    expect(calls[6]).toMatchObject({ action: 'pause', params: { verify: inst } });
  });

  it('refuses an instruction id that is not 64 hex characters', async () => {
    const wallet = mockWallet();
    const v = getVerify({ moduleCall: vi.fn() } as unknown as Connection, verifyAddress(OWNER, 2, 101));
    await expect(v.finalize(wallet, 'abc')).rejects.toThrow(/64 hex/);
  });

  it('proof / instance reads go through the connection with the instance address', async () => {
    const moduleCall = vi.fn(async () => ({ spent: false }));
    const inst = verifyAddress(OWNER, 2, 101);
    const v = getVerify({ moduleCall } as unknown as Connection, inst);
    await v.proof(ID);
    await v.instance();
    expect(moduleCall.mock.calls[0][0]).toEqual({
      module: 'verify',
      action: 'proof',
      params: { verify: inst, instruction_id: ID },
    });
    expect(moduleCall.mock.calls[1][0]).toEqual({ module: 'verify', action: 'instance', params: { verify: inst } });
  });

  it('is registered as a built-in module and bound like the others', () => {
    expect(listModules().map((m) => m.id)).toContain('verify');
    const conn = { moduleCall: vi.fn() } as unknown as Connection;
    expect(modules.verify(conn, verifyAddress(OWNER, 1, 1))).toBeInstanceOf(VerifyModule);
    const bound = bindModules(conn, mockWallet());
    expect(typeof bound.verify(verifyAddress(OWNER, 1, 1)).finalize).toBe('function');
  });
});
