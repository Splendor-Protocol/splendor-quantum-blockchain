/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { useBalance, useBridge, useContract, useQRC1155, useQRC20 } from '../react/hooks.js';
import { __resetPendingTxCache } from '../core/pendingTx.js';
import { createMockConnectController, mockWallet, MockSplendorProvider } from '../testing.js';

const TOKEN = 'SQ:' + 'aa'.repeat(32);
const TO = 'SQ:' + 'cd'.repeat(32);

function setup(walletOpts: Parameters<typeof mockWallet>[0] = {}, balance?: () => string) {
  const wallet = mockWallet(walletOpts);
  const ctrl = createMockConnectController({ network: 'testnet', wallet, ...(balance ? { balance } : {}) });
  const wrapper = ({ children }: { children: ReactNode }) => React.createElement(MockSplendorProvider, { controller: ctrl }, children);
  return { ctrl, wrapper };
}

beforeEach(() => {
  localStorage.clear();
  __resetPendingTxCache();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('transaction hooks report UI copy in errorMessage', () => {
  const failing = () => setup({ fail: { sendModuleCall: new Error('node said: 0xdeadbeef stack trace'), callContract: new Error('raw rpc body') } });

  it('useQRC20, useQRC1155, useBridge and useContract give transaction copy, not the raw error', async () => {
    const { wrapper } = failing();
    const { result } = renderHook(
      () => ({ token: useQRC20(TOKEN, 0), nft: useQRC1155(TOKEN), bridge: useBridge(TOKEN), contract: useContract(TOKEN) }),
      { wrapper },
    );
    expect(result.current.token.errorMessage).toBeNull();
    await act(async () => {
      await result.current.token.transfer(TO, '1').catch(() => undefined);
      await result.current.nft.mint(TO, '1', '1').catch(() => undefined);
      await result.current.bridge
        .claim({ id: 1, asset: 'spld', amount: '1', recipient: TO, evmTx: '0x' + '11'.repeat(32), attestations: [] })
        .catch(() => undefined);
      await result.current.contract.call('set(uint256)', ['1']).catch(() => undefined);
    });
    const hooks = { token: result.current.token, nft: result.current.nft, bridge: result.current.bridge, contract: result.current.contract };
    const seen = Object.fromEntries(Object.entries(hooks).map(([k, h]) => [k, { status: h.status, errorMessage: h.errorMessage }]));
    for (const h of Object.values(seen)) {
      expect(h.status).toBe('error');
      expect(h.errorMessage).not.toMatch(/deadbeef|raw rpc body|Connection failed/);
    }
    expect(seen.token!.errorMessage).toBe('Transaction failed — try again');
    expect(seen.nft!.errorMessage).toBe('Transaction failed — try again');
  });

  it('calling a write before the wallet is ready throws NOT_CONNECTED, or INVALID_PARAMS without an address', async () => {
    const wallet = mockWallet();
    const ctrl = createMockConnectController({ network: 'testnet', wallet, status: 'disconnected' });
    const wrapper = ({ children }: { children: ReactNode }) => React.createElement(MockSplendorProvider, { controller: ctrl }, children);
    const { result } = renderHook(() => ({ withToken: useQRC20(TOKEN, 0) }), { wrapper });
    await expect(result.current.withToken.transfer(TO, '1')).rejects.toMatchObject({ code: 'NOT_CONNECTED', message: 'Wallet or token not ready' });

    const connected = setup();
    const { result: noToken } = renderHook(() => useQRC20(null, 0), { wrapper: connected.wrapper });
    await expect(noToken.current.transfer(TO, '1')).rejects.toMatchObject({ code: 'INVALID_PARAMS', userHint: 'Set the token address first' });
  });
});

describe('polling pauses in hidden tabs', () => {
  let hidden = false;
  beforeEach(() => {
    hidden = false;
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') });
  });
  afterEach(() => {
    delete (document as unknown as { visibilityState?: string }).visibilityState;
  });
  const setHidden = (value: boolean) => {
    hidden = value;
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
  };

  it('useBalance stops reading while hidden and reads once when the tab is back', async () => {
    vi.useFakeTimers();
    let reads = 0;
    const { wrapper } = setup({}, () => {
      reads += 1;
      return '1';
    });
    renderHook(() => useBalance(1_000), { wrapper });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(reads).toBe(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(reads).toBe(4);

    setHidden(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(reads).toBe(4);

    setHidden(false);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10);
    });
    expect(reads).toBe(5);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(reads).toBe(6);
  });
});
