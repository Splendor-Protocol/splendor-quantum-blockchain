import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { emptySpec } from '@splendor/dapp-spec';
import { MAX_RELOADS, PreviewFrame } from './PreviewFrame';
import { PREVIEW_SOURCE } from './protocol';

afterEach(cleanup);

const frame = () => screen.getByTestId('preview-frame') as HTMLIFrameElement;
const tokenOf = (el: HTMLIFrameElement) => new URL(el.getAttribute('src')!, 'http://x').hash.replace('#t=', '');

/** A message from the frame's window, as the preview runtime posts it. */
function fromFrame(data: object) {
  const el = frame();
  const origin = el.dataset.isolated === 'true' ? 'null' : window.location.origin;
  act(() => {
    window.dispatchEvent(new MessageEvent('message', { data: { source: PREVIEW_SOURCE, ...data }, origin, source: el.contentWindow }));
  });
}

/** The frame's document loads again, as when a link navigated it away; returns once it was replaced (or stopped). */
function navigateAway() {
  const before = frame();
  for (let i = 0; i < 3 && frame() === before && !screen.queryByTestId('preview-timeout'); i++) {
    act(() => {
      fireEvent.load(frame());
    });
  }
}

const renderFrame = (onNavigate = vi.fn()) => {
  render(<PreviewFrame spec={emptySpec('Frame test')} pagePath="/" network="testnet" walletState="connected" onNavigate={onNavigate} />);
  return onNavigate;
};

describe('PreviewFrame', () => {
  it('opens the preview entry with a fresh token in the fragment', () => {
    renderFrame();
    expect(frame().getAttribute('src')).toMatch(/preview\.html#t=[0-9a-f]{32}$/);
  });

  it('ignores messages without the frame token, and follows those with it', () => {
    const onNavigate = renderFrame();
    fromFrame({ type: 'navigate', pagePath: '/x' });
    fromFrame({ type: 'navigate', pagePath: '/x', token: 'a'.repeat(32) });
    expect(onNavigate).not.toHaveBeenCalled();
    fromFrame({ type: 'navigate', pagePath: '/x', token: tokenOf(frame()) });
    expect(onNavigate).toHaveBeenCalledWith('/x');
  });

  it('sends the frame token with every message to the frame', () => {
    renderFrame();
    const el = frame();
    const spy = vi.spyOn(el.contentWindow!, 'postMessage').mockImplementation(() => {});
    fromFrame({ type: 'ready', token: tokenOf(el) });
    expect(spy).toHaveBeenCalled();
    for (const [msg] of spy.mock.calls) expect((msg as { token?: string }).token).toBe(tokenOf(el));
  });

  it('replaces a frame that left the preview with a new one and a new token, and stops after a few', () => {
    const onNavigate = renderFrame();
    const tokens = new Set([tokenOf(frame())]);
    for (let i = 0; i < MAX_RELOADS; i++) {
      navigateAway();
      tokens.add(tokenOf(frame()));
    }
    expect(tokens.size).toBe(MAX_RELOADS + 1);
    expect(screen.queryByTestId('preview-timeout')).toBeNull();

    // One navigation too many: the frame is left alone and says so; its old token no longer works.
    const last = tokenOf(frame());
    navigateAway();
    expect(screen.getByTestId('preview-timeout').textContent).toMatch(/left this page/);
    fromFrame({ type: 'navigate', pagePath: '/y', token: 'b'.repeat(32) });
    expect(onNavigate).not.toHaveBeenCalled();

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Reload preview' }));
    });
    expect(screen.queryByTestId('preview-timeout')).toBeNull();
    expect(tokenOf(frame())).not.toBe(last);
  });
});
