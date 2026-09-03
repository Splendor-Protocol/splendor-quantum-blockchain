import { describe, expect, it } from 'vitest';
import { MockTransport } from '../core/transport/mock-transport.js';
import { Connection } from '../core/connection.js';
import { MockSigner } from '../core/signer.js';
import { MODULE_ROUTER_ADDRESS } from '../addresses/index.js';

describe('MockTransport', () => {
  it('records get/post/moduleCall and returns scripted data', async () => {
    const t = new MockTransport({
      handlers: {
        get: (path) => ({ path, ok: true }),
        post: (_path, body) => ({ echoed: body }),
        moduleCall: (env) => ({ module: env.module, action: env.action, balance: '1' }),
      },
    });
    expect(await t.get('/status')).toEqual({ path: '/status', ok: true });
    expect(await t.post('/tx/submit', { a: 1 })).toEqual({ echoed: { a: 1 } });
    expect(await t.moduleCall({ module: 'qrc20', action: 'balance_of', params: {} })).toEqual({
      module: 'qrc20',
      action: 'balance_of',
      balance: '1',
    });
    expect(t.calls.get).toEqual(['/status']);
    expect(t.calls.moduleCall[0]?.module).toBe('qrc20');
  });

  it('Connection.moduleCall uses injected MockTransport', async () => {
    const transport = new MockTransport({
      handlers: {
        moduleCall: () => ({ balance: '99' }),
      },
    });
    const conn = new Connection({ transport });
    const res = await conn.moduleCall<{ balance: string }>({
      module: 'qrc20',
      action: 'balance_of',
      params: { token: 'SQ:' + 'aa'.repeat(32), owner: 'SQ:' + 'bb'.repeat(32) },
    });
    expect(res.balance).toBe('99');
    expect(conn.transport).toBe(transport);
    expect(conn.chainId).toBe(transport.chainId);
  });
});

describe('MockSigner', () => {
  it('records sendModuleCall', async () => {
    const s = new MockSigner('SQ:' + 'aa'.repeat(32));
    const out = await s.sendModuleCall({
      module: 'qrc20',
      action: 'transfer',
      params: { to: 'SQ:' + 'bb'.repeat(32), amount: '1' },
    });
    expect(out.hash).toMatch(/^0x/);
    expect(s.calls.sendModuleCall).toHaveLength(1);
    expect(s.getAddress()).toMatch(/^SQ:/);
  });

  it('records callContract', async () => {
    const s = new MockSigner('SQ:' + 'aa'.repeat(32));
    await s.callContract({
      contract: MODULE_ROUTER_ADDRESS,
      function: 'noop()',
      args: [],
    });
    expect(s.calls.callContract).toHaveLength(1);
  });
});
