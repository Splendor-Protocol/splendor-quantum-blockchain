import { describe, expect, it, vi } from 'vitest';
import { encodeFunctionDataHex } from '../abi.js';
import { Contract, deployBytecode } from '../contracts/contract.js';
import type { Connection } from '../core/connection.js';
import type { SplendorPQBridge } from '../splendor-wallet.js';
import { buildModuleCall, encodeModuleCallHex } from '../core/module-call.js';
import { Program } from '../core/program.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const ADDR = golden.address;
const CONTRACT = 'SQ:' + 'cc'.repeat(32);

describe('Contract functional', () => {
  it('read encodes ABI and calls connection.call', async () => {
    const call = vi.fn(async (opts: { to: string; data: string }) => {
      expect(opts.to).toBe(CONTRACT);
      expect(opts.data.startsWith('0x')).toBe(true);
      return { return_data: '0x' + '00'.repeat(31) + '2a' };
    });
    const connection = {
      call,
      getContractInfo: vi.fn(),
      verifyContract: vi.fn(),
    } as unknown as Connection;
    const c = new Contract(connection, CONTRACT);
    const n = await c.readUint256('balanceOf(sqaddress)', [ADDR]);
    expect(n).toBe(0x2an);
    expect(call).toHaveBeenCalledOnce();
  });

  it('deployBytecode predicts then deploys', async () => {
    const wallet = {
      getAddress: () => ADDR,
      getNonce: async () => ({ nonce: 5 }),
      deployContract: vi.fn(async () => ({ status: 'ok', hash: '0x1', signedTx: {} })),
    } as unknown as SplendorPQBridge;
    const { address, result } = await deployBytecode(wallet, '0xdead');
    expect(address).toBe(Contract.predictAddress(ADDR, 5));
    expect(result.hash).toBe('0x1');
  });
});

describe('module-call + Program instance key', () => {
  it('round-trips encode/decode', () => {
    const env = buildModuleCall('exchange', 'split', { exchange: ADDR, condition: 1, amount: '10' });
    const hex = encodeModuleCallHex(env);
    const parsed = JSON.parse(Buffer.from(hex.slice(2), 'hex').toString('utf8'));
    expect(parsed).toEqual(env);
  });

  it('Program injects custom instance key', async () => {
    class Ex extends Program {
      protected moduleName() {
        return 'exchange';
      }
      protected instanceParamKey() {
        return 'exchange';
      }
      async split(wallet: SplendorPQBridge, condition: number, amount: string) {
        return this.write(wallet, 'split', { condition, amount });
      }
    }
    const sendModuleCall = vi.fn(async () => ({ status: 'ok', hash: 'h', signedTx: {} }));
    const wallet = { sendModuleCall } as unknown as SplendorPQBridge;
    const conn = { moduleCall: vi.fn() } as unknown as Connection;
    await new Ex(conn, ADDR).split(wallet, 2, '9');
    expect(sendModuleCall.mock.calls[0][0].params).toMatchObject({
      exchange: ADDR,
      condition: 2,
      amount: '9',
    });
  });

  it('encodeFunctionDataHex still works for contracts', () => {
    const hex = encodeFunctionDataHex('transfer(sqaddress,uint256)', [ADDR, 1n]);
    expect(hex.startsWith('0x')).toBe(true);
  });
});
