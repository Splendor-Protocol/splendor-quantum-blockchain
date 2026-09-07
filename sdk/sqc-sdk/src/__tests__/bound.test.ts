import { describe, expect, it, vi } from 'vitest';
import { withSigner } from '../bound.js';

describe('withSigner', () => {
  it('injects wallet into write methods and leaves reads alone', async () => {
    const wallet = { sendModuleCall: vi.fn() };
    const client = {
      balanceOf: vi.fn(async (owner: string) => `bal:${owner}`),
      transfer: vi.fn(async (_w: typeof wallet, to: string, amount: string) => ({
        to,
        amount,
      })),
      withSigner() {
        return withSigner(this, wallet, new Set(['transfer']));
      },
    };

    const bound = client.withSigner();
    await expect(bound.balanceOf('SQ:abc')).resolves.toBe('bal:SQ:abc');
    expect(client.balanceOf).toHaveBeenCalledWith('SQ:abc');

    await bound.transfer('SQ:to', '100');
    expect(client.transfer).toHaveBeenCalledWith(wallet, 'SQ:to', '100');

    // Explicit wallet still works
    await bound.transfer(wallet as never, 'SQ:x', '1');
    expect(client.transfer).toHaveBeenLastCalledWith(wallet, 'SQ:x', '1');
  });
});
