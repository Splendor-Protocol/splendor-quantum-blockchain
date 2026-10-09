/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { useBalance, useSendTransfer } from '../react/hooks.js';
import { invalidateSharedReads } from '../core/readCache.js';
import { createMockConnectController, MockSplendorProvider } from '../testing.js';

const TO = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

function setup() {
  const ctrl = createMockConnectController({ status: 'connected', balance: '10' });
  const provider = ctrl.getProvider() as unknown as { getBalance: (a: string) => Promise<unknown> };
  const getBalance = vi.spyOn(provider, 'getBalance');
  const Wrap = ({ children }: { children: ReactNode }) =>
    React.createElement(MockSplendorProvider, { controller: ctrl }, children);
  return { provider, getBalance, Wrap };
}

describe('balance invalidation', () => {
  it('useBalance refetches when its reads are invalidated', async () => {
    const { provider, getBalance, Wrap } = setup();
    const { result } = renderHook(() => useBalance(60_000), { wrapper: Wrap });
    await waitFor(() => expect(result.current.balanceSPLD).toBe('10'));
    const before = getBalance.mock.calls.length;
    act(() => invalidateSharedReads(provider, 'balance|'));
    await waitFor(() => expect(getBalance.mock.calls.length).toBeGreaterThan(before));
  });

  it('a recorded send refreshes the balance without waiting for the next poll', async () => {
    const { getBalance, Wrap } = setup();
    const { result } = renderHook(() => ({ bal: useBalance(60_000), send: useSendTransfer() }), { wrapper: Wrap });
    await waitFor(() => expect(result.current.bal.balanceSPLD).toBe('10'));
    const before = getBalance.mock.calls.length;
    await act(async () => {
      await result.current.send.sendTransfer({ to: TO, amountSPLD: '1' });
    });
    await waitFor(() => expect(getBalance.mock.calls.length).toBeGreaterThan(before));
  });
});
