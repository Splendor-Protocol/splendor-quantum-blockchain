/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import React, { type ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { useDex, useBridge, useModules, useQRC1155, useExchange, useContract, usePayX402 } from '../react/hooks.js';
import { ControllerContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);
const POOL = 'SQ:' + 'cc'.repeat(32);
const BRIDGE = 'SQ:' + 'dd'.repeat(32);
const TOKEN = 'SQ:' + 'aa'.repeat(32);
const COLLECTION = 'SQ:' + 'ee'.repeat(32);
const EXCHANGE = 'SQ:' + 'ff'.repeat(32);
const CONTRACT = 'SQ:' + '11'.repeat(32);

const {
  dexSwap,
  dexReserves,
  bridgeClaim,
  bridgeInfo,
  q1155Balance,
  exchangeInfo,
  contractRead,
  payX402,
} = vi.hoisted(() => ({
  dexSwap: vi.fn(async () => ({ hash: '0xdex' })),
  dexReserves: vi.fn(async () => ({ reserve0: '1', reserve1: '2' })),
  bridgeClaim: vi.fn(async () => ({ hash: '0xbr' })),
  bridgeInfo: vi.fn(async () => ({ paused: false })),
  q1155Balance: vi.fn(async () => '7'),
  exchangeInfo: vi.fn(async () => ({ name: 'ex' })),
  contractRead: vi.fn(async () => ({ decoded: true })),
  payX402: vi.fn(async () => ({ hash: '0x402' })),
}));

vi.mock('@splendor/sqc-sdk/defi', () => ({
  DEX: class {
    swap(...args: unknown[]) {
      return dexSwap(...args);
    }
    reserves(...args: unknown[]) {
      return dexReserves(...args);
    }
    addLiquidity = vi.fn();
  },
  Exchange: class {
    info(...args: unknown[]) {
      return exchangeInfo(...args);
    }
    redeem = vi.fn(async () => ({ hash: '0xrd' }));
    resolve = vi.fn(async () => ({ hash: '0xrs' }));
  },
  deployPool: vi.fn(),
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

describe('useDex', () => {
  beforeEach(() => vi.clearAllMocks());

  it('swap sets status/hash success', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useDex(POOL), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.swap({ tokenIn: 0, amountIn: '100', minAmountOut: '1' });
    });
    expect(dexSwap).toHaveBeenCalled();
    expect(result.current.status).toBe('success');
    expect(result.current.hash).toBe('0xdex');
  });

  it('reserves reads pool state', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useDex(POOL), { wrapper: wrapperFor(ctrl) });
    let reserves: Record<string, unknown> | undefined;
    await act(async () => {
      reserves = await result.current.reserves();
    });
    expect(reserves).toMatchObject({ reserve0: '1' });
  });
});

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
  it('returns bound factories when connected', () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useModules(), { wrapper: wrapperFor(ctrl) });
    expect(result.current).not.toBeNull();
    expect(result.current!.dex(POOL).getAddress()).toBe(POOL);
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

describe('useExchange', () => {
  beforeEach(() => vi.clearAllMocks());

  it('info reads exchange_info', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useExchange(EXCHANGE), { wrapper: wrapperFor(ctrl) });
    let info: Record<string, unknown> | undefined;
    await act(async () => {
      info = await result.current.info();
    });
    expect(info).toMatchObject({ name: 'ex' });
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
