/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React, { StrictMode } from 'react';
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react';
import type { Connector } from '../connectors/types.js';
import { createMockConnectController, MockSplendorProvider, MOCK_ADDRESS } from '../testing.js';

/** Each reconnect attempt, with whether it was already stale when it ran. */
const { attempts, created } = vi.hoisted(() => ({
  attempts: [] as { isStale: () => boolean }[],
  created: [] as { destroyed: boolean }[],
}));

vi.mock('../core/controller.js', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../core/controller.js')>();
  return {
    ...mod,
    createConnectController: () => {
      const ctrl = createMockConnectController({ status: 'disconnected' });
      const record = { destroyed: false };
      created.push(record);
      ctrl.tryAutoReconnect = async (opts?: { isStale?: () => boolean }) => {
        attempts.push({ isStale: opts?.isStale ?? (() => false) });
        return false;
      };
      const destroy = ctrl.destroy;
      ctrl.destroy = () => {
        record.destroyed = true;
        destroy();
      };
      return ctrl;
    },
  };
});

const { SplendorConnectProvider } = await import('../react/provider.js');
const { ConnectModal } = await import('../ui/ConnectModal.js');

afterEach(() => {
  cleanup();
  attempts.length = 0;
  created.length = 0;
});

const connectors = [{ id: 'dummy', name: 'Dummy', ready: async () => true }] as never;

describe('SplendorConnectProvider under StrictMode', () => {
  it('runs one live auto-reconnect: the double-mounted effect marks the first attempt stale', async () => {
    render(
      React.createElement(
        StrictMode,
        null,
        React.createElement(SplendorConnectProvider, { connectors, hostModals: false, autoReconnect: true }, null),
      ),
    );
    await waitFor(() => expect(attempts.length).toBeGreaterThan(0));
    // Let the deferred destroy of the discarded mount run.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(attempts.filter((a) => !a.isStale())).toHaveLength(1);
    // The controller in use survives StrictMode's unmount/remount.
    expect(created.at(-1)?.destroyed).toBe(false);
  });
});

describe('ConnectModal without a wallet extension', () => {
  it('marks the extension as not installed and does not let it be picked', async () => {
    const extension = {
      id: 'splendor-extension',
      name: 'Splendor Quantum Wallet',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: async () => false,
      connect: async () => ({ address: MOCK_ADDRESS }),
      disconnect: async () => undefined,
    } as unknown as Connector;
    const ctrl = createMockConnectController({ status: 'disconnected', connectors: [extension] });
    ctrl.setState({ modal: 'connect' });
    render(React.createElement(MockSplendorProvider, { controller: ctrl }, React.createElement(ConnectModal)));

    const item = await within(document.body).findByRole('button', { name: /Splendor Quantum Wallet/ });
    await waitFor(() => expect(screen.getByText('Extension not installed')).toBeTruthy());
    expect((item as HTMLButtonElement).disabled).toBe(true);
  });
});
