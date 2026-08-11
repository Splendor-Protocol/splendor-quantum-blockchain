import { describe, expect, it, vi, beforeEach } from 'vitest';
import { createConnectController, connectErrorMessage } from '../core/controller.js';
import type { Connector } from '../connectors/types.js';
import type { DeriveInfo } from '@splendor/pq-bridge';
import { BridgeError } from '@splendor/pq-bridge';

const memoryStore = new Map<string, string>();
vi.stubGlobal('sessionStorage', {
  getItem: (k: string) => memoryStore.get(k) ?? null,
  setItem: (k: string, v: string) => {
    memoryStore.set(k, v);
  },
  removeItem: (k: string) => {
    memoryStore.delete(k);
  },
  clear: () => memoryStore.clear(),
});

const mockInfo: DeriveInfo = {
  address: 'SQ:' + 'ab'.repeat(32),
  public_key_b64: 'dGVzdA==',
  public_key_hex: '74657374',
  evmAddress: '0x1111111111111111111111111111111111111111',
};

function mockWallet() {
  let info: DeriveInfo | null = null;
  const listeners = new Map<string, Set<(p: unknown) => void>>();
  return {
    init: vi.fn(async () => undefined),
    connectFromMetaMask: vi.fn(async () => {
      info = mockInfo;
      return mockInfo;
    }),
    connectFromSeed: vi.fn(async (seed: string) => {
      if (!seed) throw new Error('no seed');
      info = { ...mockInfo, evmAddress: undefined };
      return info;
    }),
    disconnect: vi.fn(() => {
      info = null;
      listeners.get('disconnect')?.forEach((fn) => fn({}));
    }),
    getDeriveInfo: () => info,
    getConnectionState: () => (info ? 'connected' : 'disconnected'),
    on: (event: string, handler: (p: unknown) => void) => {
      if (!listeners.has(event)) listeners.set(event, new Set());
      listeners.get(event)!.add(handler);
      return () => listeners.get(event)?.delete(handler);
    },
  };
}

vi.mock('@splendor/pq-bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@splendor/pq-bridge')>();
  return {
    ...actual,
    createSplendorClient: vi.fn(() => {
      const wallet = mockWallet();
      return {
        network: {
          id: 'local',
          name: 'Local',
          chainId: 'splendor-quantum-1',
          rpcUrls: ['http://127.0.0.1:8080'],
        },
        provider: {
          getBalance: vi.fn(async () => ({
            address: mockInfo.address,
            balance: '0',
            balance_spld: '0',
            unit: 'atto',
          })),
        },
        wallet,
        info: null,
      };
    }),
  };
});

describe('createConnectController', () => {
  beforeEach(() => {
    memoryStore.clear();
  });

  it('requires at least one connector', () => {
    expect(() =>
      createConnectController({ network: 'local', connectors: [] }),
    ).toThrow(/at least one connector/);
  });

  it('connects via metamask connector and persists last id', async () => {
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: async (ctx) => ctx.wallet.connectFromMetaMask(),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    const info = await ctrl.connect('metamask');
    expect(info.address).toMatch(/^SQ:/);
    expect(ctrl.getState().status).toBe('connected');
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('metamask');
    ctrl.destroy();
  });

  it('does not persist seed connector id', async () => {
    const connector: Connector = {
      id: 'seed',
      name: 'Seed',
      advanced: true,
      ready: () => true,
      connect: async (ctx, opts) => ctx.wallet.connectFromSeed(String(opts?.seed ?? '')),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('seed', { seed: 'aa'.repeat(32) });
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBeUndefined();
    ctrl.destroy();
  });

  it('maps NO_PROVIDER when connector not ready', async () => {
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => false,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('metamask')).rejects.toMatchObject({ code: 'NO_PROVIDER' });
    expect(ctrl.getState().status).toBe('error');
    ctrl.destroy();
  });
});

describe('connectErrorMessage', () => {
  it('maps BridgeError codes', () => {
    expect(connectErrorMessage(BridgeError.userRejected())).toMatch(/rejected/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toMatch(/wallet/i);
  });
});
