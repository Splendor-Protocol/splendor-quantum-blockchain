/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { DeployTokenWidget } from '../widgets/DeployTokenWidget.js';
import { ControllerContext, NetworkSwitchContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);
const OTHER = 'SQ:' + 'cd'.repeat(32);
const TOKEN = 'SQ:' + 'ee'.repeat(32);

const { transferHuman, deployQRC20 } = vi.hoisted(() => ({
  transferHuman: vi.fn(async () => ({ status: 'ok', hash: 'tok-hash' })),
  deployQRC20: vi.fn(async () => ({ address: 'SQ:' + '77'.repeat(32), result: { hash: 'dep-hash' } })),
}));

vi.mock('@splendor/sqc-sdk/tokens', () => ({
  QRC20: class {
    balanceOf() {
      return Promise.resolve('5000000000000000000');
    }
    pinDecimals() {
      return this;
    }
    tokenInfo() {
      return Promise.resolve({ symbol: 'LAB', decimals: 18 });
    }
    transferHuman(...args: unknown[]) {
      return transferHuman(...(args as []));
    }
  },
  deployQRC20: (...args: unknown[]) => deployQRC20(...(args as [])),
}));

function makeState(): ConnectControllerState {
  return {
    status: 'connected',
    connectingStep: null,
    account: { address: ADDR, connectorId: 'seed', keyOrigin: 'native', postQuantumSecure: true },
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
    activeConnectorId: 'seed',
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
  } as ConnectControllerState;
}

function setup(sendTransfer = vi.fn(async () => ({ status: 'ok', hash: 'h1', confirmation: { ok: true } }))) {
  const state = makeState();
  const wallet = { sendTransfer, connection: {} };
  const provider = { getBalance: vi.fn(async () => ({ balance: '0', balance_spld: '1.5' })) };
  const ctrl = {
    getState: () => state,
    subscribe: () => () => undefined,
    connect: vi.fn(async () => state.info!),
    disconnect: vi.fn(async () => undefined),
    clearError: vi.fn(),
    openModal: vi.fn(),
    closeModal: vi.fn(),
    getWallet: () => wallet,
    getProvider: () => provider,
    getWalletEpoch: () => 0,
    isStub: false,
  } as unknown as ConnectController;
  function Wrap({ children }: { children: ReactNode }) {
    return React.createElement(
      ControllerContext.Provider,
      { value: ctrl },
      React.createElement(
        NetworkSwitchContext.Provider,
        {
          value: {
            switchNetwork: vi.fn(async () => undefined) as (id: never) => Promise<void>,
            allowPlaceholderNetworks: true,
            networkId: 'local' as never,
            switching: false,
          },
        },
        children,
      ),
    );
  }
  return { Wrap, sendTransfer };
}

const click = (root: ReturnType<typeof within>, name: RegExp) =>
  fireEvent.click(root.getByRole('button', { name }));

describe('TransferWidget callbacks', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => cleanup());

  it('defaultTo prefills the recipient', async () => {
    const { Wrap } = setup();
    const view = render(React.createElement(TransferWidget, { defaultTo: OTHER }), { wrapper: Wrap });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    expect((root.getByLabelText(/^To \(SQ/i) as HTMLInputElement).value).toBe(OTHER);
  });

  it('onSent receives the recipient, amount and hash after a send', async () => {
    const onSent = vi.fn();
    const { Wrap } = setup();
    const view = render(
      React.createElement(TransferWidget, { defaultTo: OTHER, defaultAmount: '0.25', onSent }),
      { wrapper: Wrap },
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    click(root, /^Review$/i);
    click(root, /Confirm send/i);
    await waitFor(() => expect(onSent).toHaveBeenCalledTimes(1));
    expect(onSent.mock.calls[0]![0]).toMatchObject({ to: OTHER, amount: '0.25', hash: 'h1' });
  });

  it('onError receives the error and onSent is not called when the send fails', async () => {
    const onSent = vi.fn();
    const onError = vi.fn();
    const { Wrap } = setup(
      vi.fn(async () => {
        throw new Error('User rejected transaction');
      }),
    );
    const view = render(
      React.createElement(TransferWidget, { defaultTo: OTHER, defaultAmount: '0.25', onSent, onError }),
      { wrapper: Wrap },
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    click(root, /^Review$/i);
    click(root, /Confirm send/i);
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect((onError.mock.calls[0]![0] as Error).message).toMatch(/rejected/);
    expect(onSent).not.toHaveBeenCalled();
  });
});

describe('TokenPanel callbacks', () => {
  beforeEach(() => {
    localStorage.clear();
    transferHuman.mockClear();
  });
  afterEach(() => cleanup());

  it('defaultAmount prefills the amount and onSent reports the token transfer', async () => {
    const onSent = vi.fn();
    const { Wrap } = setup();
    const view = render(
      React.createElement(TokenPanel, { tokenAddress: TOKEN, defaultAmount: '2', onSent }),
      { wrapper: Wrap },
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 5(\.0)? LAB/)).toBeTruthy());
    expect((root.getByLabelText(/Amount \(human/i) as HTMLInputElement).value).toBe('2');
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    click(root, /^Review$/i);
    click(root, /Confirm send/i);
    await waitFor(() => expect(onSent).toHaveBeenCalledTimes(1));
    expect(onSent.mock.calls[0]![0]).toEqual({ token: TOKEN, to: OTHER, amount: '2', hash: 'tok-hash' });
  });

  it('onError fires when the token transfer fails', async () => {
    transferHuman.mockImplementationOnce(async () => {
      throw new Error('insufficient token balance');
    });
    const onError = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(TokenPanel, { tokenAddress: TOKEN, onError }), {
      wrapper: Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 5(\.0)? LAB/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    click(root, /^Review$/i);
    click(root, /Confirm send/i);
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect((onError.mock.calls[0]![0] as Error).message).toMatch(/insufficient/);
  });
});

describe('DeployTokenWidget callbacks', () => {
  beforeEach(() => deployQRC20.mockClear());
  afterEach(() => cleanup());

  it('onDeployed receives the token address and hash', async () => {
    const onDeployed = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(DeployTokenWidget, { onDeployed }), { wrapper: Wrap });
    click(within(view.container), /^Review deploy$/i);
    click(within(view.container), /^Confirm deploy$/i);
    await waitFor(() => expect(onDeployed).toHaveBeenCalledTimes(1));
    expect(onDeployed).toHaveBeenCalledWith({ address: 'SQ:' + '77'.repeat(32), hash: 'dep-hash' });
  });

  it('onError fires when the deploy fails', async () => {
    deployQRC20.mockImplementationOnce(async () => {
      throw new Error('fee below minimum');
    });
    const onError = vi.fn();
    const onDeployed = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(DeployTokenWidget, { onDeployed, onError }), {
      wrapper: Wrap,
    });
    click(within(view.container), /^Review deploy$/i);
    click(within(view.container), /^Confirm deploy$/i);
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onDeployed).not.toHaveBeenCalled();
  });
});
