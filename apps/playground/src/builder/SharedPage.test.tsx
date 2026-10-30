import { act, cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TEMPLATES, instantiateTemplate } from '@splendor/dapp-spec';
import { api } from '../api/client';
import { PREVIEW_SOURCE } from '../preview/protocol';
import { RegistryProvider } from '../registry';
import { SharedPage } from './SharedPage';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('SharedPage', () => {
  it('runs the shared app in play mode, so page gates apply to visitors', async () => {
    const spec = instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!);
    vi.spyOn(api, 'getShared').mockResolvedValue({ name: spec.name, spec });
    vi.spyOn(api, 'catalogFlags').mockResolvedValue({ flags: {} });

    render(
      <RegistryProvider>
        <MemoryRouter initialEntries={['/s/abc']}>
          <Routes>
            <Route path="/s/:shareId" element={<SharedPage />} />
          </Routes>
        </MemoryRouter>
      </RegistryProvider>,
    );
    const frame = (await screen.findByTestId('preview-frame')) as HTMLIFrameElement;
    const token = new URL(frame.getAttribute('src')!, 'http://x').hash.replace('#t=', '');
    const posted = vi.spyOn(frame.contentWindow!, 'postMessage').mockImplementation(() => {});
    const origin = frame.dataset.isolated === 'true' ? 'null' : window.location.origin;
    act(() => {
      window.dispatchEvent(new MessageEvent('message', { data: { source: PREVIEW_SOURCE, type: 'ready', token }, origin, source: frame.contentWindow }));
    });

    const render_ = posted.mock.calls.map(([m]) => m as { type: string; mode?: string }).find((m) => m.type === 'render');
    expect(render_?.mode).toBe('play');
  });
});
