import { describe, expect, it, vi } from 'vitest';
import { createX402Middleware } from '../x402/client.js';
import type { Connection } from '../core/connection.js';

const PAY_TO = 'SQ:' + 'aa'.repeat(32);

function mockConnection(opts: {
  verifyValid?: boolean;
  settleSuccess?: boolean;
}): Connection {
  return {
    chainId: 'splendor-quantum-1',
    rpc: {
      x402Verify: vi.fn(async () => ({ valid: opts.verifyValid !== false })),
      x402Settle: vi.fn(async () =>
        opts.settleSuccess === false
          ? { success: false, error: 'settle failed' }
          : { success: true, tx_hash: '0xsettle' },
      ),
    },
  } as unknown as Connection;
}

describe('createX402Middleware', () => {
  it('rejects verify-only payments when settlement is required (default)', async () => {
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection: mockConnection({}),
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw(
      {
        path: '/api',
        headers: {
          'x-payment': JSON.stringify({
            chain_id: 'splendor-quantum-1',
            from: PAY_TO,
            to: PAY_TO,
            amount: '1',
            valid_after: 0,
            valid_before: 9e9,
            nonce: 'ab'.repeat(32),
            signature: 'YQ==',
          }),
        },
      },
      res as never,
      next,
    );
    expect(res.status).toHaveBeenCalledWith(402);
    expect(json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Settlement Required' }),
    );
    expect(next).not.toHaveBeenCalled();
  });

  it('calls next after verify + settle when settlement_tx present', async () => {
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection: mockConnection({}),
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw(
      {
        path: '/api',
        headers: {
          'x-payment': JSON.stringify({
            chain_id: 'splendor-quantum-1',
            from: PAY_TO,
            to: PAY_TO,
            amount: '10000000000000000',
            valid_after: 0,
            valid_before: 9e9,
            nonce: 'ab'.repeat(32),
            signature: 'YQ==',
            settlement_tx: { type: 5, chain_id: 'splendor-quantum-1' },
          }),
        },
      },
      res as never,
      next,
    );
    expect(next).toHaveBeenCalled();
  });
});
