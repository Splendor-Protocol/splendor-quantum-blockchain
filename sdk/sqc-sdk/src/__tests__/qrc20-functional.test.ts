import { describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { QRC20, deployQRC20, getToken } from '../tokens/qrc20.js';
import { MODULE_ROUTER_ADDRESS } from '../addresses/index.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const TOKEN = 'SQ:' + 'aa'.repeat(32);
const OWNER = golden.address;

function mockConnection(moduleCall: ReturnType<typeof vi.fn>): Connection {
  return {
    moduleCall,
    rpc: {} as Connection['rpc'],
    transport: {} as Connection['transport'],
    chainId: golden.chainId,
  } as unknown as Connection;
}

function mockWallet(overrides: Partial<SplendorPQBridge> = {}): SplendorPQBridge {
  return {
    getAddress: () => OWNER,
    getNonce: async () => ({ nonce: 3 }),
    sendModuleCall: vi.fn(async (opts) => ({
      status: 'ok',
      hash: '0xabc',
      signedTx: { module: opts.module, action: opts.action, params: opts.params },
    })),
    connection: mockConnection(vi.fn()),
    ...overrides,
  } as unknown as SplendorPQBridge;
}

describe('QRC20 functional (mocked transport)', () => {
  it('balanceOf / tokenInfo build envelopes with token injected', async () => {
    const moduleCall = vi.fn(async (envelope: { module: string; action: string; params: Record<string, unknown> }) => {
      expect(envelope.module).toBe('qrc20');
      if (envelope.action === 'balance_of') {
        expect(envelope.params.token).toBe(TOKEN);
        expect(envelope.params.owner).toBe(OWNER);
        return { balance: '100' };
      }
      if (envelope.action === 'token_info') {
        return { name: 'T', symbol: 'T', token: TOKEN };
      }
      throw new Error('unexpected ' + envelope.action);
    });
    const token = getToken(mockConnection(moduleCall), TOKEN);
    expect(await token.balanceOf(OWNER)).toBe('100');
    expect((await token.tokenInfo()).name).toBe('T');
    expect(moduleCall).toHaveBeenCalledTimes(2);
  });

  it('transfer / approve call sendModuleCall with expected params', async () => {
    const wallet = mockWallet();
    const token = new QRC20(mockConnection(vi.fn()), TOKEN);
    await token.transfer(wallet, OWNER, '50');
    await token.approve(wallet, OWNER, '10');

    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send).toHaveBeenCalledTimes(2);
    expect(send.mock.calls[0][0]).toMatchObject({
      module: 'qrc20',
      action: 'transfer',
      params: { token: TOKEN, to: OWNER, amount: '50' },
    });
    expect(send.mock.calls[1][0]).toMatchObject({
      module: 'qrc20',
      action: 'approve',
      params: { token: TOKEN, spender: OWNER, amount: '10' },
    });
  });

  it('deployQRC20 predicts address and sends deploy without token key', async () => {
    const wallet = mockWallet();
    const { address, result } = await deployQRC20(wallet, {
      name: 'My',
      symbol: 'MY',
      decimals: 18,
      totalSupply: '1000',
    });
    expect(address.startsWith('SQ:')).toBe(true);
    expect(address).toBe(QRC20.predictAddress(OWNER, 3));
    expect(result.hash).toBe('0xabc');
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[0][0]).toMatchObject({
      module: 'qrc20',
      action: 'deploy',
      params: {
        name: 'My',
        symbol: 'MY',
        decimals: 18,
        total_supply: '1000',
      },
    });
    expect(send.mock.calls[0][0].params.token).toBeUndefined();
  });

  it('transferHuman forwards the fee and confirmation options to sendModuleCall', async () => {
    const wallet = mockWallet();
    const moduleCall = vi.fn(async () => ({ name: 'T', symbol: 'T', decimals: 6 }));
    const token = new QRC20(mockConnection(moduleCall), TOKEN);
    await token.transferHuman(wallet, OWNER, '1.5', {
      fee: '12345',
      waitForConfirmation: true,
      confirmTimeoutMs: 1000,
      params: { amount: '999' },
    } as never);
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[0][0]).toMatchObject({
      module: 'qrc20',
      action: 'transfer',
      params: { token: TOKEN, to: OWNER, amount: '1500000' },
      fee: '12345',
      waitForConfirmation: true,
      confirmTimeoutMs: 1000,
    });
  });

  it('deployQRC20 takes the address from the signed nonce, not the pre-send read', async () => {
    const wallet = mockWallet({
      sendModuleCall: vi.fn(async () => ({ status: 'ok', hash: '0xabc', signedTx: { nonce: '8' } })),
    } as Partial<SplendorPQBridge>);
    const { address } = await deployQRC20(wallet, { name: 'My', symbol: 'MY', decimals: 18, totalSupply: '1' });
    expect(address).toBe(QRC20.predictAddress(OWNER, 8));
  });

  it('submitPermit forwards signed fields', async () => {
    const wallet = mockWallet();
    const token = new QRC20(mockConnection(vi.fn()), TOKEN);
    await token.submitPermit(wallet, {
      owner: OWNER,
      token: TOKEN,
      spender: OWNER,
      amount: '1',
      nonce: 1,
      deadline: 99,
      chain_id: golden.chainId,
      digest_hex: 'ab',
      signature: [1, 2],
      owner_pubkey: [3, 4],
    });
    const send = wallet.sendModuleCall as ReturnType<typeof vi.fn>;
    expect(send.mock.calls[0][0].action).toBe('permit');
    expect(send.mock.calls[0][0].params.signature).toEqual([1, 2]);
  });

  it('MODULE_ROUTER_ADDRESS is pinned', () => {
    expect(MODULE_ROUTER_ADDRESS).toBe(golden.moduleRouterAddress);
  });
});
