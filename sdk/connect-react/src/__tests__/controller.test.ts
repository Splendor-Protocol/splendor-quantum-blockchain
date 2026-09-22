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

/** Soft probe result after connect — tests may override. */
let nextProbeResult:
  | { ok: true; chainId?: string }
  | { ok: false; error: BridgeError } = { ok: true, chainId: 'splendor-quantum-1' };

function mockWallet() {
  let info: DeriveInfo | null = null;
  const listeners = new Map<string, Set<(p: unknown) => void>>();
  const wallet = {
    init: vi.fn(async () => undefined),
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
    connection: {
      probeNetwork: vi.fn(async () => nextProbeResult),
    },
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

/** Resumable native extension connector (no connectFields). */
function extensionConnector(overrides: Partial<Connector> = {}): Connector {
  return {
    id: 'splendor-extension',
    name: 'Splendor Extension',
    keyOrigin: 'native',
    postQuantumSecure: true,
    ready: () => true,
    connect: async () => mockInfo,
    ...overrides,
  };
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
    nextProbeResult = { ok: true, chainId: 'splendor-quantum-1' };
  });

  it('requires at least one connector', () => {
    expect(() =>
      createConnectController({ network: 'local', connectors: [] }),
    ).toThrow(/at least one connector/);
  });

  it('connects via extension connector and persists last id', async () => {
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    const info = await ctrl.connect('splendor-extension');
    expect(info.address).toMatch(/^SQ:/);
    expect(ctrl.getState().status).toBe('connected');
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('splendor-extension');
    expect(lastWallet).not.toHaveProperty('connectFromMetaMask');
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
    const connector = extensionConnector({
      ready: () => false,
    });
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('splendor-extension')).rejects.toMatchObject({ code: 'NO_PROVIDER' });
    expect(ctrl.getState().status).toBe('error');
    expect(ctrl.getState().activeConnectorId).toBeNull();
    ctrl.destroy();
  });

  it('clears activeConnectorId on connect failure', async () => {
    const connector = extensionConnector({
      connect: async () => {
        throw BridgeError.userRejected();
      },
    });
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('splendor-extension')).rejects.toMatchObject({ code: 'USER_REJECTED' });
    expect(ctrl.getState().activeConnectorId).toBeNull();
    ctrl.destroy();
  });

  it('throws on unknown connector', async () => {
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await expect(ctrl.connect('nope')).rejects.toThrow(/Unknown connector/);
    ctrl.destroy();
  });

  it('disconnect clears storage and state', async () => {
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    await ctrl.disconnect();
    expect(ctrl.getState().status).toBe('disconnected');
    expect(ctrl.getState().account).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBeUndefined();
    ctrl.destroy();
  });

  it('preserves BridgeError code from wallet error event', () => {
    const connector = extensionConnector();
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
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const connector = extensionConnector({
      authorized: () => true,
    });
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
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const connector = extensionConnector({
      ready: () => false,
      authorized: () => true,
    });
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
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const connect = vi.fn(async () => mockInfo);
    const connector = extensionConnector({
      authorized: async () => false,
      connect,
    });
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
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const connector = extensionConnector({
      authorized: async () => true,
    });
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
    const connector = extensionConnector({
      connect: () =>
        new Promise<DeriveInfo>((resolve) => {
          resolveConnect = resolve;
        }),
    });
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    const p = ctrl.connect('splendor-extension');
    await vi.waitFor(() => {
      expect(ctrl.getState().connectingStep).toBe('sign');
    });
    resolveConnect(mockInfo);
    await p;
    expect(ctrl.getState().connectingStep).toBeNull();
    ctrl.destroy();
  });

  it('softDisconnect clears account but keeps lastConnectorId', async () => {
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('splendor-extension');
    await ctrl.softDisconnect();
    expect(ctrl.getState().status).toBe('disconnected');
    expect(ctrl.getState().account).toBeNull();
    expect(ctrl.getState().activeConnectorId).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('splendor-extension');
    ctrl.destroy();
  });

  it('tryAutoReconnect force works when autoReconnect is false', async () => {
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const connector = extensionConnector({
      authorized: async () => true,
    });
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
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    lastWallet.emit('lock', {});
    expect(ctrl.getState().status).toBe('locked');
    expect(ctrl.getState().account).toBeNull();
    expect(memoryStore.get('splendor.connect.lastConnectorId')).toBe('splendor-extension');
    ctrl.destroy();
  });

  it('connect mutex rejects overlapping connect', async () => {
    let resolveConnect!: (v: DeriveInfo) => void;
    const connector = extensionConnector({
      connect: () =>
        new Promise<DeriveInfo>((resolve) => {
          resolveConnect = resolve;
        }),
    });
    const seedConnector: Connector = {
      id: 'seed',
      name: 'Seed',
      keyOrigin: 'native',
      postQuantumSecure: true,
      connectFields: [{ name: 'seed', type: 'password', label: 'ML-DSA-65 seed' }],
      ready: () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector, seedConnector],
      allowUnpinnedWasm: true,
    });
    const a = ctrl.connect('splendor-extension');
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

  it('getState snapshot is referentially stable across no-op reads', () => {
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    const a = ctrl.getState();
    const b = ctrl.getState();
    expect(a).toBe(b);
    ctrl.openModal('connect');
    const c = ctrl.getState();
    expect(c).not.toBe(a);
    expect(c.modal).toBe('connect');
    const d = ctrl.getState();
    expect(c).toBe(d);
    ctrl.destroy();
  });

  it('wires connector.onAccountsChanged when present', async () => {
    let fire: ((evm: string) => void) | null = null;
    const connector = extensionConnector({
      onAccountsChanged: (cb) => {
        fire = cb;
        return () => {
          fire = null;
        };
      },
    });
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    expect(typeof fire).toBe('function');
    fire?.('0x2222222222222222222222222222222222222222');
    ctrl.destroy();
    expect(fire).toBeNull();
  });

  it('softDisconnect mid-connect prevents stale session commit', async () => {
    let releaseReady!: () => void;
    const readyGate = new Promise<void>((r) => {
      releaseReady = r;
    });
    const c: Connector = {
      id: 'splendor-extension',
      name: 'Ext',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: async () => {
        await readyGate;
        return true;
      },
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [c],
      allowUnpinnedWasm: true,
    });
    const p = ctrl.connect('splendor-extension');
    await ctrl.softDisconnect();
    releaseReady();
    await expect(p).rejects.toMatchObject({ code: 'USER_REJECTED' });
    expect(ctrl.getState().status).toBe('disconnected');
    expect(ctrl.getState().account).toBeNull();
    ctrl.destroy();
  });

  it('softDisconnect keeps connect mutex until in-flight unlock settles', async () => {
    let releaseReady!: () => void;
    const readyGate = new Promise<void>((r) => {
      releaseReady = r;
    });
    const slow: Connector = {
      id: 'splendor-extension',
      name: 'Ext',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: async () => {
        await readyGate;
        return true;
      },
      connect: async () => mockInfo,
    };
    const other: Connector = {
      id: 'other-ext',
      name: 'Other',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: async () => true,
      connect: async () => mockInfo,
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [slow, other],
      allowUnpinnedWasm: true,
    });
    const first = ctrl.connect('splendor-extension');
    await ctrl.softDisconnect();
    await expect(ctrl.connect('other-ext')).rejects.toMatchObject({
      message: /already in progress/i,
    });
    releaseReady();
    await expect(first).rejects.toMatchObject({ code: 'USER_REJECTED' });
    await expect(ctrl.connect('other-ext')).resolves.toMatchObject({ address: mockInfo.address });
    ctrl.destroy();
  });

  it('probeNetwork fail surfaces error after connect', async () => {
    const probeErr = BridgeError.rpcUnavailable('all endpoints down');
    nextProbeResult = { ok: false, error: probeErr };
    const connector = extensionConnector();
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    expect(ctrl.getState().status).toBe('connected');
    await vi.waitFor(() => {
      expect(ctrl.getState().error).toMatchObject({ code: 'RPC_UNAVAILABLE' });
    });
    expect(ctrl.getState().status).toBe('connected');
    ctrl.destroy();
  });

  it('connector setSessionWallet is returned by getWallet', async () => {
    const setSessionSpy = vi.fn();
    const sessionWallet = {
      getDeriveInfo: () => mockInfo,
      getConnectionState: () => 'connected' as const,
      disconnect: vi.fn(),
      connection: {
        probeNetwork: vi.fn(async () => ({ ok: true as const, chainId: 'splendor-quantum-1' })),
      },
      on: vi.fn(() => () => undefined),
    };
    const connector: Connector = {
      id: 'splendor-extension',
      name: 'Splendor Extension',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: () => true,
      connect: async (ctx) => {
        setSessionSpy(ctx.setSessionWallet);
        ctx.setSessionWallet?.(sessionWallet as never);
        return mockInfo;
      },
    };
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    expect(setSessionSpy).toHaveBeenCalled();
    expect(ctrl.getWallet()).toBe(sessionWallet);
    expect(ctrl.getState().status).toBe('connected');
    ctrl.destroy();
  });
});
