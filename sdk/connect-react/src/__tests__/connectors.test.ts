import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import {
  metamaskConnector,
  injectedConnector,
  seedConnector,
  assertSeedHex,
  generateSeedHex,
  defaultBrowserConnectors,
  hasAuthorizedAccounts,
} from '../connectors/index.js';
import type { DeriveInfo, EthereumProvider } from '@splendor/sqc-sdk';

const mockInfo: DeriveInfo = {
  address: 'SQ:' + 'cd'.repeat(32),
  public_key_b64: 'cA==',
  public_key_hex: '70',
};

describe('assertSeedHex', () => {
  it('accepts 64 hex chars', () => {
    expect(assertSeedHex('ab'.repeat(32))).toBe('ab'.repeat(32));
  });

  it('strips 0x prefix', () => {
    expect(assertSeedHex('0x' + '11'.repeat(32))).toBe('11'.repeat(32));
  });

  it('rejects empty / short / non-hex', () => {
    expect(() => assertSeedHex('')).toThrow(/required/i);
    expect(() => assertSeedHex('abcd')).toThrow(/32 bytes/i);
    expect(() => assertSeedHex('zz'.repeat(32))).toThrow(/32 bytes/i);
  });
});

describe('generateSeedHex', () => {
  it('produces a seed the connector accepts', () => {
    const seed = generateSeedHex();
    expect(seed).toMatch(/^[0-9a-f]{64}$/);
    expect(assertSeedHex(seed)).toBe(seed);
  });

  it('does not repeat itself', () => {
    const seeds = new Set(Array.from({ length: 16 }, () => generateSeedHex()));
    expect(seeds.size).toBe(16);
  });
});

describe('connector ordering', () => {
  afterEach(() => {
    delete (globalThis as { ethereum?: unknown }).ethereum;
  });

  // Leading with an EVM-derived wallet on a post-quantum chain is the exact
  // claim this package must not make, so the order is a test, not a habit.
  it('offers the native ML-DSA connector first and EVM-derived last', async () => {
    const list = await defaultBrowserConnectors();
    expect(list[0].keyOrigin).toBe('native');
    expect(list[0].postQuantumSecure).toBe(true);
    expect(list[list.length - 1].keyOrigin).toBe('evm-derived');
    expect(list.some((c) => c.id === 'metamask')).toBe(true);
  });

  it('keeps the native connector out of the Advanced accordion', async () => {
    const [first] = await defaultBrowserConnectors();
    expect(first.advanced).toBeUndefined();
  });
});

describe('post-quantum labelling', () => {
  // A connector that silently claims to be quantum-safe is worse than one that
  // is honest about being derived from ECDSA, so these flags are asserted
  // rather than left to review.
  it('marks every EVM-derived connector as not post-quantum', () => {
    for (const c of [
      metamaskConnector(),
      injectedConnector({ ethereumProvider: {} as EthereumProvider }),
    ]) {
      expect(c.keyOrigin).toBe('evm-derived');
      expect(c.postQuantumSecure).toBe(false);
    }
  });

  it('marks the native seed connector as post-quantum', () => {
    const c = seedConnector();
    expect(c.keyOrigin).toBe('native');
    expect(c.postQuantumSecure).toBe(true);
  });
});

describe('metamaskConnector', () => {
  afterEach(() => {
    delete (globalThis as { ethereum?: unknown }).ethereum;
  });

  it('ready false without ethereum', () => {
    const c = metamaskConnector();
    expect(c.ready()).toBe(false);
  });

  it('ready true with ethereum.request', () => {
    (globalThis as { ethereum?: EthereumProvider }).ethereum = {
      request: vi.fn(),
    };
    expect(metamaskConnector().ready()).toBe(true);
  });

  it('connect calls wallet.connectFromMetaMask', async () => {
    const provider: EthereumProvider = { request: vi.fn() };
    (globalThis as { ethereum?: EthereumProvider }).ethereum = provider;
    const connectFromMetaMask = vi.fn(async () => mockInfo);
    const c = metamaskConnector();
    const info = await c.connect({
      wallet: { connectFromMetaMask } as never,
      connection: {} as never,
      provider: {} as never,
      network: {} as never,
    });
    expect(info).toBe(mockInfo);
    expect(connectFromMetaMask).toHaveBeenCalledWith(provider);
  });
});

describe('injectedConnector', () => {
  it('uses provided provider', async () => {
    const provider: EthereumProvider = { request: vi.fn() };
    const connectFromMetaMask = vi.fn(async () => mockInfo);
    const c = injectedConnector({ ethereumProvider: provider, name: 'Rabby' });
    expect(c.ready()).toBe(true);
    expect(c.name).toBe('Rabby');
    await c.connect({
      wallet: { connectFromMetaMask } as never,
      connection: {} as never,
      provider: {} as never,
      network: {} as never,
    });
    expect(connectFromMetaMask).toHaveBeenCalledWith(provider);
  });
});

describe('seedConnector', () => {
  it('rejects bad seed before wallet', async () => {
    const connectFromSeed = vi.fn();
    const c = seedConnector();
    await expect(
      c.connect(
        {
          wallet: { connectFromSeed } as never,
          connection: {} as never,
          provider: {} as never,
          network: {} as never,
        },
        { seed: 'bad' },
      ),
    ).rejects.toThrow(/32 bytes/i);
    expect(connectFromSeed).not.toHaveBeenCalled();
  });

  it('calls connectFromSeed with normalized hex', async () => {
    const connectFromSeed = vi.fn(async () => mockInfo);
    const c = seedConnector();
    expect(c.connectFields?.[0]?.name).toBe('seed');
    const seed = '0x' + 'aa'.repeat(32);
    await c.connect(
      {
        wallet: { connectFromSeed } as never,
        connection: {} as never,
        provider: {} as never,
        network: {} as never,
      },
      { seed },
    );
    expect(connectFromSeed).toHaveBeenCalledWith('aa'.repeat(32));
  });
});

describe('hasAuthorizedAccounts', () => {
  beforeEach(() => {
    delete (globalThis as { ethereum?: unknown }).ethereum;
  });

  it('returns false without provider', async () => {
    expect(await hasAuthorizedAccounts()).toBe(false);
  });

  it('returns false when eth_accounts empty', async () => {
    const provider: EthereumProvider = {
      request: vi.fn(async () => []),
    };
    expect(await hasAuthorizedAccounts(provider)).toBe(false);
  });

  it('returns true when eth_accounts non-empty', async () => {
    const provider: EthereumProvider = {
      request: vi.fn(async () => ['0xabc']),
    };
    expect(await hasAuthorizedAccounts(provider)).toBe(true);
  });
});
