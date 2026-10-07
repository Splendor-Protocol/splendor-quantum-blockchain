/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { cleanup, render, waitFor } from '@testing-library/react';
import { getNetwork, type NetworkConfig } from '@splendor/sqc-sdk';
import { createMockConnectController } from '../testing.js';

const { createSpy } = vi.hoisted(() => ({ createSpy: vi.fn() }));

vi.mock('../core/controller.js', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../core/controller.js')>();
  return {
    ...mod,
    createConnectController: (opts: unknown) => {
      createSpy(opts);
      return createMockConnectController();
    },
  };
});

const { SplendorConnectProvider } = await import('../react/provider.js');

const connectors = [{ id: 'dummy', name: 'Dummy' }] as never;

afterEach(() => {
  cleanup();
  createSpy.mockClear();
});

function customNet(id: string): NetworkConfig {
  return { ...getNetwork('local')!, id, name: `Custom ${id}` } as NetworkConfig;
}

describe('SplendorConnectProvider networks', () => {
  it('registers `networks` and hands them to the controller when customNetworks is omitted', async () => {
    const net = customNet('reg-test-networks');
    render(
      React.createElement(
        SplendorConnectProvider,
        { networks: [net], network: net.id, connectors, hostModals: false, autoReconnect: false },
        null,
      ),
    );
    expect(getNetwork(net.id)).toBeTruthy();
    await waitFor(() => expect(createSpy).toHaveBeenCalled());
    const opts = createSpy.mock.calls.at(-1)![0] as { networks?: NetworkConfig[] };
    expect(opts.networks?.map((n) => n.id)).toEqual([net.id]);
  });

  it('customNetworks wins over networks', async () => {
    const a = customNet('reg-test-a');
    const b = customNet('reg-test-b');
    render(
      React.createElement(
        SplendorConnectProvider,
        { networks: [a], customNetworks: [b], connectors, hostModals: false, autoReconnect: false },
        null,
      ),
    );
    await waitFor(() => expect(createSpy).toHaveBeenCalled());
    const opts = createSpy.mock.calls.at(-1)![0] as { networks?: NetworkConfig[] };
    expect(opts.networks?.map((n) => n.id)).toEqual([b.id]);
  });
});
