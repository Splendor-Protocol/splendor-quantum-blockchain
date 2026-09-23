/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import React, { type ReactNode } from 'react';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useQRC20, useDeployToken, useConnect, useModuleCall } from '../react/hooks.js';
import { ControllerContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);
const TOKEN = 'SQ:' + 'aa'.repeat(32);

const { balanceOf, tokenInfo, transfer, approve, deployQRC20 } = vi.hoisted(() => ({
  balanceOf: vi.fn(async () => '100'),
  tokenInfo: vi.fn(async () => ({ name: 'T', symbol: 'T' })),
  transfer: vi.fn(async () => ({ hash: '0xt' })),
  approve: vi.fn(async () => ({ hash: '0xa' })),
  deployQRC20: vi.fn(async () => ({
    address: 'SQ:' + 'aa'.repeat(32),
    result: { hash: '0xd', status: 'ok' },
  })),
}));

vi.mock('@splendor/sqc-sdk/tokens', () => ({
  QRC20: class {
    balanceOf(...args: unknown[]) {
      return balanceOf(...args);
    }
    tokenInfo(...args: unknown[]) {
      return tokenInfo(...args);
    }
    transfer(...args: unknown[]) {
      return transfer(...args);
    }
    approve(...args: unknown[]) {
      return approve(...args);
    }
  },
  deployQRC20,
}));

function makeState(overrides: Partial<ConnectControllerState> = {}): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: {
      address: ADDR,
      evmAddress: undefined,
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
      explorerUrl: '',
    },
    ...overrides,
  } as ConnectControllerState;
}

function makeController(state: ConnectControllerState): ConnectController {
  const wallet = {
    connection: {},
    sendModuleCall: vi.fn(async () => ({ hash: '0xm', status: 'ok' })),
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
    getWalletEpoch: () => 0,
  } as unknown as ConnectController;
}

function wrapperFor(ctrl: ConnectController) {
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(ControllerContext.Provider, { value: ctrl }, children);
  };
}

describe('useQRC20', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('refresh loads balance and tokenInfo', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useQRC20(TOKEN, 0), {
      wrapper: wrapperFor(ctrl),
    });
    await act(async () => {
      await result.current.refresh();
    });
    await waitFor(() => expect(result.current.balance).toBe('100'));
    expect(result.current.tokenInfo).toMatchObject({ name: 'T' });
  });

  it('transfer sets status/hash success and surfaces errors', async () => {
    const ctrl = makeController(makeState());
    const wallet = ctrl.getWallet() as {
      sendModuleCall: ReturnType<typeof vi.fn>;
    };
    const { result } = renderHook(() => useQRC20(TOKEN, 0), {
      wrapper: wrapperFor(ctrl),
    });
    await act(async () => {
      const res = await result.current.transfer(ADDR, '1');
      expect(res.hash).toBeTruthy();
    });
    expect(result.current.status).toBe('success');
    expect(result.current.txStatus).toBe('success');
    expect(result.current.hash).toBeTruthy();

    wallet.sendModuleCall.mockRejectedValueOnce(new Error('insufficient'));
    transfer.mockRejectedValueOnce(new Error('insufficient'));
    await act(async () => {
      await expect(result.current.transfer(ADDR, '1')).rejects.toThrow(/insufficient/);
    });
    expect(result.current.status).toBe('error');
    expect(result.current.error?.message).toMatch(/insufficient/);
  });

  it('throws when wallet missing', async () => {
    const ctrl = makeController(makeState());
    (ctrl as { getWallet: () => null }).getWallet = () => null;
    const { result } = renderHook(() => useQRC20(TOKEN, 0), {
      wrapper: wrapperFor(ctrl),
    });
    await expect(result.current.transfer(ADDR, '1')).rejects.toThrow(/not ready/i);
  });
});

describe('useDeployToken', () => {
  it('deploy sets address/hash/success and reset clears', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useDeployToken(), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.deploy({
        name: 'My',
        symbol: 'MY',
        decimals: 18,
        totalSupply: '1000',
      });
    });
    expect(result.current.status).toBe('success');
    expect(result.current.address).toBe(TOKEN);
    expect(result.current.hash).toBe('0xd');
    act(() => result.current.reset());
    expect(result.current.status).toBe('idle');
    expect(result.current.address).toBeNull();
  });
});

describe('useConnect', () => {
  it('exposes controller connect/disconnect/modal', () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useConnect(), { wrapper: wrapperFor(ctrl) });
    expect(result.current.status).toBe('connected');
    void result.current.openModal('connect');
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });
});

describe('useModuleCall', () => {
  it('forwards sendModuleCall', async () => {
    const ctrl = makeController(makeState());
    const { result } = renderHook(() => useModuleCall(), { wrapper: wrapperFor(ctrl) });
    await act(async () => {
      await result.current.call({
        module: 'qrc20',
        action: 'transfer',
        params: { token: TOKEN, to: ADDR, amount: '1' },
      });
    });
    expect(result.current.status).toBe('success');
    expect(result.current.hash).toBe('0xm');
  });
});
