import { describe, expect, it, afterEach, vi } from 'vitest';
import { createSession } from '../session.js';
import { qrc721Address } from '../addresses/index.js';
import { modules } from '../modules.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

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

describe('createSession', () => {
  it('binds address, modules, and wallet-free writes', async () => {
    const session = await createSession({
      network: 'local',
      mode: 'seed',
      seed: golden.seedHex,
    });
    sessions.push(session);

    expect(session.address()).toBe(golden.address);
    expect(session.info?.address).toBe(golden.address);
    expect(session.connection.chainId).toBe(golden.chainId);

    const tokenAddr = 'SQ:' + 'aa'.repeat(32);
    const token = session.token(tokenAddr);
    expect(token.getAddress()).toBe(tokenAddr);
    expect(session.modules.qrc20(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.qrc20(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.bridge(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.collection(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.nft(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.qrc721(tokenAddr).getAddress()).toBe(tokenAddr);
    expect(typeof session.deployNftCollection).toBe('function');
    expect(session).not.toHaveProperty('dex');
    expect(session).not.toHaveProperty('exchange');
    expect(session).not.toHaveProperty('deployPool');
    expect(session).not.toHaveProperty('deployExchange');

    const contract = session.contract(tokenAddr);
    expect(contract.getAddress()).toBe(tokenAddr);

    // Global unbound registry still needs connection
    expect(modules.qrc20(session.connection, tokenAddr).getAddress()).toBe(tokenAddr);
    expect(session.modulesUnbound.qrc20(session.connection, tokenAddr).getAddress()).toBe(
      tokenAddr,
    );

    expect(typeof session.sendSPLD).toBe('function');
    expect(typeof session.getBalance).toBe('function');
    expect(typeof session.deployToken).toBe('function');
    expect(typeof session.deployBridge).toBe('function');
  }, 30_000);

  it('deployNftCollection deploys through the session wallet and returns a bound collection', async () => {
    const session = await createSession({ network: 'local', mode: 'seed', seed: golden.seedHex });
    sessions.push(session);
    const send = vi
      .spyOn(session.wallet, 'sendModuleCall')
      .mockResolvedValue({ status: 'submitted', hash: 'nft-hash', signedTx: { nonce: 3 } } as never);
    vi.spyOn(session.wallet, 'getNonce').mockResolvedValue({ nonce: 1 } as never);

    const res = await session.deployNftCollection({ name: 'Art', symbol: 'ART' }, { fee: '5000' });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0]![0]).toMatchObject({ module: 'qrc721', action: 'deploy', params: { name: 'Art', symbol: 'ART' }, fee: '5000' });
    expect(res.hash).toBe('nft-hash');
    expect(res.address).toBe(qrc721Address(golden.address, 3));
    expect(res.nft.getAddress()).toBe(res.address);
  }, 30_000);

  it('mode none leaves wallet disconnected (address throws)', async () => {
    const session = await createSession({ network: 'local', mode: 'none' });
    sessions.push(session);
    expect(session.info).toBeNull();
    expect(() => session.address()).toThrow();
  });
});
