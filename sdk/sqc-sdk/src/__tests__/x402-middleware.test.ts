import { describe, expect, it, vi } from 'vitest';
import { createX402Middleware } from '../x402/client.js';
import type { Connection } from '../core/connection.js';

const PAY_TO = 'SQ:' + 'aa'.repeat(32);

function mockConnection(opts: {
  verifyValid?: boolean;
  nonceUsed?: boolean;
  settleStatus?: 'submitted' | 'confirmed' | 'failed';
  settleSuccess?: boolean;
  wait?: boolean | Error;
}): Connection {
  const settleStatus =
    opts.settleStatus ?? (opts.settleSuccess === false ? 'failed' : 'submitted');
  const settleSuccess = settleStatus === 'confirmed';
  return {
    chainId: 'splendor-quantum-1',
    x402Verify: vi.fn(async () => ({
      valid: opts.verifyValid !== false && !opts.nonceUsed,
      reason: opts.nonceUsed ? 'payment nonce already used' : undefined,
      nonce_used: opts.nonceUsed === true,
    })),
    x402Settle: vi.fn(async () =>
      settleStatus === 'failed'
        ? { success: false, status: 'failed', error: 'settle failed' }
        : { success: settleSuccess, status: settleStatus, tx_hash: 'aa'.repeat(32) },
    ),
    waitForConfirmation:
      opts.wait instanceof Error
        ? vi.fn(async () => {
            throw opts.wait;
          })
        : opts.wait === false
          ? undefined
          : vi.fn(async () => ({ confirmed: true as const, nonce: 1 })),
  } as unknown as Connection;
}

const paidHeader = {
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
};

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

  it('does not call next on submitted-only settle without a receipt', async () => {
    const connection = mockConnection({ wait: false });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).not.toHaveBeenCalled();
    expect(res.status).toHaveBeenCalledWith(402);
    expect(json).toHaveBeenCalledWith(
      expect.objectContaining({ error: 'Settlement Unconfirmed', status: 'submitted' }),
    );
  });

  it('calls next after a successful receipt for a submitted settlement', async () => {
    const connection = mockConnection({ settleStatus: 'submitted' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(connection.waitForConfirmation).toHaveBeenCalled();
    expect(next).toHaveBeenCalled();
  });

  it('calls next immediately when settle reports confirmed', async () => {
    const connection = mockConnection({ settleStatus: 'confirmed' });
    const wait = connection.waitForConfirmation as ReturnType<typeof vi.fn>;
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
    expect(wait).not.toHaveBeenCalled();
  });

  it('grants on submitted when settlementPolicy is optimistic-submitted', async () => {
    const connection = mockConnection({ settleStatus: 'submitted', wait: false });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
      settlementPolicy: 'optimistic-submitted',
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
  });

  it('treats a consumed nonce as idempotent confirmed payment', async () => {
    const connection = mockConnection({ nonceUsed: true, settleStatus: 'confirmed' });
    const mw = createX402Middleware({
      payTo: PAY_TO,
      price: '0.01',
      connection,
    });
    const json = vi.fn();
    const res = { status: vi.fn(() => ({ json })), json };
    const next = vi.fn();
    await mw({ path: '/api', headers: paidHeader }, res as never, next);
    expect(next).toHaveBeenCalled();
  });
});
