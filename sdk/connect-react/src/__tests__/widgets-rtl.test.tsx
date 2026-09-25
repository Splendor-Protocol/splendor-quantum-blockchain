/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi } from 'vitest';
import React, { type ReactNode, useEffect } from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { WrongNetworkBanner } from '../ui/WrongNetworkBanner.js';
import { TxToastHost, useTxToast } from '../ui/TxToastHost.js';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { ControllerContext, NetworkSwitchContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);

function makeState(overrides: Partial<ConnectControllerState> = {}): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: {
      address: ADDR,
      connectorId: 'splendor-extension',
      keyOrigin: 'native',
      postQuantumSecure: true,
    },
    info: {
      address: ADDR,
      public_key_b64: 'x',
      public_key_hex: '00',
      postQuantumSecure: true,
      keyOrigin: 'native',
    },
    error: null,
    modal: 'closed',
    connectors: [],
    activeConnectorId: 'splendor-extension',
    network: {
      id: 'local',
      name: 'Local',
      networkId: 'splendor-local',
      chainId: 'splendor-quantum-1',
      rpcUrls: ['http://127.0.0.1:8080'],
      gatewayUrl: '',
      gatewayPort: 3090,
      wasmUrl: '',
      execUrl: '',
      wasmSha256: '',
      execSha256: '',
      timeoutMs: 20000,
      retries: 0,
      explorerUrl: 'https://explorer.example',
    },
    ...overrides,
  } as ConnectControllerState;
}

function makeController(state: ConnectControllerState): ConnectController {
  return {
    getState: () => state,
    subscribe: () => () => undefined,
    connect: vi.fn(async () => state.info!),
    disconnect: vi.fn(async () => undefined),
    clearError: vi.fn(),
    openModal: vi.fn(),
    closeModal: vi.fn(),
    getWallet: () => null,
    getProvider: () => null,
    getWalletEpoch: () => 0,
  } as unknown as ConnectController;
}

function providerWrap(
  ctrl: ConnectController,
  networkOpts: {
    networkId?: string;
    switching?: boolean;
    switchNetwork?: (id: string) => Promise<void>;
  } = {},
) {
  const switchNetwork = networkOpts.switchNetwork ?? vi.fn(async () => undefined);
  return function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(
      ControllerContext.Provider,
      { value: ctrl },
      React.createElement(
        NetworkSwitchContext.Provider,
        {
          value: {
            switchNetwork: switchNetwork as (id: never) => Promise<void>,
            allowPlaceholderNetworks: true,
            networkId: (networkOpts.networkId ?? 'local') as never,
            switching: networkOpts.switching ?? false,
          },
        },
        children,
      ),
    );
  };
}

describe('WrongNetworkBanner', () => {
  it('renders null when on expected network', () => {
    const ctrl = makeController(makeState());
    const { container } = render(
      React.createElement(WrongNetworkBanner, { expectedNetworkId: 'local' }),
      { wrapper: providerWrap(ctrl, { networkId: 'local' }) },
    );
    expect(container.querySelector('.sqc-wrong-network')).toBeNull();
  });

  it('shows switch CTA when on wrong network', () => {
    const switchNetwork = vi.fn(async () => undefined);
    const ctrl = makeController(makeState());
    render(
      React.createElement(WrongNetworkBanner, { expectedNetworkId: 'testnet' }),
      { wrapper: providerWrap(ctrl, { networkId: 'local', switchNetwork }) },
    );
    expect(screen.getByRole('alert').textContent).toMatch(/expects testnet/i);
    fireEvent.click(screen.getByRole('button', { name: /Switch to testnet/i }));
    expect(switchNetwork).toHaveBeenCalledWith('testnet');
  });

  it('shows switching status', () => {
    const ctrl = makeController(makeState());
    render(React.createElement(WrongNetworkBanner, { expectedNetworkId: 'testnet' }), {
      wrapper: providerWrap(ctrl, { networkId: 'local', switching: true }),
    });
    expect(screen.getByRole('status').textContent).toMatch(/Switching network/i);
  });
});

describe('TxToastHost', () => {
  it('renders pushed toasts', () => {
    function Pusher() {
      const { push } = useTxToast();
      useEffect(() => {
        push({ status: 'success', title: 'Sent!', hash: '0xabcdef1234567890' });
      }, [push]);
      return null;
    }
    render(
      React.createElement(
        TxToastHost,
        { autoDismissMs: 0 },
        React.createElement(Pusher),
      ),
    );
    expect(screen.getByText('Sent!')).toBeTruthy();
    expect(screen.getByText(/0xabcdef12/)).toBeTruthy();
  });
});

describe('TransferWidget', () => {
  it('shows unlock UI when wallet is locked', () => {
    const ctrl = makeController(makeState({ status: 'locked', account: null, info: null }));
    render(React.createElement(TransferWidget), {
      wrapper: providerWrap(ctrl),
    });
    expect(screen.getByText(/Wallet locked — unlock to send/i)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Unlock/i }));
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });
});

describe('ConnectModal', () => {
  it('opens without ReferenceError (no legacy group)', async () => {
    const { ConnectModal } = await import('../ui/ConnectModal.js');
    const { seedConnector, extensionConnector } = await import('../connectors/index.js');
    const state = makeState({
      status: 'disconnected',
      account: null,
      info: null,
      modal: 'connect',
      connectors: [extensionConnector({ discoveryTimeoutMs: 50 }), seedConnector()],
    });
    const ctrl = makeController(state);
    expect(() =>
      render(React.createElement(ConnectModal), { wrapper: providerWrap(ctrl) }),
    ).not.toThrow();
    expect(screen.getByText(/Connect to Splendor/i)).toBeTruthy();
  });
});
