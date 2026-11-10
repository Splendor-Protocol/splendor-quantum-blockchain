/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, render, renderHook } from '@testing-library/react';
import { BridgeClaimWidget } from '../widgets/BridgeClaimWidget.js';
import { useAddressBook } from '../react/hooks/addressBook.js';
import { useActivity } from '../react/hooks/chain.js';
import { __resetDevWarnings, devWarn } from '../core/devWarn.js';
import { MockSplendorProvider, createMockConnectController, type MockConnectController } from '../testing.js';

const wrapper = (ctrl: MockConnectController) =>
  function Wrapper({ children }: { children: ReactNode }) {
    return <MockSplendorProvider controller={ctrl}>{children}</MockSplendorProvider>;
  };

let warn: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  __resetDevWarnings();
  warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
});
afterEach(() => {
  cleanup();
  warn.mockRestore();
  vi.unstubAllEnvs();
});

describe('development warnings for silent misconfiguration', () => {
  it('devWarn warns once per key and never in production', () => {
    devWarn('k', 'first');
    devWarn('k', 'again');
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn).toHaveBeenCalledWith('@splendor/connect-react: first');
    __resetDevWarnings();
    vi.stubEnv('NODE_ENV', 'production');
    devWarn('k', 'prod');
    expect(warn).toHaveBeenCalledTimes(1);
  });

  it('BridgeClaimWidget without a bridgeAddress, or with a bad one', () => {
    const ctrl = createMockConnectController();
    render(<MockSplendorProvider controller={ctrl}><BridgeClaimWidget /></MockSplendorProvider>);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('needs a bridgeAddress'));
    cleanup();
    render(<MockSplendorProvider controller={ctrl}><BridgeClaimWidget bridgeAddress="0xabc" /></MockSplendorProvider>);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('"0xabc" is not an SQ: address'));
  });

  it('BridgeClaimWidget with a valid address stays quiet', () => {
    render(
      <MockSplendorProvider controller={createMockConnectController()}>
        <BridgeClaimWidget bridgeAddress={'SQ:' + 'aa'.repeat(32)} />
      </MockSplendorProvider>,
    );
    expect(warn).not.toHaveBeenCalledWith(expect.stringContaining('BridgeClaimWidget'));
  });

  it('useAddressBook().save before a network is set', () => {
    const ctrl = createMockConnectController();
    act(() => ctrl.setState({ network: null as never }));
    const { result } = renderHook(() => useAddressBook(), { wrapper: wrapper(ctrl) });
    expect(result.current.save('SQ:' + 'cd'.repeat(32), 'x')).toEqual({ ok: false, reason: 'no-network' });
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('before a network was set'));
  });

  it('useActivity with an indexer URL it refuses to fetch', () => {
    renderHook(() => useActivity(undefined, { indexerUrl: 'http://indexer.example' }), {
      wrapper: wrapper(createMockConnectController()),
    });
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('indexerUrl "http://indexer.example" was ignored'));
  });
});
