import { describe, expect, it, vi, beforeEach } from 'vitest';
import { createConnectController, connectErrorMessage } from '../core/controller.js';
import type { Connector } from '../connectors/types.js';
import type { DeriveInfo } from '@splendor/sqc-sdk';
import { BridgeError } from '@splendor/sqc-sdk';

const memoryStore = new Map<string, string>();
vi.stubGlobal('localStorage', {
  getItem: (k: string) => memoryStore.get(k) ?? null,
  setItem: (k: string, v: string) => {
    memoryStore.set(k, v);
  },
  removeItem: (k: string) => {
    memoryStore.delete(k);
  },
  clear: () => memoryStore.clear(),
});
vi.stubGlobal('sessionStorage', {
  getItem: () => null,
  setItem: () => undefined,
  removeItem: () => undefined,
  clear: () => undefined,
});

const mockInfo: DeriveInfo = {
  address: 'SQ:' + 'ab'.repeat(32),
  public_key_b64: 'dGVzdA==',
  public_key_hex: '74657374',
  evmAddress: '0x1111111111111111111111111111111111111111',
};

type MockWallet = ReturnType<typeof mockWallet>;

function mockWallet() {
  let info: DeriveInfo | null = null;
  const listeners = new Map<string, Set<(p: unknown) => void>>();
  const wallet = {
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
    getConnectionState: () => (info ? ('connected' as const) : ('disconnected' as const)),
    on: (event: string, handler: (p: unknown) => void) => {
      if (!listeners.has(event)) listeners.set(event, new Set());
      listeners.get(event)!.add(handler);
      return () => listeners.get(event)?.delete(handler);
    },
    emit(event: string, payload: unknown) {
      listeners.get(event)?.forEach((fn) => fn(payload));
    },
    lock: vi.fn(() => {
      info = null;
      listeners.get('lock')?.forEach((fn) => fn({}));
    }),
  };
  return wallet;
}

let lastWallet: MockWallet;

vi.mock('@splendor/sqc-sdk', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@splendor/sqc-sdk')>();
  return {
    ...actual,
    createSplendorClient: vi.fn(() => {
      lastWallet = mockWallet();
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
        wallet: lastWallet,
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

  it('does not persist advanced connector id', async () => {
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

  it('does not persist a connector whose secret the user types in', async () => {
    // The native seed connector is no longer "advanced", but a reload still
    // cannot resume it — persisting its id would auto-fire a connect with an
    // empty seed and strand the UI in `error`.
    const connector: Connector = {
      id: 'seed',
      name: 'Splendor Quantum Wallet',
      keyOrigin: 'native',
      postQuantumSecure: true,
      connectFields: [{ name: 'seed', type: 'password', label: 'ML-DSA-65 seed' }],
      ready: () => true,
      connect: async (ctx, opts) => ctx.wallet.connectFromSeed(String(opts?.seed ?? '')),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    await ctrl.connect('seed', { seed: 'bb'.repeat(32) });
    expect(ctrl.getState().status).toBe('connected');
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBeUndefined();
    ctrl.destroy();
  });

  it('autoReconnect skips a connector that needs a typed secret', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'seed');
    const connect = vi.fn(async (ctx: never, opts?: Record<string, unknown>) =>
      (ctx as unknown as { wallet: { connectFromSeed: (s: string) => Promise<DeriveInfo> } }).wallet.connectFromSeed(
        String(opts?.seed ?? ''),
      ),
    );
    const connector: Connector = {
      id: 'seed',
      name: 'Splendor Quantum Wallet',
      keyOrigin: 'native',
      postQuantumSecure: true,
      connectFields: [{ name: 'seed', type: 'password', label: 'ML-DSA-65 seed' }],
      ready: () => true,
      connect: connect as unknown as Connector['connect'],
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    expect(connect).not.toHaveBeenCalled();
    expect(ctrl.getState().status).toBe('disconnected');
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
    expect(ctrl.getState().activeConnectorId).toBeNull();
    ctrl.destroy();
  });

  it('clears activeConnectorId on connect failure', async () => {
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: async () => {
        throw BridgeError.userRejected();
      },
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('metamask')).rejects.toMatchObject({ code: 'USER_REJECTED' });
    expect(ctrl.getState().activeConnectorId).toBeNull();
    ctrl.destroy();
  });

  it('throws on unknown connector', async () => {
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('nope')).rejects.toThrow(/Unknown connector/);
    ctrl.destroy();
  });

  it('disconnect clears storage and state', async () => {
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
    await ctrl.connect('metamask');
    await ctrl.disconnect();
    expect(ctrl.getState().status).toBe('disconnected');
    expect(ctrl.getState().account).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBeUndefined();
    ctrl.destroy();
  });

  it('preserves BridgeError code from wallet error event', () => {
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    lastWallet.emit('error', { code: 'USER_REJECTED', message: 'rejected' });
    const state = ctrl.getState();
    expect(state.status).toBe('error');
    expect(state.error).toBeInstanceOf(BridgeError);
    expect((state.error as BridgeError).code).toBe('USER_REJECTED');
    expect(connectErrorMessage(state.error)).toMatch(/rejected/i);
    ctrl.destroy();
  });

  it('tryAutoReconnect is no-op when disabled', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'metamask');
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      authorized: () => true,
      connect: async (ctx) => ctx.wallet.connectFromMetaMask(),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: false,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    expect(ctrl.getState().status).toBe('disconnected');
    ctrl.destroy();
  });

  it('tryAutoReconnect skips advanced last connector', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'seed');
    const seed: Connector = {
      id: 'seed',
      name: 'Seed',
      advanced: true,
      ready: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [seed],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    ctrl.destroy();
  });

  it('tryAutoReconnect skips when not ready', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'metamask');
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => false,
      authorized: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    ctrl.destroy();
  });

  it('tryAutoReconnect skips when not authorized', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'metamask');
    const connect = vi.fn(async () => mockInfo);
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      authorized: async () => false,
      connect,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    expect(connect).not.toHaveBeenCalled();
    ctrl.destroy();
  });

  it('tryAutoReconnect succeeds when authorized', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'metamask');
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      authorized: async () => true,
      connect: async (ctx) => ctx.wallet.connectFromMetaMask(),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(true);
    expect(ctrl.getState().status).toBe('connected');
    ctrl.destroy();
  });

  it('exposes connectingStep during connect', async () => {
    let resolveConnect!: (v: DeriveInfo) => void;
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: () =>
        new Promise<DeriveInfo>((resolve) => {
          resolveConnect = resolve;
        }),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    const p = ctrl.connect('metamask');
    await vi.waitFor(() => {
      expect(ctrl.getState().connectingStep).toBe('sign');
    });
    resolveConnect(mockInfo);
    await p;
    expect(ctrl.getState().connectingStep).toBeNull();
    ctrl.destroy();
  });

  it('softDisconnect clears account but keeps lastConnectorId', async () => {
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
    await ctrl.connect('metamask');
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('metamask');
    await ctrl.softDisconnect();
    expect(ctrl.getState().status).toBe('disconnected');
    expect(ctrl.getState().account).toBeNull();
    expect(ctrl.getState().activeConnectorId).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('metamask');
    ctrl.destroy();
  });

  it('tryAutoReconnect force works when autoReconnect is false', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'metamask');
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      authorized: async () => true,
      connect: async (ctx) => ctx.wallet.connectFromMetaMask(),
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: false,
    });
    expect(await ctrl.tryAutoReconnect()).toBe(false);
    expect(await ctrl.tryAutoReconnect({ force: true })).toBe(true);
    expect(ctrl.getState().status).toBe('connected');
    ctrl.destroy();
  });

  it('maps wallet lock event to locked status and keeps last connector', async () => {
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
    await ctrl.connect('metamask');
    lastWallet.emit('lock', {});
    expect(ctrl.getState().status).toBe('locked');
    expect(ctrl.getState().account).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('metamask');
    ctrl.destroy();
  });

  it('connect mutex rejects overlapping connect', async () => {
    let resolveConnect!: (v: DeriveInfo) => void;
    const connector: Connector = {
      id: 'metamask',
      name: 'MetaMask',
      ready: () => true,
      connect: () =>
        new Promise<DeriveInfo>((resolve) => {
          resolveConnect = resolve;
        }),
    };
    const seedConnector: Connector = {
      id: 'seed',
      name: 'Seed',
      ready: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector, seedConnector],
      allowUnpinnedWasm: true,
    });
    const a = ctrl.connect('metamask');
    await expect(ctrl.connect('seed')).rejects.toMatchObject({
      code: 'USER_REJECTED',
      message: expect.stringMatching(/already in progress/i),
    });
    await vi.waitFor(() => {
      expect(typeof resolveConnect).toBe('function');
    });
    resolveConnect(mockInfo);
    await a;
    ctrl.destroy();
  });
});
