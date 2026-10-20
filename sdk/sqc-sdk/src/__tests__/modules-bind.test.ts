import { describe, expect, it, vi, afterEach } from 'vitest';
import { createSession } from '../session.js';
import { modules, bindModules } from '../modules.js';
import { getToken } from '../tokens/qrc20.js';
import { getPool } from '../defi/dex.js';
import { fromUnits } from '../units.js';
import type { Connection } from '../core/connection.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const ADDR = 'SQ:' + 'aa'.repeat(32);
const TO = 'SQ:' + 'bb'.repeat(32);
const POOL = 'SQ:' + 'cc'.repeat(32);

function fakeConnection(): Connection {
  return {
    chainId: 'splendor-quantum-1',
    moduleCall: vi.fn(),
    getBalance: vi.fn(),
  } as unknown as Connection;
}

describe('modules.bind / bindModules', () => {
  it('address-only factories bind connection', () => {
    const connection = fakeConnection();
    const bound = modules.bind(connection);
    expect(bound.qrc20(ADDR).getAddress()).toBe(ADDR);
    expect(bound.qrc721(ADDR).getAddress()).toBe(ADDR);
    expect(bound.bridge(ADDR).getAddress()).toBe(ADDR);
    expect(bound.contract(ADDR).getAddress()).toBe(ADDR);
    expect(bound).not.toHaveProperty('dex');
    expect(bound).not.toHaveProperty('exchange');
  });

  it('with wallet injects signer on writes', async () => {
    const connection = fakeConnection();
    const wallet = {
      sendModuleCall: vi.fn(async () => ({
        status: 'ok',
        hash: '0xswap',
        signedTx: {},
      })),
    };
    const signed = bindModules(connection, wallet);
    expect(signed).not.toHaveProperty('dex');
    expect(wallet.sendModuleCall).not.toHaveBeenCalled();
  });
});

describe('QRC20 withSigner + transferHuman', () => {
  it('transferHuman converts decimals then transfers', async () => {
    const connection = fakeConnection();
    const wallet = {
      sendModuleCall: vi.fn(async () => ({ status: 'ok', hash: '0xh', signedTx: {} })),
    };
    const token = getToken(connection, ADDR);
    vi.spyOn(token, 'tokenInfo').mockResolvedValue({
      module: 'qrc20',
      action: 'token_info',
      token: ADDR,
      decimals: 18,
    });

    await token.transferHuman(wallet as never, TO, '1.5');
    expect(wallet.sendModuleCall).toHaveBeenCalledWith(
      expect.objectContaining({
        module: 'qrc20',
        action: 'transfer',
        params: expect.objectContaining({
          to: TO,
          amount: fromUnits('1.5', 18),
        }),
      }),
    );
  });

  it('bound transfer omits wallet arg', async () => {
    const connection = fakeConnection();
    const wallet = {
      sendModuleCall: vi.fn(async () => ({ status: 'ok', hash: '0xt', signedTx: {} })),
    };
    const bound = getToken(connection, ADDR).withSigner(wallet);
    await bound.transfer(TO, '42');
    expect(wallet.sendModuleCall).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'transfer',
        params: expect.objectContaining({ to: TO, amount: '42' }),
      }),
    );
  });

  it('bound pool swap omits wallet arg', async () => {
    const connection = fakeConnection();
    const wallet = {
      sendModuleCall: vi.fn(async () => ({ status: 'ok', hash: '0xp', signedTx: {} })),
    };
    await getPool(connection, POOL).withSigner(wallet).swap({
      tokenIn: 1,
      amountIn: '9',
    });
    expect(wallet.sendModuleCall).toHaveBeenCalledOnce();
  });
});

describe('createSession write wiring', () => {
  const sessions: Array<{ wallet: { disconnect: () => void } }> = [];

  afterEach(() => {
    for (const s of sessions) {
      try {
        s.wallet.disconnect();
      } catch {
        /* ignore */
      }
    }
    sessions.length = 0;
  });

  it('session.token().transfer does not require wallet arg', async () => {
    const session = await createSession({
      network: 'local',
      mode: 'seed',
      seed: golden.seedHex,
    });
    sessions.push(session);

    const spy = vi.spyOn(session.wallet, 'sendModuleCall').mockResolvedValue({
      status: 'ok',
      hash: '0xbound',
      signedTx: {},
    });

    await session.token(ADDR).transfer(TO, '7');
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({
        module: 'qrc20',
        action: 'transfer',
        params: expect.objectContaining({ token: ADDR, to: TO, amount: '7' }),
      }),
    );
    spy.mockRestore();
  }, 30_000);
});
