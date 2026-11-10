import { describe, expect, it, vi } from 'vitest';
import type { Connection } from '../core/connection.js';
import { BridgeModule, getBridge } from '../bridge/module.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';

const BRIDGE = 'SQ:' + 'aa'.repeat(32);
const OPERATOR = 'SQ:' + 'bb'.repeat(32);

type Envelope = { module: string; action: string; params: Record<string, unknown> };

function mockWallet() {
  return {
    getAddress: () => OPERATOR,
    sendModuleCall: vi.fn(async (opts: Envelope) => ({ status: 'ok', hash: '0xabc', signedTx: { nonce: 1, ...opts } })),
  } as unknown as SplendorPQBridge;
}

const sent = (w: SplendorPQBridge) => (w.sendModuleCall as ReturnType<typeof vi.fn>).mock.calls.map((c) => c[0] as Envelope);

describe('BridgeModule admin writes', () => {
  const bridge = new BridgeModule({ moduleCall: vi.fn() } as unknown as Connection, BRIDGE);

  it('setOperator, setThreshold, pause, unpause and transferOwnership route to the node actions', async () => {
    const w = mockWallet();
    await bridge.setOperator(w, OPERATOR, true);
    await bridge.setOperator(w, OPERATOR, 'yes' as unknown as boolean);
    await bridge.setThreshold(w, 2);
    await bridge.pause(w);
    await bridge.unpause(w);
    await bridge.transferOwnership(w, OPERATOR);
    expect(sent(w).map((e) => e.action)).toEqual([
      'set_operator',
      'set_operator',
      'set_threshold',
      'pause',
      'unpause',
      'transfer_ownership',
    ]);
    expect(sent(w).every((e) => e.module === 'bridge' && e.params.bridge === BRIDGE)).toBe(true);
    expect(sent(w)[0]!.params).toMatchObject({ operator: OPERATOR, active: true });
    // Only a literal true activates an operator.
    expect(sent(w)[1]!.params).toMatchObject({ active: false });
    expect(sent(w)[2]!.params).toMatchObject({ threshold: 2 });
  });

  it('refuses a bad threshold or address before signing', async () => {
    const w = mockWallet();
    for (const bad of [0, -1, 1.5, Number.NaN]) {
      await expect(bridge.setThreshold(w, bad)).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    }
    await expect(bridge.setOperator(w, '0xdead', true)).rejects.toThrow();
    await expect(bridge.transferOwnership(w, 'not-an-address')).rejects.toThrow();
    expect(sent(w)).toEqual([]);
  });

  it('claim validates asset, attestations and amount', async () => {
    const w = mockWallet();
    const base = {
      id: 1,
      asset: 'spld' as const,
      amount: '10',
      recipient: OPERATOR,
      evmTx: '0x' + '11'.repeat(32),
      attestations: [{ pubkey: 'p', signature: 's' }],
    };
    await expect(bridge.claim(w, { ...base, asset: 'eth' as never })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(bridge.claim(w, { ...base, attestations: [] })).rejects.toMatchObject({ code: 'INVALID_PARAMS' });
    await expect(bridge.claim(w, { ...base, amount: '1.5' })).rejects.toMatchObject({ code: 'INVALID_AMOUNT' });
    await bridge.claim(w, base);
    expect(sent(w)).toHaveLength(1);
    expect(sent(w)[0]!.params).toMatchObject({ id: 1, asset: 'spld', amount: '10', evm_tx: base.evmTx });
  });

  it('getBridge returns a client bound to the address', () => {
    expect(getBridge({ moduleCall: vi.fn() } as unknown as Connection, BRIDGE)).toBeInstanceOf(BridgeModule);
  });
});
