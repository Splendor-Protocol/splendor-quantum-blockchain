/**
 * Core connect-react suite: rebindNetwork, wallet epoch, cross-tab broadcast,
 * useWallet readiness, TokenPanel / amount guards, BUSY copy.
 *
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import React, { type ReactNode } from 'react';
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react';
import { renderHook, act } from '@testing-library/react';
import { createConnectController, connectErrorMessage } from '../core/controller.js';
import {
  broadcastConnectEvent,
  subscribeConnectBroadcast,
  setLastConnectorId,
} from '../core/storage.js';
import type { Connector } from '../connectors/types.js';
import type { DeriveInfo } from '@splendor/sqc-sdk';
import { BridgeError } from '@splendor/sqc-sdk';
import { ControllerContext, NetworkSwitchContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';
import { useWallet, useAccount, useConnect } from '../react/hooks/core.js';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { DeployTokenWidget } from '../widgets/DeployTokenWidget.js';
import { extensionConnector, seedConnector, assertSeedHex, generateSeedHex } from '../connectors/index.js';
import { groupConnectors } from '../ui/connectorGroups.js';

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
  keyOrigin: 'native',
  postQuantumSecure: true,
};
const RECIPIENT = 'SQ:' + 'cd'.repeat(32);

let nextProbeResult:
  | { ok: true; chainId?: string }
  | { ok: false; error: BridgeError } = { ok: true, chainId: 'splendor-quantum-1' };

type MockWallet = ReturnType<typeof mockWallet>;

function mockWallet() {
  let info: DeriveInfo | null = null;
  const listeners = new Map<string, Set<(p: unknown) => void>>();
  const rebindConnection = vi.fn((_next: unknown, _meta?: unknown) => undefined);
  const wallet = {
    init: vi.fn(async () => undefined),
    connectFromSeed: vi.fn(async (seed: string) => {
      if (!seed) throw new Error('no seed');
      info = { ...mockInfo };
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
    rebindConnection,
    provider: {},
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

function extensionConn(overrides: Partial<Connector> = {}): Connector {
  return {
    id: 'splendor-extension',
    name: 'Splendor Quantum Wallet',
    keyOrigin: 'native',
    postQuantumSecure: true,
    ready: () => true,
    authorized: () => true,
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

vi.mock('@splendor/sqc-sdk/tokens', () => ({
  QRC20: class {
    balanceOf = vi.fn(async () => '0');
    tokenInfo = vi.fn(async () => ({ name: 'T', symbol: 'T', decimals: 18 }));
    transfer = vi.fn(async () => ({ hash: '0xt' }));
  },
  deployQRC20: vi.fn(),
}));

describe('controller rebindNetwork + epoch', () => {
  beforeEach(() => {
    memoryStore.clear();
    nextProbeResult = { ok: true, chainId: 'splendor-quantum-1' };
  });

  it('rebindNetwork keeps session and calls rebindConnection', async () => {
    const ctrl = createConnectController({
      network: 'local',
      connectors: [extensionConn()],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    const walletBefore = ctrl.getWallet();
    const epochBefore = ctrl.getWalletEpoch();
    await ctrl.rebindNetwork('testnet');
    expect(ctrl.getWallet()).toBe(walletBefore);
    expect(ctrl.getState().status).toBe('connected');
    expect(lastWallet.rebindConnection).toHaveBeenCalled();
    expect(ctrl.getNetwork()?.id).toBe('testnet');
    expect(ctrl.getWalletEpoch()).toBe(epochBefore);
    ctrl.destroy();
  });

  it('rebindNetwork rejects unknown network', async () => {
    const ctrl = createConnectController({
      network: 'local',
      connectors: [extensionConn()],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    await expect(ctrl.rebindNetwork('not-a-real-net' as never)).rejects.toMatchObject({
      code: 'RPC',
    });
    ctrl.destroy();
  });

  it('getWalletEpoch increments on setSessionWallet', async () => {
    const ctrl = createConnectController({
      network: 'local',
      connectors: [
        extensionConn({
          connect: async (ctx) => {
            const ext = mockWallet();
            await ext.connectFromSeed('01'.repeat(32));
            ctx.setSessionWallet?.(ext as never);
            return mockInfo;
          },
        }),
      ],
      allowUnpinnedWasm: true,
    });
    const epoch0 = ctrl.getWalletEpoch();
    await ctrl.connect('splendor-extension');
    expect(ctrl.getWalletEpoch()).toBeGreaterThan(epoch0);
    ctrl.destroy();
  });

  it('tracks the wallet chain id from getChainId and onChainChanged', async () => {
    let fireChain: ((id: string) => void) | null = null;
    const ctrl = createConnectController({
      network: 'local',
      connectors: [
        extensionConn({
          getChainId: async () => 'splendor-quantum-testnet-1',
          onChainChanged: (cb) => {
            fireChain = cb;
            return () => {
              fireChain = null;
            };
          },
        }),
      ],
      allowUnpinnedWasm: true,
    });
    expect(ctrl.getState().walletChainId).toBeNull();
    await ctrl.connect('splendor-extension');
    await vi.waitFor(() => {
      expect(ctrl.getState().walletChainId).toBe('splendor-quantum-testnet-1');
    });
    fireChain!('splendor-quantum-1');
    expect(ctrl.getState().walletChainId).toBe('splendor-quantum-1');
    await ctrl.rebindNetwork('testnet');
    expect(ctrl.getState().walletChainId).toBe('splendor-quantum-1');
    await ctrl.disconnect();
    expect(ctrl.getState().walletChainId).toBeNull();
    expect(fireChain).toBeNull();
    ctrl.destroy();
  });

  it('leaves walletChainId null for connectors that follow the app network', async () => {
    const ctrl = createConnectController({
      network: 'local',
      connectors: [extensionConn()],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    expect(ctrl.getState().walletChainId).toBeNull();
    ctrl.destroy();
  });

  it('onAccountsChanged updates account address while connected', async () => {
    let fire: ((addr: string) => void) | null = null;
    const ctrl = createConnectController({
      network: 'local',
      connectors: [
        extensionConn({
          onAccountsChanged: (cb) => {
            fire = cb;
            return () => {
              fire = null;
            };
          },
        }),
      ],
      allowUnpinnedWasm: true,
    });
    await ctrl.connect('splendor-extension');
    const next = 'SQ:' + 'cd'.repeat(32);
    // syncFromWallet reads session wallet derive info — keep mock in sync.
    let derive: DeriveInfo | null = { ...mockInfo };
    lastWallet.getDeriveInfo = () => derive;
    lastWallet.getConnectionState = () => (derive ? 'connected' : 'disconnected');
    derive = { ...mockInfo, address: next };
    fire?.(next);
    await vi.waitFor(() => {
      expect(ctrl.getState().account?.address).toBe(next);
    });
    expect(ctrl.getState().status).toBe('connected');
    ctrl.destroy();
  });
});

describe('cross-tab broadcast', () => {
  beforeEach(() => {
    memoryStore.clear();
  });

  it('broadcastConnectEvent delivers to subscribers', async () => {
    if (typeof BroadcastChannel === 'undefined') return;
    const seen: unknown[] = [];
    const off = subscribeConnectBroadcast((msg) => seen.push(msg));
    broadcastConnectEvent({
      type: 'connected',
      connectorId: 'splendor-extension',
      address: mockInfo.address,
    });
    await vi.waitFor(() => {
      expect(seen).toEqual([
        {
          type: 'connected',
          connectorId: 'splendor-extension',
          address: mockInfo.address,
        },
      ]);
    });
    off();
  });

  it('disconnect broadcast clears a connected peer controller', async () => {
    if (typeof BroadcastChannel === 'undefined') return;
    const a = createConnectController({
      network: 'local',
      connectors: [extensionConn()],
      allowUnpinnedWasm: true,
    });
    const b = createConnectController({
      network: 'local',
      connectors: [extensionConn()],
      allowUnpinnedWasm: true,
    });
    await a.connect('splendor-extension');
    await b.connect('splendor-extension');
    expect(b.getState().status).toBe('connected');
    await a.disconnect();
    await vi.waitFor(() => {
      expect(b.getState().status).toBe('disconnected');
    });
    a.destroy();
    b.destroy();
  });

  it('connected broadcast triggers tryAutoReconnect on disconnected peer', async () => {
    if (typeof BroadcastChannel === 'undefined') return;
    setLastConnectorId('splendor-extension');
    const peer = createConnectController({
      network: 'local',
      connectors: [extensionConn({ authorized: () => true })],
      allowUnpinnedWasm: true,
      autoReconnect: false,
    });
    expect(peer.getState().status).toBe('disconnected');
    broadcastConnectEvent({
      type: 'connected',
      connectorId: 'splendor-extension',
      address: mockInfo.address,
    });
    await vi.waitFor(() => {
      expect(peer.getState().status).toBe('connected');
    });
    peer.destroy();
  });
});

describe('connectErrorMessage BUSY', () => {
  it('maps BUSY to in-progress copy', () => {
    expect(connectErrorMessage(BridgeError.busy())).toMatch(/progress|busy|wait/i);
  });
});

describe('connectors + groups', () => {
  it('extensionConnector is native PQ with authorized hook', async () => {
    const c = extensionConnector();
    expect(c.id).toBe('splendor-extension');
    expect(c.keyOrigin).toBe('native');
    expect(c.postQuantumSecure).toBe(true);
    expect(c.name).toMatch(/Splendor/i);
  });

  it('seedConnector requires typed secret fields', () => {
    const c = seedConnector();
    expect(c.id).toBe('seed');
    expect(c.connectFields?.some((f) => f.name === 'seed')).toBe(true);
    expect(c.name).toMatch(/seed/i);
  });

  it.each([
    ['01'.repeat(32), true],
    ['0x' + 'ab'.repeat(32), true],
    ['zz'.repeat(32), false],
    ['01'.repeat(31), false],
    ['', false],
  ] as const)('assertSeedHex(%s)', (raw, ok) => {
    if (ok) expect(assertSeedHex(raw)).toHaveLength(64);
    else expect(() => assertSeedHex(raw)).toThrow();
  });

  it('generateSeedHex returns 64 hex chars', () => {
    const s = generateSeedHex();
    expect(s).toMatch(/^[0-9a-f]{64}$/);
  });

  it('groupConnectors only surfaces native PQ', () => {
    const groups = groupConnectors([
      extensionConnector(),
      seedConnector(),
      {
        id: 'metamask',
        name: 'MetaMask',
        keyOrigin: 'evm-derived',
        postQuantumSecure: false,
        ready: () => true,
        connect: async () => mockInfo,
      },
    ]);
    expect(groups.native.map((c) => c.id).sort()).toEqual(['seed', 'splendor-extension'].sort());
    expect(groups.legacy).toEqual([]);
    expect(groups.advanced).toEqual([]);
  });
});

function makeState(overrides: Partial<ConnectControllerState> = {}): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: {
      address: mockInfo.address,
      connectorId: 'seed',
      keyOrigin: 'native',
      postQuantumSecure: true,
    },
    info: mockInfo,
    error: null,
    modal: 'closed',
    connectors: [],
    activeConnectorId: 'seed',
    network: {
      id: 'local',
      name: 'Local',
      networkId: 'splendor-local',
      chainId: 'splendor-quantum-1',
      rpcUrls: ['http://127.0.0.1:8080'],
      gatewayUrl: '',
      gatewayPort: 3090,
      wasmUrl: '',
      execUrl: '',
      wasmSha256: '',
      execSha256: '',
      timeoutMs: 20000,
      retries: 0,
      explorerUrl: '',
    },
    ...overrides,
  } as ConnectControllerState;
}

function makeHookController(state: ConnectControllerState, wallet: unknown = {}) {
  let epoch = 0;
  return {
    getState: () => state,
    subscribe: () => () => undefined,
    connect: vi.fn(async () => state.info!),
    disconnect: vi.fn(async () => undefined),
    clearError: vi.fn(),
    openModal: vi.fn(),
    closeModal: vi.fn(),
    getWallet: () => wallet,
    getProvider: () => ({}),
    getWalletEpoch: () => epoch,
    bumpEpoch: () => {
      epoch += 1;
    },
    isStub: false,
  } as unknown as ConnectController & { bumpEpoch: () => void };
}

function wrapperFor(ctrl: ConnectController) {
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(ControllerContext.Provider, { value: ctrl }, children);
  };
}

function widgetWrap(ctrl: ConnectController) {
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(
      ControllerContext.Provider,
      { value: ctrl },
      React.createElement(
        NetworkSwitchContext.Provider,
        {
          value: {
            switchNetwork: vi.fn(async () => undefined) as (id: never) => Promise<void>,
            allowPlaceholderNetworks: true,
            networkId: 'local' as never,
            switching: false,
          },
        },
        children,
      ),
    );
  };
}

describe('useWallet readiness', () => {
  it.each([
    ['disconnected', false],
    ['connecting', false],
    ['error', false],
    ['connected', true],
    ['locked', true],
  ] as const)('status %s → ready=%s', (status, ready) => {
    const state = makeState({ status });
    const ctrl = makeHookController(state);
    const { result } = renderHook(() => useWallet(), { wrapper: wrapperFor(ctrl) });
    expect(result.current.ready).toBe(ready);
    if (ready) expect(result.current.wallet).toBeTruthy();
    else expect(result.current.wallet).toBeNull();
  });

  it('useAccount mirrors address when connected', () => {
    const ctrl = makeHookController(makeState());
    const { result } = renderHook(() => useAccount(), { wrapper: wrapperFor(ctrl) });
    expect(result.current.isConnected).toBe(true);
    expect(result.current.address).toBe(mockInfo.address);
  });
});

describe('widgets core', () => {
  afterEach(() => {
    cleanup();
  });

  it('TokenPanel shows address input when tokenAddress omitted', () => {
    const ctrl = makeHookController(makeState());
    render(React.createElement(TokenPanel), { wrapper: widgetWrap(ctrl) });
    expect(screen.getByLabelText(/Token/i)).toBeTruthy();
    expect(screen.getByText(/Enter a QRC-20/i)).toBeTruthy();
  });

  it.each(['abc', '01', '1.', '.5', ''] as const)(
    'TransferWidget disables send for invalid amount %s',
    (amount) => {
      const ctrl = makeHookController(makeState());
      const { container } = render(React.createElement(TransferWidget), {
        wrapper: widgetWrap(ctrl),
      });
      const root = within(container);
      fireEvent.change(root.getByLabelText(/Amount \(SPLD\)/i), { target: { value: amount } });
      fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: RECIPIENT } });
      expect((root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled).toBe(
        true,
      );
    },
  );

  it('TransferWidget enables send for valid amount', () => {
    const ctrl = makeHookController(makeState());
    const { container } = render(React.createElement(TransferWidget), {
      wrapper: widgetWrap(ctrl),
    });
    const root = within(container);
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: RECIPIENT } });
    fireEvent.change(root.getByLabelText(/Amount \(SPLD\)/i), { target: { value: '0.01' } });
    expect((root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled).toBe(
      false,
    );
  });

  it('DeployTokenWidget shows Connect when disconnected', () => {
    const ctrl = makeHookController(makeState({ status: 'disconnected', account: null }));
    render(React.createElement(DeployTokenWidget), { wrapper: widgetWrap(ctrl) });
    expect(screen.getByRole('button', { name: /^Connect$/i })).toBeTruthy();
  });

  it('TransferWidget shows Unlock when locked', () => {
    const ctrl = makeHookController(makeState({ status: 'locked' }));
    render(React.createElement(TransferWidget), { wrapper: widgetWrap(ctrl) });
    expect(screen.getByRole('button', { name: /^Unlock$/i })).toBeTruthy();
  });

  it('TokenPanel Connect CTA when disconnected', () => {
    const ctrl = makeHookController(makeState({ status: 'disconnected', account: null }));
    render(React.createElement(TokenPanel), { wrapper: widgetWrap(ctrl) });
    expect(screen.getByRole('button', { name: /^Connect$/i })).toBeTruthy();
  });

  it.each(['1.5', '10', '0.01'] as const)(
    'TokenPanel accepts decimal amount shape %s when address filled',
    (amount) => {
      const ctrl = makeHookController(makeState());
      const { container } = render(
        React.createElement(TokenPanel, { tokenAddress: mockInfo.address }),
        { wrapper: widgetWrap(ctrl) },
      );
      const root = within(container);
      fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: RECIPIENT } });
      fireEvent.change(root.getByLabelText(/Amount \(human/i), { target: { value: amount } });
      expect(
        (root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled,
      ).toBe(false);
    },
  );

  it.each(['abc', '01', '1.', '0', '0.0', '0.000'] as const)('TokenPanel rejects bad amount %s', (amount) => {
    const ctrl = makeHookController(makeState());
    const { container } = render(
      React.createElement(TokenPanel, { tokenAddress: mockInfo.address }),
      { wrapper: widgetWrap(ctrl) },
    );
    const root = within(container);
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: RECIPIENT } });
    fireEvent.change(root.getByLabelText(/Amount \(human/i), { target: { value: amount } });
    expect(
      (root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });
});

describe('useConnect surface', () => {
  it('exposes openModal and status', () => {
    const ctrl = makeHookController(makeState({ status: 'disconnected', account: null }));
    const { result } = renderHook(() => useConnect(), { wrapper: wrapperFor(ctrl) });
    expect(result.current.status).toBe('disconnected');
    act(() => {
      result.current.openModal('connect');
    });
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });
});
