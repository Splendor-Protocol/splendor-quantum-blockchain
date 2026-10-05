/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { useBalance } from '../react/hooks.js';
import { useTransferForm } from '../widgets/parts/useTransferForm.js';
import { createMockConnectController, MockSplendorProvider } from '../testing.js';

const A = 'SQ:' + 'aa'.repeat(32);
const B = 'SQ:' + 'bb'.repeat(32);
const TO = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe('stale result guards', () => {
  it('useBalance drops a late response for the previous account', async () => {
    const ctrl = createMockConnectController({ account: { address: A } });
    const pending: Record<string, ReturnType<typeof deferred<{ balance: string; balance_spld: string }>>> = {
      [A]: deferred(),
      [B]: deferred(),
    };
    ctrl.getProvider = (() => ({ getBalance: (addr: string) => pending[addr]!.promise })) as never;
    const wrapper = ({ children }: { children: ReactNode }) =>
      React.createElement(MockSplendorProvider, { controller: ctrl }, children);
    const { result } = renderHook(() => useBalance(0), { wrapper });

    act(() => {
      ctrl.setState({ account: { ...ctrl.getState().account!, address: B } });
    });
    await act(async () => {
      pending[B]!.resolve({ balance: '2', balance_spld: '2' });
    });
    await waitFor(() => expect(result.current.balanceSPLD).toBe('2'));
    await act(async () => {
      pending[A]!.resolve({ balance: '1', balance_spld: '1' });
    });
    expect(result.current.balanceSPLD).toBe('2');
  });

  it('useTransferForm closes the review when the account or chain changes', () => {
    const { result, rerender } = renderHook(
      (p: { self: string; contextKey: string }) =>
        useTransferForm({ balance: '10', self: p.self, contextKey: p.contextKey, defaultTo: TO, defaultAmount: '1' }),
      { initialProps: { self: A, contextKey: 'chain-1' } },
    );
    act(() => result.current.startReview());
    expect(result.current.reviewing).toBe(true);
    rerender({ self: A, contextKey: 'chain-2' });
    expect(result.current.reviewing).toBe(false);

    act(() => result.current.startReview());
    expect(result.current.reviewing).toBe(true);
    rerender({ self: B, contextKey: 'chain-2' });
    expect(result.current.reviewing).toBe(false);
  });
});
