/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { useAccountDto, useWaitForConfirmation } from '../react/hooks/core.js';
import { useAddressBook } from '../react/hooks/addressBook.js';
import { __resetAddressBook } from '../core/addressBook.js';
import { MOCK_ADDRESS, MockSplendorProvider, createMockConnectController, type MockConnectController } from '../testing.js';

const OTHER = 'SQ:' + 'cd'.repeat(32);

function wrapper(ctrl: MockConnectController) {
  return ({ children }: { children: ReactNode }) => <MockSplendorProvider controller={ctrl}>{children}</MockSplendorProvider>;
}

beforeEach(() => {
  localStorage.clear();
  __resetAddressBook();
});
afterEach(() => cleanup());

describe('useWaitForConfirmation', () => {
  function withProvider(waitForConfirmation: (opts: unknown) => Promise<unknown>, status: 'connected' | 'disconnected' = 'connected') {
    const ctrl = createMockConnectController({ status });
    ctrl.getProvider = (() => ({ waitForConfirmation })) as never;
    return ctrl;
  }

  it('goes idle → pending → success and passes the options through', async () => {
    let release!: () => void;
    const wait = vi.fn(() => new Promise<void>((r) => (release = r)));
    const { result } = renderHook(() => useWaitForConfirmation(), { wrapper: wrapper(withProvider(wait)) });
    expect(result.current.status).toBe('idle');
    let done!: Promise<void>;
    act(() => {
      done = result.current.wait({ from: MOCK_ADDRESS, nonceBefore: 3, timeoutMs: 1000 });
    });
    await waitFor(() => expect(result.current.status).toBe('pending'));
    await act(async () => {
      release();
      await done;
    });
    expect(result.current.status).toBe('success');
    expect(wait).toHaveBeenCalledWith({ from: MOCK_ADDRESS, nonceBefore: 3, timeoutMs: 1000 });
  });

  it('records the error, rethrows it, and reset() clears it', async () => {
    const wait = vi.fn(async () => {
      throw new Error('timed out');
    });
    const { result } = renderHook(() => useWaitForConfirmation(), { wrapper: wrapper(withProvider(wait)) });
    await act(async () => {
      await expect(result.current.wait({ from: MOCK_ADDRESS, nonceBefore: 0 })).rejects.toThrow('timed out');
    });
    expect(result.current.status).toBe('error');
    expect(result.current.error?.message).toBe('timed out');
    act(() => result.current.reset());
    expect(result.current.status).toBe('idle');
    expect(result.current.error).toBeNull();
  });

  it('an older wait() settling late does not overwrite the latest one', async () => {
    const releases: Array<{ ok: () => void; fail: (e: Error) => void }> = [];
    const wait = vi.fn(() => new Promise<void>((ok, fail) => releases.push({ ok, fail })));
    const { result } = renderHook(() => useWaitForConfirmation(), { wrapper: wrapper(withProvider(wait)) });
    let first!: Promise<void>;
    let second!: Promise<void>;
    act(() => {
      first = result.current.wait({ from: MOCK_ADDRESS, nonceBefore: 1 });
      second = result.current.wait({ from: MOCK_ADDRESS, nonceBefore: 2 });
    });
    await act(async () => {
      releases[1]!.ok();
      await second;
    });
    expect(result.current.status).toBe('success');
    await act(async () => {
      releases[0]!.fail(new Error('old timeout'));
      await expect(first).rejects.toThrow('old timeout');
    });
    expect(result.current.status).toBe('success');
    expect(result.current.error).toBeNull();
  });

  it('refuses to wait without a connected wallet', async () => {
    const wait = vi.fn(async () => undefined);
    const { result } = renderHook(() => useWaitForConfirmation(), {
      wrapper: wrapper(withProvider(wait, 'disconnected')),
    });
    await expect(result.current.wait({ from: MOCK_ADDRESS, nonceBefore: 0 })).rejects.toThrow(/not ready/);
    expect(wait).not.toHaveBeenCalled();
  });
});

describe('useAddressBook', () => {
  it('saves, labels and removes contacts for the current network', () => {
    const ctrl = createMockConnectController({ network: 'local' });
    const { result } = renderHook(() => useAddressBook(), { wrapper: wrapper(ctrl) });
    let saved: ReturnType<typeof result.current.save> | undefined;
    act(() => {
      saved = result.current.save(OTHER, 'Alice');
    });
    expect(saved).toMatchObject({ ok: true });
    expect(result.current.contacts.map((c) => c.label)).toEqual(['Alice']);
    expect(result.current.labelOf(OTHER.toLowerCase())).toBe('Alice');
    expect(result.current.labelOf(null)).toBeNull();
    act(() => result.current.remove(OTHER));
    expect(result.current.contacts).toEqual([]);
  });

  it('keeps contacts per network', async () => {
    const ctrl = createMockConnectController({ network: 'local' });
    const { result } = renderHook(() => useAddressBook(), { wrapper: wrapper(ctrl) });
    act(() => {
      result.current.save(OTHER, 'Local friend');
    });
    await act(async () => {
      await ctrl.rebindNetwork('devnet');
    });
    expect(result.current.contacts).toEqual([]);
    expect(result.current.labelOf(OTHER)).toBeNull();
  });

  it('rejects an invalid address without throwing', () => {
    const { result } = renderHook(() => useAddressBook(), { wrapper: wrapper(createMockConnectController()) });
    let saved: ReturnType<typeof result.current.save> | undefined;
    act(() => {
      saved = result.current.save('0xdeadbeef', 'EVM');
    });
    expect(saved).toMatchObject({ ok: false });
    expect(result.current.contacts).toEqual([]);
  });
});

describe('useAccountDto', () => {
  it('connected, locked and disconnected shapes', () => {
    const connected = renderHook(() => useAccountDto(), { wrapper: wrapper(createMockConnectController()) });
    expect(connected.result.current).toMatchObject({
      address: MOCK_ADDRESS,
      status: 'connected',
      isConnected: true,
      isLocked: false,
      postQuantumSecure: true,
    });
    const locked = renderHook(() => useAccountDto(), { wrapper: wrapper(createMockConnectController({ status: 'locked' })) });
    expect(locked.result.current).toMatchObject({ isConnected: false, isLocked: true });
    const off = renderHook(() => useAccountDto(), {
      wrapper: wrapper(createMockConnectController({ status: 'disconnected' })),
    });
    expect(off.result.current).toMatchObject({ address: null, isConnected: false, postQuantumSecure: null });
  });
});
