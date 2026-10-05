import { describe, expect, it } from 'vitest';
import { inspect } from 'node:util';
import { SplendorPQBridge } from '../splendor-wallet.js';
import { Keypair } from '../wallet/keypair.js';

const SEED = '5e'.repeat(32);

describe('seed secrecy', () => {
  it('Keypair never serializes or prints its seed', () => {
    const kp = Keypair.fromSeed('0x' + SEED.toUpperCase());
    expect(kp.seedHex).toBe(SEED);
    expect(JSON.stringify(kp)).not.toContain(SEED);
    expect(String(kp)).not.toContain(SEED);
    expect(inspect(kp)).not.toContain(SEED);
    expect(Object.keys(kp)).not.toContain('seedHex');
  });

  it('Keypair.fromSeed errors do not echo the input', () => {
    const bad = SEED.slice(0, 62) + 'zz';
    try {
      Keypair.fromSeed(bad);
      expect.fail('expected throw');
    } catch (e) {
      expect(String((e as Error).message)).not.toContain(bad);
    }
  });

  it('the wallet does not keep a seed passed in its options', () => {
    const opts = {
      networkId: 'testnet',
      rpcUrls: ['https://rpc.test'],
      chainId: 'splendor-quantum-1',
      seed: SEED,
    } as ConstructorParameters<typeof SplendorPQBridge>[0] & { seed: string };
    const wallet = new SplendorPQBridge(opts);
    const stored = (wallet as unknown as { opts: Record<string, unknown> }).opts;
    expect(stored).not.toHaveProperty('seed');
    expect(JSON.stringify(stored)).not.toContain(SEED);
  });
});
