/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { DeployTokenWidget } from '../widgets/DeployTokenWidget.js';
import { AccountCard } from '../widgets/AccountCard.js';
import { SignInButton } from '../widgets/SignInButton.js';
import { TxToastHost } from '../ui/TxToastHost.js';
import { BrandContext, ControllerContext, NetworkSwitchContext, type ConnectBrandLabels } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState, ConnectionStatus } from '../core/types.js';
import { clickWhenEnabled } from './test-helpers.js';

const ADDR = 'SQ:' + 'ab'.repeat(32);
const OTHER = 'SQ:' + 'cd'.repeat(32);
const TOKEN = 'SQ:' + 'ee'.repeat(32);
const DEPLOYED = 'SQ:' + '77'.repeat(32);

const { deployQRC20 } = vi.hoisted(() => ({
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
    transferHuman() {
      return Promise.resolve({ status: 'ok', hash: 'tok-hash' });
    }
  },
  deployQRC20: (...args: unknown[]) => deployQRC20(...(args as [])),
}));

function makeState(status: ConnectionStatus): ConnectControllerState {
  const connected = status === 'connected' || status === 'locked';
  return {
    status,
    connectingStep: null,
    account: connected
      ? { address: ADDR, connectorId: 'seed', keyOrigin: 'native', postQuantumSecure: true }
      : null,
    info: connected
      ? { address: ADDR, public_key_b64: 'x', public_key_hex: '00', postQuantumSecure: true, keyOrigin: 'native' }
      : null,
    error: null,
    modal: 'closed',
    connectors: [],
    activeConnectorId: connected ? 'seed' : null,
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

type SetupOpts = {
  status?: ConnectionStatus;
  sendTransfer?: ReturnType<typeof vi.fn>;
  brand?: ConnectBrandLabels;
  toastHost?: boolean;
};

function setup({
  status = 'connected',
  sendTransfer = vi.fn(async () => ({ status: 'ok', hash: 'h1', confirmation: { ok: true } })),
  brand = {},
  toastHost = false,
}: SetupOpts = {}) {
  const state = makeState(status);
  const wallet = {
    sendTransfer,
    connection: {},
    signMessage: vi.fn(async (message: string) => ({ message, signatureB64: 'sig', publicKeyB64: 'pk' })),
  };
  const provider = { getBalance: vi.fn(async () => ({ balance: '0', balance_spld: '1.5' })) };
  const ctrl = {
    getState: () => state,
    subscribe: () => () => undefined,
    connect: vi.fn(async () => state.info!),
    disconnect: vi.fn(async () => undefined),
    clearError: vi.fn(),
    openModal: vi.fn(),
    closeModal: vi.fn(),
    getWallet: () => (status === 'connected' ? wallet : null),
    getProvider: () => provider,
    getWalletEpoch: () => 0,
    isStub: false,
  } as unknown as ConnectController;
  function Wrap({ children }: { children: ReactNode }) {
    const inner = toastHost ? React.createElement(TxToastHost, { autoDismissMs: 0 }, children) : children;
    return React.createElement(
      ControllerContext.Provider,
      { value: ctrl },
      React.createElement(
        BrandContext.Provider,
        { value: brand },
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
          inner,
        ),
      ),
    );
  }
  return { Wrap, ctrl, wallet, sendTransfer };
}

const click = (root: ReturnType<typeof within>, name: RegExp) =>
  fireEvent.click(root.getByRole('button', { name }));

const widgets: Array<{ kind: string; title: string; el: (p: Record<string, unknown>) => React.ReactElement }> = [
  { kind: 'transfer', title: 'Transfer SPLD', el: (p) => React.createElement(TransferWidget, p) },
  { kind: 'token', title: 'Token', el: (p) => React.createElement(TokenPanel, p) },
  { kind: 'deploy', title: 'Deploy QRC-20', el: (p) => React.createElement(DeployTokenWidget, p) },
  { kind: 'account', title: 'Account', el: (p) => React.createElement(AccountCard, p) },
  { kind: 'signin', title: 'Sign in', el: (p) => React.createElement(SignInButton, p) },
];

beforeEach(() => {
  localStorage.clear();
  deployQRC20.mockClear();
});
afterEach(() => cleanup());

describe.each(widgets)('$kind widget: shared props', ({ kind, title, el }) => {
  it('renders a root with kind class, data attributes, className and style', () => {
    const { Wrap } = setup();
    const view = render(el({ className: 'mine', style: { maxWidth: '321px' } }), { wrapper: Wrap });
    const root = view.container.querySelector(`[data-sqc-widget="${kind}"]`) as HTMLElement;
    expect(root).toBeTruthy();
    expect(root.classList.contains('sqc-widget')).toBe(true);
    expect(root.classList.contains(`sqc-widget--${kind}`)).toBe(true);
    expect(root.classList.contains('mine')).toBe(true);
    expect(root.style.maxWidth).toBe('321px');
    expect(root.getAttribute('data-status')).toBe('idle');
  });

  it('uses the default title, a custom title, or none with hideTitle', () => {
    const { Wrap } = setup();
    const first = render(el({}), { wrapper: Wrap });
    expect(first.container.querySelector('.sqc-widget__title')?.textContent).toBe(title);
    cleanup();
    const custom = render(el({ title: 'My panel' }), { wrapper: Wrap });
    expect(custom.container.querySelector('.sqc-widget__title')?.textContent).toBe('My panel');
    cleanup();
    const hidden = render(el({ hideTitle: true }), { wrapper: Wrap });
    expect(hidden.container.querySelector('.sqc-widget__title')).toBeNull();
  });

  it('labels.title from the provider applies, and the labels prop wins over it', () => {
    const key = kind === 'signin' ? 'signIn' : kind;
    const { Wrap } = setup({ brand: { widgets: { [key]: { title: 'From provider' } } } });
    const a = render(el({}), { wrapper: Wrap });
    expect(a.container.querySelector('.sqc-widget__title')?.textContent).toBe('From provider');
    cleanup();
    const b = render(el({ labels: { title: 'From prop' } }), { wrapper: Wrap });
    expect(b.container.querySelector('.sqc-widget__title')?.textContent).toBe('From prop');
  });

  it('shows a Connect button when disconnected and opens the connect modal', () => {
    const { Wrap, ctrl } = setup({ status: 'disconnected' });
    const view = render(el({}), { wrapper: Wrap });
    const gate = view.container.querySelector('[data-sqc-gate="disconnected"]');
    expect(gate).toBeTruthy();
    click(within(view.container), /^Connect$/);
    expect(ctrl.openModal).toHaveBeenCalledWith('connect');
  });

  it('shows an Unlock button when the wallet is locked', () => {
    const { Wrap } = setup({ status: 'locked' });
    const view = render(el({}), { wrapper: Wrap });
    expect(view.container.querySelector('[data-sqc-gate="locked"]')).toBeTruthy();
    expect(within(view.container).getByRole('button', { name: /^Unlock$/ })).toBeTruthy();
  });
});

describe('common labels from the provider', () => {
  it('labels.widgets.common changes the gate text of every widget', () => {
    const { Wrap } = setup({ status: 'disconnected', brand: { widgets: { common: { connect: 'Verbinden' } } } });
    const view = render(
      React.createElement(React.Fragment, null, ...widgets.map((w) => w.el({ key: w.kind }))),
      { wrapper: Wrap },
    );
    expect(within(view.container).getAllByRole('button', { name: 'Verbinden' })).toHaveLength(widgets.length);
  });

  it('per-widget labels replace field text and keep {placeholders} working', async () => {
    const { Wrap } = setup();
    const view = render(
      React.createElement(TransferWidget, {
        labels: { toLabel: 'Recipient', amountLabel: 'How much', balance: 'You have {balance} {symbol}' },
      }),
      { wrapper: Wrap },
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText('You have 1.5 SPLD')).toBeTruthy());
    expect(root.getByLabelText('Recipient')).toBeTruthy();
    expect(root.getByLabelText('How much')).toBeTruthy();
  });
});

describe('TransferWidget contract', () => {
  async function send(props: Record<string, unknown>, opts: SetupOpts = {}) {
    const ctx = setup(opts);
    const view = render(
      React.createElement(TransferWidget, { defaultTo: OTHER, defaultAmount: '0.25', ...props }),
      { wrapper: ctx.Wrap },
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    click(root, /^Review$/i);
    await clickWhenEnabled(root, /Confirm send/i);
    return { ...ctx, view, root };
  }

  it('reports pending then success, and calls onSuccess and the onSent alias', async () => {
    const onStatusChange = vi.fn();
    const onSuccess = vi.fn();
    const onSent = vi.fn();
    const { view } = await send({ onStatusChange, onSuccess, onSent });
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
    expect(onSent).toHaveBeenCalledTimes(1);
    expect(onSuccess.mock.calls[0]![0]).toMatchObject({ to: OTHER, amount: '0.25', hash: 'h1' });
    expect(onSent.mock.calls[0]![0]).toEqual(onSuccess.mock.calls[0]![0]);
    // The status is reported from the render after the callback, so wait for it rather than racing it.
    await waitFor(() => expect(onStatusChange.mock.calls.map((c) => c[0])).toEqual(['pending', 'success']));
    expect(view.container.querySelector('[data-sqc-widget]')?.getAttribute('data-status')).toBe('success');
  });

  it('reports error and calls onError, not onSuccess, when the wallet rejects', async () => {
    const onStatusChange = vi.fn();
    const onSuccess = vi.fn();
    const onError = vi.fn();
    await send(
      { onStatusChange, onSuccess, onError },
      { sendTransfer: vi.fn(async () => Promise.reject(new Error('User rejected transaction'))) },
    );
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onStatusChange.mock.calls.map((c) => c[0])).toEqual(['pending', 'error']);
  });

  it('pushes pending and success toasts when a TxToastHost is mounted', async () => {
    const onSuccess = vi.fn();
    const { view } = await send({ onSuccess }, { toastHost: true });
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    const toasts = view.container.querySelectorAll('.sqc-tx-toast');
    expect(toasts).toHaveLength(1);
    expect(toasts[0]!.classList.contains('sqc-tx-toast--success')).toBe(true);
    expect(toasts[0]!.textContent).toContain('SPLD sent');
  });

  it('pushes no toast with toast={false}, and none (without a warning) when no host is mounted', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const a = vi.fn();
    const first = await send({ onSuccess: a, toast: false }, { toastHost: true });
    await waitFor(() => expect(a).toHaveBeenCalled());
    expect(first.view.container.querySelectorAll('.sqc-tx-toast')).toHaveLength(0);
    cleanup();
    const b = vi.fn();
    await send({ onSuccess: b });
    await waitFor(() => expect(b).toHaveBeenCalled());
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('shows an error toast when the send fails', async () => {
    const onError = vi.fn();
    const { view } = await send(
      { onError },
      { toastHost: true, sendTransfer: vi.fn(async () => Promise.reject(new Error('nope'))) },
    );
    await waitFor(() => expect(onError).toHaveBeenCalled());
    const toast = view.container.querySelector('.sqc-tx-toast--error');
    expect(toast?.textContent).toContain('Transfer failed');
  });
});

describe('TokenPanel contract', () => {
  it('onSuccess and the onSent alias both receive the token transfer', async () => {
    const onSuccess = vi.fn();
    const onSent = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(TokenPanel, { tokenAddress: TOKEN, onSuccess, onSent }), {
      wrapper: Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 5(\.0)? LAB/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    click(root, /^Review$/i);
    await clickWhenEnabled(root, /Confirm send/i);
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
    expect(onSuccess).toHaveBeenCalledWith({ token: TOKEN, to: OTHER, amount: '1', hash: 'tok-hash' });
    expect(onSent).toHaveBeenCalledWith(onSuccess.mock.calls[0]![0]);
  });

  it('rejects a malformed token address without loading it', () => {
    const { Wrap } = setup();
    const view = render(React.createElement(TokenPanel), { wrapper: Wrap });
    const root = within(view.container);
    fireEvent.change(root.getByLabelText(/^Token \(SQ/i), { target: { value: '0x1234' } });
    expect(root.getByText('Enter a valid SQ: token address')).toBeTruthy();
  });
});

describe('DeployTokenWidget contract', () => {
  it('onSuccess gets address, hash and explorerUrl; onDeployed still fires', async () => {
    const onSuccess = vi.fn();
    const onDeployed = vi.fn();
    const onStatusChange = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(DeployTokenWidget, { onSuccess, onDeployed, onStatusChange }), {
      wrapper: Wrap,
    });
    click(within(view.container), /^Review deploy$/i);
    await clickWhenEnabled(within(view.container), /^Confirm deploy$/i);
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
    expect(onSuccess.mock.calls[0]![0]).toMatchObject({ address: DEPLOYED, hash: 'dep-hash' });
    expect(onDeployed).toHaveBeenCalledWith({ address: DEPLOYED, hash: 'dep-hash' });
    expect(onStatusChange.mock.calls.map((c) => c[0])).toEqual(['pending', 'success']);
  });

  it('showDecimals adds an editable Decimals field that is validated', () => {
    const { Wrap } = setup();
    const view = render(React.createElement(DeployTokenWidget, { showDecimals: true }), { wrapper: Wrap });
    const root = within(view.container);
    const input = root.getByLabelText('Decimals') as HTMLInputElement;
    expect(input.value).toBe('18');
    fireEvent.change(input, { target: { value: '19' } });
    expect(root.getByText(/Decimals must be a whole number/)).toBeTruthy();
    expect((root.getByRole('button', { name: /^Review deploy$/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('maxSupply below the total supply blocks the deploy', () => {
    const { Wrap } = setup();
    const view = render(React.createElement(DeployTokenWidget, { maxSupply: '10', defaultSupply: '100' }), {
      wrapper: Wrap,
    });
    const root = within(view.container);
    expect(root.getByText('Total supply cannot exceed the max supply (10)')).toBeTruthy();
    expect((root.getByRole('button', { name: /^Review deploy$/ }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('AccountCard contract', () => {
  it('hides rows with showBalance / showNetwork / showDisconnect = false', () => {
    const { Wrap } = setup();
    const view = render(
      React.createElement(AccountCard, { showBalance: false, showNetwork: false, showDisconnect: false }),
      { wrapper: Wrap },
    );
    const root = within(view.container);
    expect(root.queryByRole('button', { name: /^Refresh$/ })).toBeNull();
    expect(root.queryByRole('button', { name: /^Disconnect$/ })).toBeNull();
    expect(root.queryByText('Local')).toBeNull();
  });

  it('onDisconnect fires after Disconnect', async () => {
    const onDisconnect = vi.fn();
    const { Wrap, ctrl } = setup();
    const view = render(React.createElement(AccountCard, { onDisconnect }), { wrapper: Wrap });
    click(within(view.container), /^Disconnect$/);
    await waitFor(() => expect(onDisconnect).toHaveBeenCalledTimes(1));
    expect(ctrl.disconnect).toHaveBeenCalled();
  });
});

describe('SignInButton contract', () => {
  it('onSuccess and the onSignedIn alias receive the signed auth', async () => {
    const onSuccess = vi.fn();
    const onSignedIn = vi.fn();
    const onStatusChange = vi.fn();
    const { Wrap } = setup();
    const view = render(React.createElement(SignInButton, { onSuccess, onSignedIn, onStatusChange }), {
      wrapper: Wrap,
    });
    click(within(view.container), /^Sign in$/);
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
    expect(onSuccess.mock.calls[0]![0]).toMatchObject({ address: ADDR, signatureB64: 'sig' });
    expect(onSignedIn).toHaveBeenCalledWith(onSuccess.mock.calls[0]![0]);
    expect(onStatusChange.mock.calls.map((c) => c[0])).toEqual(['pending', 'success']);
  });

  it('onError fires and status is error when the domain check fails', async () => {
    const onError = vi.fn();
    const onSuccess = vi.fn();
    const { Wrap, wallet } = setup();
    const view = render(
      React.createElement(SignInButton, { domain: 'https://evil.example', onError, onSuccess }),
      { wrapper: Wrap },
    );
    click(within(view.container), /^Sign in$/);
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onSuccess).not.toHaveBeenCalled();
    expect(wallet.signMessage).not.toHaveBeenCalled();
    expect(view.container.querySelector('[data-sqc-widget]')?.getAttribute('data-status')).toBe('error');
  });

  it('label sets the button text', () => {
    const { Wrap } = setup();
    const view = render(React.createElement(SignInButton, { label: 'Log in with Splendor' }), { wrapper: Wrap });
    expect(within(view.container).getByRole('button', { name: 'Log in with Splendor' })).toBeTruthy();
  });
});
