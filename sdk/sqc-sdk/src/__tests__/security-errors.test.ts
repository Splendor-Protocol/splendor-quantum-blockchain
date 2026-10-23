/**
 * SQDP-1 required error codes are constructible and thrown from real APIs where cheap.
 */
import { describe, expect, it } from 'vitest';
import { BridgeError, isBridgeError, wrapUnknown, mapProviderError } from '../errors.js';
import { assertAddress } from '../address.js';
import { assertFeeAtLeastMin } from '../encoding.js';
import { SqRpc } from '../rpc.js';
import { CHAIN_ID } from '../derive.js';

describe('security-errors SQDP-1 codes', () => {
  const codes = [
    'USER_REJECTED',
    'NO_PROVIDER',
    'TIMEOUT',
    'TX_NOT_CONFIRMED',
    'CHAIN_MISMATCH',
    'INVALID_ADDRESS',
    'INVALID_FEE',
  ] as const;

  it('constructs all required BridgeError codes', () => {
    const errors = [
      BridgeError.userRejected(),
      BridgeError.noProvider(),
      BridgeError.timeout('timed out'),
      BridgeError.txNotConfirmed('not confirmed'),
      BridgeError.chainMismatch('got', 'want'),
      BridgeError.invalidAddress('bad'),
      BridgeError.invalidFee('fee low'),
    ];
    expect(errors.map((e) => e.code).sort()).toEqual([...codes].sort());
    for (const e of errors) {
      expect(isBridgeError(e)).toBe(true);
      expect(typeof e.userHint).toBe('string');
      expect(e.userHint!.length).toBeGreaterThan(0);
    }
  });

  it('INVALID_ADDRESS from assertAddress', () => {
    try {
      assertAddress('0xdead');
      expect.fail('expected throw');
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('INVALID_ADDRESS');
    }
  });

  it('INVALID_FEE from assertFeeAtLeastMin (message) and BridgeError.invalidFee', () => {
    expect(() => assertFeeAtLeastMin('999')).toThrow(/below minimum/);
    const be = BridgeError.invalidFee('fee 999 below minimum 1000');
    expect(be.code).toBe('INVALID_FEE');
  });

  it('BUSY is constructible alongside SQDP-1 codes', () => {
    const busy = BridgeError.busy();
    expect(busy.code).toBe('BUSY');
    expect(isBridgeError(busy)).toBe(true);
    expect(busy.userHint).toBeTruthy();
  });

  it('txBusy is BUSY with transaction wording, distinct from a busy connect', () => {
    const tx = BridgeError.txBusy();
    expect(tx).toBeInstanceOf(BridgeError);
    expect(tx.code).toBe('BUSY');
    expect(tx.message).toMatch(/transaction is already being signed/i);
    expect(tx.userHint).toMatch(/transaction is already in progress/i);
    expect(tx.userHint).not.toBe(BridgeError.busy().userHint);
    expect(BridgeError.txBusy('custom').message).toBe('custom');
    expect(BridgeError.busy('m', 'hint').userHint).toBe('hint');
  });

  it('CHAIN_MISMATCH from assertChainId with missing remote', async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as typeof fetch;
    try {
      const rpc = new SqRpc({ rpcUrl: 'https://x.test', chainId: CHAIN_ID, retries: 0 });
      await expect(rpc.assertChainId()).rejects.toMatchObject({ code: 'CHAIN_MISMATCH' });
    } finally {
      globalThis.fetch = original;
    }
  });

  it('TX_NOT_CONFIRMED from waitForConfirmation timeout', async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ address: 'SQ:' + 'ab'.repeat(32), nonce: 0 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as typeof fetch;
    try {
      const rpc = new SqRpc({ rpcUrl: 'https://x.test', chainId: CHAIN_ID, retries: 0 });
      await expect(
        rpc.waitForConfirmation({
          from: 'SQ:' + 'ab'.repeat(32),
          nonceBefore: 0,
          timeoutMs: 50,
          pollMs: 10,
        }),
      ).rejects.toMatchObject({ code: 'TX_NOT_CONFIRMED' });
    } finally {
      globalThis.fetch = original;
    }
  });

  it('TX_FAILED when receipt status is 0', async () => {
    const original = globalThis.fetch;
    const from = 'SQ:' + 'ab'.repeat(32);
    globalThis.fetch = (async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/receipt')) {
        return new Response(JSON.stringify({ status: 0, error: 'module reverted' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify({ address: from, nonce: 1 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;
    try {
      const rpc = new SqRpc({ rpcUrl: 'https://x.test', chainId: CHAIN_ID, retries: 0 });
      await expect(
        rpc.waitForConfirmation({
          from,
          nonceBefore: 0,
          hash: 'aa'.repeat(32),
          timeoutMs: 200,
          pollMs: 10,
        }),
      ).rejects.toMatchObject({ code: 'TX_FAILED' });
    } finally {
      globalThis.fetch = original;
    }
  });

  it('TIMEOUT from aborted RPC', async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async (_url, init) => {
      const signal = init?.signal;
      return new Promise((_resolve, reject) => {
        if (signal?.aborted) {
          reject(Object.assign(new Error('Aborted'), { name: 'AbortError' }));
          return;
        }
        signal?.addEventListener('abort', () => {
          reject(Object.assign(new Error('Aborted'), { name: 'AbortError' }));
        });
      });
    }) as typeof fetch;
    try {
      const rpc = new SqRpc({
        rpcUrl: 'https://slow.test',
        chainId: CHAIN_ID,
        timeoutMs: 30,
        retries: 0,
      });
      await expect(rpc.getStatus()).rejects.toMatchObject({ code: 'RPC_UNAVAILABLE' });
    } finally {
      globalThis.fetch = original;
    }
  });

  it('USER_REJECTED factory message', () => {
    expect(BridgeError.userRejected().code).toBe('USER_REJECTED');
  });

  it('NO_PROVIDER factory message', () => {
    expect(BridgeError.noProvider().code).toBe('NO_PROVIDER');
  });
});

describe('wrapUnknown', () => {
  it('preserves BridgeError instance and code', () => {
    const original = BridgeError.noProvider();
    const wrapped = wrapUnknown(original, 'USER_REJECTED');
    expect(wrapped).toBe(original);
    expect(wrapped.code).toBe('NO_PROVIDER');
  });

  it('maps provider-missing messages to NO_PROVIDER', () => {
    const w = wrapUnknown(new Error('EIP-1193 provider (window.ethereum) not found'));
    expect(w.code).toBe('NO_PROVIDER');
  });

  it('maps user reject messages to USER_REJECTED', () => {
    const w = wrapUnknown(new Error('User rejected the request'));
    expect(w.code).toBe('USER_REJECTED');
  });
});

describe('mapProviderError SQDP numeric matrix', () => {
  function withCode(code: number, message = 'x') {
    const e = new Error(message) as Error & { code: number };
    e.code = code;
    return e;
  }

  it('maps 4001 → USER_REJECTED', () => {
    expect(mapProviderError(withCode(4001)).code).toBe('USER_REJECTED');
  });

  it('maps 4100 → NOT_CONNECTED with unlock hint when locked', () => {
    const err = mapProviderError(withCode(4100, 'Wallet locked'));
    expect(err.code).toBe('NOT_CONNECTED');
    expect(err.userHint).toMatch(/unlock|locked/i);
  });

  it('maps 4200 → RPC (unsupported for alias retry)', () => {
    expect(mapProviderError(withCode(4200, 'unsupported method')).code).toBe('RPC');
  });

  it('surfaces the wallet message for a 4200 rejection, not the generic network hint', () => {
    const err = mapProviderError(withCode(4200, 'amount must be an integer string in base units'));
    expect(err.code).toBe('RPC');
    expect(err.userHint).toContain('amount must be an integer string in base units');
    expect(mapProviderError(withCode(4200, 'Method not supported: x')).userHint).toBe(
      'Network request failed — try again',
    );
  });

  it('maps 4900 / 4901 / 4902', () => {
    expect(mapProviderError(withCode(4900)).code).toBe('INVALID_ADDRESS');
    expect(mapProviderError(withCode(4901)).code).toBe('INVALID_FEE');
    expect(mapProviderError(withCode(4902)).code).toBe('CHAIN_MISMATCH');
  });

  it('accepts string numeric codes', () => {
    const e = { code: '4100', message: 'Not connected' };
    expect(mapProviderError(e).code).toBe('NOT_CONNECTED');
  });

  it('wrapUnknown delegates to mapProviderError', () => {
    expect(wrapUnknown(withCode(4902)).code).toBe('CHAIN_MISMATCH');
  });
});
