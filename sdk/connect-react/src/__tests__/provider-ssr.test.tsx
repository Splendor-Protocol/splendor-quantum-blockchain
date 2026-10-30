/**
 * @vitest-environment node
 */
import { describe, expect, it, vi } from 'vitest';
import React from 'react';
import { renderToString } from 'react-dom/server';

const { createSpy } = vi.hoisted(() => ({ createSpy: vi.fn() }));

vi.mock('../core/controller.js', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../core/controller.js')>();
  return {
    ...mod,
    createConnectController: (opts: unknown) => {
      createSpy(opts);
      throw new Error('a real controller must not be created during server rendering');
    },
  };
});

const { SplendorConnectProvider } = await import('../react/provider.js');
const { ConnectButton } = await import('../ui/ConnectButton.js');

describe('SplendorConnectProvider on the server', () => {
  it('renders to a string without window, and without creating a wallet controller', () => {
    expect(typeof (globalThis as { window?: unknown }).window).toBe('undefined');
    const html = renderToString(
      React.createElement(
        SplendorConnectProvider,
        { network: 'local', connectors: 'auto', hostModals: false, autoReconnect: true },
        React.createElement(ConnectButton),
      ),
    );
    expect(html.length).toBeGreaterThan(0);
    expect(createSpy).not.toHaveBeenCalled();
  });
});
