/**
 * Factory connectors must reuse the shared Connection (sticky RPC / chain assert).
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { seedConnector } from '../wallet/connectors/seed.js';
import { SplendorPQBridge } from '../splendor-wallet.js';
import { CHAIN_ID } from '../derive.js';
import { resetWasmLoader } from '../wasm-loader.js';

afterEach(() => {
  resetWasmLoader();
  vi.restoreAllMocks();
});

describe('wallet factory connectors', () => {
  it('seedConnector attaches the provided Connection instance', async () => {
    const connection = new Connection({
      rpcUrl: 'http://127.0.0.1:9',
      chainId: CHAIN_ID,
      retries: 0,
    });
    vi.spyOn(SplendorPQBridge.prototype, 'init').mockResolvedValue(undefined);
    vi.spyOn(SplendorPQBridge.prototype, 'connectFromSeed').mockResolvedValue({
      address: 'SQ:' + 'ab'.repeat(32),
      public_key_b64: 'pk',
      keyOrigin: 'native',
      postQuantumSecure: true,
    } as never);

    const wallet = await seedConnector.connect(connection, {
      seed: '01'.repeat(32),
      allowUnpinnedWasm: true,
    });
    expect(wallet.connection).toBe(connection);
  });
});
