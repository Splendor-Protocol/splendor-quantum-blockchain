import { describe, expect, it, afterEach } from 'vitest';
import { connect } from '../wallet/connect.js';
import golden from '../../testdata/golden-vectors.json' with { type: 'json' };

const bridges: Array<{ disconnect: () => void }> = [];

afterEach(() => {
  for (const b of bridges) {
    try {
      b.disconnect();
    } catch {
      /* ignore */
    }
  }
  bridges.length = 0;
});

describe('connect() API', () => {
  it('mode seed returns wallet, connection, info, network', async () => {
    const res = await connect({
      network: 'local',
      mode: 'seed',
      seed: golden.seedHex,
    });
    bridges.push(res.wallet);
    expect(res.info?.address).toBe(golden.address);
    expect(res.wallet.getAddress()).toBe(golden.address);
    expect(res.connection.chainId).toBe(golden.chainId);
    expect(res.network.id).toBe('local');
    expect(res.client.wallet).toBe(res.wallet);
  }, 30_000);

  it('mode none leaves info null', async () => {
    const res = await connect({ network: 'local', mode: 'none' });
    bridges.push(res.wallet);
    expect(res.info).toBeNull();
    expect(res.wallet.getConnectionState()).toBe('disconnected');
  });

  it('unknown network throws', async () => {
    await expect(connect({ network: 'nope' as 'local' })).rejects.toThrow();
  });
});
