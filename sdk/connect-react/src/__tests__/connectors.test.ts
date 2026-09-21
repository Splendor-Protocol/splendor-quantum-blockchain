import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  seedConnector,
  extensionConnector,
  assertSeedHex,
  generateSeedHex,
  defaultBrowserConnectors,
} from '../connectors/index.js';
import type { ConnectorContext } from '../connectors/types.js';
import type { DeriveInfo } from '@splendor/sqc-sdk';

const mockInfo: DeriveInfo = {
  address: 'SQ:' + 'ab'.repeat(32),
  public_key_b64: 'pk',
  public_key_hex: 'ab'.repeat(32),
  keyOrigin: 'native',
  postQuantumSecure: true,
};

function mockCtx(overrides: Partial<ConnectorContext> = {}): ConnectorContext {
  return {
    wallet: {
      connectFromSeed: vi.fn(async () => mockInfo),
    } as never,
    connection: {} as never,
    provider: {} as never,
    network: { id: 'local' } as never,
    ...overrides,
  };
}

describe('seedConnector', () => {
  it('validates seed and calls connectFromSeed', async () => {
    const ctx = mockCtx();
    const c = seedConnector();
    expect(c.keyOrigin).toBe('native');
    expect(c.postQuantumSecure).toBe(true);
    const info = await c.connect(ctx, { seed: '01'.repeat(32) });
    expect(info.address).toMatch(/^SQ:/);
    expect(ctx.wallet.connectFromSeed).toHaveBeenCalled();
  });

  it('assertSeedHex rejects bad input', () => {
    expect(() => assertSeedHex('zz')).toThrow(/64/);
    expect(assertSeedHex('01'.repeat(32))).toHaveLength(64);
  });

  it('generateSeedHex returns 64 hex chars', () => {
    expect(generateSeedHex()).toMatch(/^[0-9a-f]{64}$/);
  });
});

describe('extensionConnector', () => {
  afterEach(() => {
    delete (globalThis as { window?: unknown }).window;
  });

  it('ready is false without window.splendor', () => {
    (globalThis as { window?: Window }).window = globalThis as unknown as Window;
    delete (window as { splendor?: unknown }).splendor;
    expect(extensionConnector().ready()).toBe(false);
  });

  it('ready is true for extension inject', () => {
    (globalThis as { window?: Window }).window = globalThis as unknown as Window;
    window.splendor = {
      isSplendor: true,
      request: async () => [],
      on() {},
      removeListener() {},
      once() {},
    };
    expect(extensionConnector().ready()).toBe(true);
  });
});

describe('defaultBrowserConnectors', () => {
  it('returns extension then seed only', async () => {
    const list = await defaultBrowserConnectors();
    expect(list.map((c) => c.id)).toEqual(['splendor-extension', 'seed']);
  });
});



