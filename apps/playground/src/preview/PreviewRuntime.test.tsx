import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { emptySpec } from '@splendor/dapp-spec';
import { PreviewRuntime } from './PreviewRuntime';
import { PREVIEW_SOURCE } from './protocol';

const TOKEN = 'c'.repeat(32);

function fromParent(data: object) {
  act(() => {
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { source: PREVIEW_SOURCE, ...data },
        origin: window.location.origin,
        source: window.parent,
      }),
    );
  });
}

const renderMsg = (token?: string) => ({
  type: 'render',
  spec: emptySpec('Runtime test'),
  pagePath: '/',
  network: 'testnet',
  walletState: 'connected',
  selectedBlockId: null,
  ...(token ? { token } : {}),
});

beforeEach(() => {
  window.location.hash = `#t=${TOKEN}`;
  vi.spyOn(window.parent, 'postMessage').mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = '';
});

describe('PreviewRuntime', () => {
  it('ignores parent messages without the frame token', () => {
    render(<PreviewRuntime />);
    fromParent(renderMsg());
    fromParent(renderMsg('d'.repeat(32)));
    expect(screen.getByText(/Waiting for the playground/)).toBeTruthy();
  });

  it('renders a spec sent with the frame token', () => {
    render(<PreviewRuntime />);
    fromParent(renderMsg(TOKEN));
    expect(screen.queryByText(/Waiting for the playground/)).toBeNull();
  });
});
