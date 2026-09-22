/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import React, { type ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { useBridge, useModules, useQRC1155, useContract, usePayX402 } from '../react/hooks.js';
import { ControllerContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);
const BRIDGE = 'SQ:' + 'dd'.repeat(32);
const COLLECTION = 'SQ:' + 'ee'.repeat(32);
const CONTRACT = 'SQ:' + '11'.repeat(32);

const {
  bridgeClaim,
  bridgeInfo,
  q1155Balance,
  contractRead,
  payX402,
} = vi.hoisted(() => ({
  bridgeClaim: vi.fn(async () => ({ hash: '0xbr' })),
  bridgeInfo: vi.fn(async () => ({ paused: false })),
  q1155Balance: vi.fn(async () => '7'),
  contractRead: vi.fn(async () => ({ decoded: true })),
  payX402: vi.fn(async () => ({ hash: '0x402' })),
}));

vi.mock('@splendor/sqc-sdk/tokens', () => ({
  QRC1155: class {
    balanceOf(...args: unknown[]) {
      return q1155Balance(...args);
    }
  },
}));

vi.mock('@splendor/sqc-sdk/contracts', () => ({
  Contract: class {
    read(...args: unknown[]) {
      return contractRead(...args);
    }
  },
}));

vi.mock('@splendor/sqc-sdk/bridge', () => ({
  BridgeModule: class {
    claim(...args: unknown[]) {
      return bridgeClaim(...args);
    }
    info(...args: unknown[]) {
      return bridgeInfo(...args);
    }
    claimed = vi.fn(async () => ({ claimed: false }));
  },
  deployBridge: vi.fn(),
}));

function makeState(overrides: Partial<ConnectControllerState> = {}): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: {
      address: ADDR,
      connectorId: 'seed',
      keyOrigin: 'native',
      postQuantumSecure: true,
    },
    info: {
      address: ADDR,
      public_key_b64: 'x',
      public_key_hex: '00',
      postQuantumSecure: true,
      keyOrigin: 'native',
    },
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
      explorerUrl: 'https://explorer.example',
    },
    ...overrides,
  } as ConnectControllerState;
}

function makeController(state: ConnectControllerState): ConnectController {
  const wallet = {
    connection: {},
    sendModuleCall: vi.fn(async () => ({ hash: '0xm', status: 'ok' })),
    payX402: (...args: unknown[]) => payX402(...args),
    callContract: vi.fn(async () => ({ hash: '0xc', status: 'ok' })),
  };
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
  } as unknown as ConnectController;
}

function wrapperFor(ctrl: ConnectController) {
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(ControllerContext.Provider, { value: ctrl }, children);
  };
}

describe('useBridge', () => {
  beforeEach(() => vi.clearAllMocks());

  it('claim sets status/hash', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useBridge(BRIDGE), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.claim({
        id: 1,
        asset: 'spld',
        amount: '10',
        recipient: ADDR,
        evmTx: '0xabc',
        attestations: [],
      });
    });
    expect(bridgeClaim).toHaveBeenCalled();
    expect(result.current.status).toBe('success');
    expect(result.current.hash).toBe('0xbr');
  });
});

describe('useModules', () => {
  it('returns bound factories when connected (no public dex/exchange)', () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useModules(), { wrapper: wrapperFor(ctrl) });
    expect(result.current).not.toBeNull();
    expect(result.current!.qrc20(ADDR).getAddress()).toBe(ADDR);
    expect(result.current!.bridge(ADDR).getAddress()).toBe(ADDR);
    expect(result.current!).not.toHaveProperty('dex');
    expect(result.current!).not.toHaveProperty('exchange');
  });

  it('returns null when wallet missing', () => {
    const ctrl = makeController(makeState());
    (ctrl as { getWallet: () => null }).getWallet = () => null;
    const { result } = renderHook(() => useModules(), { wrapper: wrapperFor(ctrl) });
    expect(result.current).toBeNull();
  });
});

describe('useQRC1155', () => {
  beforeEach(() => vi.clearAllMocks());

  it('balanceOf reads collection', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useQRC1155(COLLECTION), { wrapper: wrapperFor(ctrl) });
    let bal: string | undefined;
    await act(async () => {
      bal = await result.current.balanceOf(ADDR, '1');
    });
    expect(bal).toBe('7');
    expect(q1155Balance).toHaveBeenCalled();
  });
});

describe('useContract', () => {
  beforeEach(() => vi.clearAllMocks());

  it('read calls Contract.read', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useContract(CONTRACT), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.read('totalSupply()');
    });
    expect(contractRead).toHaveBeenCalled();
  });
});

describe('usePayX402', () => {
  beforeEach(() => vi.clearAllMocks());

  it('pay delegates to wallet.payX402', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => usePayX402(), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.pay({ to: ADDR, amountSPLD: '0.01' });
    });
    expect(payX402).toHaveBeenCalled();
    expect(result.current.status).toBe('success');
  });
});
