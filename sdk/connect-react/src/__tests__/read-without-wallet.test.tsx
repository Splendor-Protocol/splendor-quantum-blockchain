/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { useTokenBalance, useTokenInfo, useReadContract, useQRC20 } from '../react/hooks.js';
import { createMockConnectController, mockWallet, MockSplendorProvider } from '../testing.js';

const TOKEN = 'SQ:' + 'ee'.repeat(32);
const OWNER = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

function disconnectedWrapper(moduleCall: Parameters<typeof mockWallet>[0]['moduleCall']) {
  const wallet = mockWallet({ moduleCall });
  const ctrl = createMockConnectController({ status: 'disconnected', wallet });
  const Wrap = ({ children }: { children: ReactNode }) =>
    React.createElement(MockSplendorProvider, { controller: ctrl }, children);
  return { ctrl, wallet, Wrap };
}

const tokenReads = (env: { action: string; params: Record<string, unknown> }) => {
  if (env.action === 'token_info') return { name: 'Lab', symbol: 'LAB', decimals: 6 };
  if (env.action === 'balance_of') return { balance: '2500000' };
  return {};
};

describe('reads without a connected wallet', () => {
  it('useTokenInfo loads metadata while disconnected and validates decimals', async () => {
    const { Wrap, wallet } = disconnectedWrapper(tokenReads);
    const { result } = renderHook(() => useTokenInfo(TOKEN), { wrapper: Wrap });
    await waitFor(() => expect(result.current.data?.symbol).toBe('LAB'));
    expect(result.current.data?.decimals).toBe(6);
    expect(wallet.calls.some((c) => c.method === 'connection.moduleCall')).toBe(true);
    expect(wallet.calls.some((c) => c.method === 'sendModuleCall')).toBe(false);
  });

  it('useTokenInfo reports decimals out of range as null', async () => {
    const { Wrap } = disconnectedWrapper(() => ({ symbol: 'BAD', decimals: 77 }));
    const { result } = renderHook(() => useTokenInfo(TOKEN), { wrapper: Wrap });
    await waitFor(() => expect(result.current.data?.symbol).toBe('BAD'));
    expect(result.current.data?.decimals).toBeNull();
  });

  it('useTokenBalance formats the balance once decimals are known', async () => {
    const { Wrap } = disconnectedWrapper(tokenReads);
    const { result } = renderHook(() => useTokenBalance(TOKEN, OWNER), { wrapper: Wrap });
    await waitFor(() => expect(result.current.formatted).toBe('2.5'));
    expect(result.current.data).toBe('2500000');
  });

  it('useQRC20 shows token info to a visitor who has not connected', async () => {
    const { Wrap } = disconnectedWrapper(tokenReads);
    const { result } = renderHook(() => useQRC20(TOKEN, 0), { wrapper: Wrap });
    await waitFor(() => expect(result.current.decimals).toBe(6));
    expect(result.current.balance).toBeNull();
  });

  it('useReadContract calls the node without signing and skips when disabled', async () => {
    const wallet = mockWallet();
    const ctrl = createMockConnectController({ status: 'disconnected', wallet });
    const Wrap = ({ children }: { children: ReactNode }) =>
      React.createElement(MockSplendorProvider, { controller: ctrl }, children);
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useReadContract(TOKEN, 'get()', [], { enabled }),
      { wrapper: Wrap, initialProps: { enabled: false } },
    );
    expect(result.current.data).toBeNull();
    expect(wallet.calls.filter((c) => c.method === 'connection.call')).toHaveLength(0);
    rerender({ enabled: true });
    await waitFor(() => expect(wallet.calls.some((c) => c.method === 'connection.call')).toBe(true));
  });

  it('drops a late result after the token address changes', async () => {
    let release: (v: unknown) => void = () => undefined;
    const slow = new Promise((r) => {
      release = r;
    });
    const { Wrap } = disconnectedWrapper((env) =>
      env.params.token === TOKEN ? slow.then(() => ({ symbol: 'OLD', decimals: 1 })) : { symbol: 'NEW', decimals: 2 },
    );
    const { result, rerender } = renderHook(({ t }: { t: string }) => useTokenInfo(t), {
      wrapper: Wrap,
      initialProps: { t: TOKEN },
    });
    rerender({ t: OWNER });
    await waitFor(() => expect(result.current.data?.symbol).toBe('NEW'));
    release(undefined);
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current.data?.symbol).toBe('NEW');
  });
});
