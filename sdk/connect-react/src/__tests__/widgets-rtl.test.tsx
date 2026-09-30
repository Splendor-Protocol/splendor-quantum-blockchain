/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode, useEffect } from 'react';
import { render, screen, fireEvent, within, waitFor, cleanup } from '@testing-library/react';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { AccountCard } from '../widgets/AccountCard.js';
import { AccountModal } from '../ui/AccountModal.js';
import { RECENT_RECIPIENTS_KEY } from '../widgets/utils.js';
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

  it('warns when the wallet is on another chain and offers to follow it', () => {
    cleanup();
    const switchNetwork = vi.fn(async () => undefined);
    const ctrl = makeController(makeState({ walletChainId: 'splendor-quantum-testnet-1' }));
    const { container } = render(React.createElement(WrongNetworkBanner), {
      wrapper: providerWrap(ctrl, { networkId: 'local', switchNetwork }),
    });
    const alert = within(container).getByRole('alert');
    expect(alert.getAttribute('data-reason')).toBe('wallet-network');
    expect(alert.textContent).toMatch(/wallet is on Splendor Quantum Testnet/);
    expect(alert.textContent).toMatch(/this app is on Local/);
    fireEvent.click(within(container).getByRole('button', { name: /Use Splendor Quantum Testnet/ }));
    expect(switchNetwork).toHaveBeenCalledWith('testnet');
  });

  it('with a pinned network only asks to switch the wallet', () => {
    cleanup();
    const ctrl = makeController(makeState({ walletChainId: 'splendor-quantum-testnet-1' }));
    const { container } = render(
      React.createElement(WrongNetworkBanner, { expectedNetworkId: 'local' }),
      { wrapper: providerWrap(ctrl, { networkId: 'local' }) },
    );
    const alert = within(container).getByRole('alert');
    expect(alert.textContent).toMatch(/Switch the network in\s+your Splendor wallet/);
    expect(within(container).queryByRole('button', { name: /^Use / })).toBeNull();
  });

  it('stays hidden when the wallet chain matches or the wallet is disconnected', () => {
    cleanup();
    const match = makeController(makeState({ walletChainId: 'splendor-quantum-1' }));
    const a = render(React.createElement(WrongNetworkBanner), { wrapper: providerWrap(match) });
    expect(a.container.querySelector('.sqc-wrong-network')).toBeNull();
    const gone = makeController(
      makeState({ walletChainId: 'splendor-quantum-testnet-1', status: 'disconnected', account: null }),
    );
    const b = render(React.createElement(WrongNetworkBanner), { wrapper: providerWrap(gone) });
    expect(b.container.querySelector('.sqc-wrong-network')).toBeNull();
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

const OTHER = 'SQ:' + 'cd'.repeat(32);

function makeFundedController(
  state: ConnectControllerState,
  opts: {
    balanceSPLD?: string;
    sendTransfer?: ReturnType<typeof vi.fn>;
    call?: ReturnType<typeof vi.fn>;
  } = {},
) {
  const sendTransfer =
    opts.sendTransfer ??
    vi.fn(async () => ({ status: 'ok', hash: 'h1', confirmation: { ok: true } }));
  const wallet = { sendTransfer, call: opts.call ?? vi.fn() };
  const provider = {
    getBalance: vi.fn(async () => ({
      balance: '0',
      balance_spld: opts.balanceSPLD ?? '1.5',
    })),
  };
  const ctrl = {
    ...makeController(state),
    getWallet: () => wallet,
    getProvider: () => provider,
    isStub: false,
  } as unknown as ConnectController;
  return { ctrl, sendTransfer, provider };
}

async function renderTransfer(opts: Parameters<typeof makeFundedController>[1] = {}) {
  const funded = makeFundedController(makeState(), opts);
  const view = render(React.createElement(TransferWidget, { defaultAmount: '' }), {
    wrapper: providerWrap(funded.ctrl),
  });
  const root = within(view.container);
  await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
  return { ...funded, root };
}

function fill(root: ReturnType<typeof within>, to: string, amount: string) {
  fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: to } });
  fireEvent.change(root.getByLabelText(/Amount \(SPLD\)/i), { target: { value: amount } });
}

const reviewBtn = (root: ReturnType<typeof within>) =>
  root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement;

describe('TransferWidget send safety', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => cleanup());

  it.each([
    ['EVM 0x', '0x' + 'ab'.repeat(20)],
    ['raw hex', 'cd'.repeat(32)],
    ['short', 'SQ:abc'],
  ])('rejects %s recipient with an inline error', async (_label, to) => {
    const { root } = await renderTransfer();
    fill(root, to, '0.1');
    expect(root.getByText(/valid SQ: address/i)).toBeTruthy();
    expect(reviewBtn(root).disabled).toBe(true);
  });

  it('blocks sending to your own address', async () => {
    const { root } = await renderTransfer();
    fill(root, 'SQ:' + 'AB'.repeat(32), '0.1');
    expect(root.getByText(/own address/i)).toBeTruthy();
    expect(reviewBtn(root).disabled).toBe(true);
  });

  it('blocks amounts above the balance', async () => {
    const { root } = await renderTransfer();
    fill(root, OTHER, '1.50001');
    expect(root.getByText(/exceeds your SPLD balance/i)).toBeTruthy();
    expect(reviewBtn(root).disabled).toBe(true);
  });

  it('Max fills the displayed balance', async () => {
    const { root } = await renderTransfer();
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    fireEvent.click(root.getByRole('button', { name: /^Max$/i }));
    expect((root.getByLabelText(/Amount \(SPLD\)/i) as HTMLInputElement).value).toBe('1.5');
    expect(reviewBtn(root).disabled).toBe(false);
  });

  it('shows a review step and only sends on confirm', async () => {
    const { root, sendTransfer } = await renderTransfer();
    fill(root, 'SQ:' + 'CD'.repeat(32), '0.25');
    fireEvent.click(reviewBtn(root));

    expect(root.getByTestId('sqc-review-to').textContent).toBe(OTHER);
    expect(root.getByTestId('sqc-review-amount').textContent).toBe('0.25 SPLD');
    expect(root.getByText('Local')).toBeTruthy();
    expect(sendTransfer).not.toHaveBeenCalled();

    fireEvent.click(root.getByRole('button', { name: /Confirm send/i }));
    await waitFor(() => expect(root.getByText(/Confirmed: h1/)).toBeTruthy());
    expect(sendTransfer).toHaveBeenCalledWith({
      to: OTHER,
      amountSPLD: '0.25',
      waitForConfirmation: true,
    });
  });

  it('Back returns to the form without sending', async () => {
    const { root, sendTransfer } = await renderTransfer();
    fill(root, OTHER, '0.25');
    fireEvent.click(reviewBtn(root));
    fireEvent.click(root.getByRole('button', { name: /^Back$/i }));
    expect(root.getByLabelText(/^To \(SQ/i)).toBeTruthy();
    expect(sendTransfer).not.toHaveBeenCalled();
  });

  it('remembers the recipient after success and lets you pick it again', async () => {
    const { root } = await renderTransfer();
    fill(root, OTHER, '0.25');
    fireEvent.click(reviewBtn(root));
    fireEvent.click(root.getByRole('button', { name: /Confirm send/i }));
    await waitFor(() => expect(root.getByText(/Confirmed: h1/)).toBeTruthy());
    expect(JSON.parse(localStorage.getItem(RECENT_RECIPIENTS_KEY) ?? '[]')).toEqual([OTHER]);

    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: '' } });
    const group = root.getByLabelText(/Recent recipients/i);
    fireEvent.click(within(group).getByTitle(OTHER));
    expect((root.getByLabelText(/^To \(SQ/i) as HTMLInputElement).value).toBe(OTHER);
  });

  it('does not remember the recipient when the send fails', async () => {
    const sendTransfer = vi.fn(async () => {
      throw new Error('rejected by node');
    });
    const { root } = await renderTransfer({ sendTransfer });
    fill(root, OTHER, '0.25');
    fireEvent.click(reviewBtn(root));
    fireEvent.click(root.getByRole('button', { name: /Confirm send/i }));
    await waitFor(() => expect(sendTransfer).toHaveBeenCalled());
    await waitFor(() =>
      expect(document.querySelector('.sqc-widget__msg--error')).toBeTruthy(),
    );
    expect(localStorage.getItem(RECENT_RECIPIENTS_KEY)).toBeNull();
    expect(root.getByTestId('sqc-review-to').textContent).toBe(OTHER);
  });

  it('loads recent recipients from storage on mount', async () => {
    localStorage.setItem(RECENT_RECIPIENTS_KEY, JSON.stringify([OTHER, 'not-an-address']));
    const { root } = await renderTransfer();
    const group = root.getByLabelText(/Recent recipients/i);
    expect(within(group).getAllByRole('button')).toHaveLength(1);
  });
});

describe('TokenPanel send safety', () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => cleanup());

  it('rejects 0x recipient and self-send', () => {
    const { ctrl } = makeFundedController(makeState());
    const view = render(React.createElement(TokenPanel, { tokenAddress: OTHER }), {
      wrapper: providerWrap(ctrl),
    });
    const root = within(view.container);
    const to = root.getByLabelText(/^To \(SQ/i);
    fireEvent.change(to, { target: { value: '0x' + 'ab'.repeat(20) } });
    expect(root.getByText(/valid SQ: address/i)).toBeTruthy();
    fireEvent.change(to, { target: { value: ADDR } });
    expect(root.getByText(/own address/i)).toBeTruthy();
    expect(reviewBtn(root).disabled).toBe(true);
  });

  it('requires review before transferring', () => {
    const { ctrl } = makeFundedController(makeState());
    const view = render(React.createElement(TokenPanel, { tokenAddress: OTHER }), {
      wrapper: providerWrap(ctrl),
    });
    const root = within(view.container);
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    fireEvent.change(root.getByLabelText(/Amount \(human/i), { target: { value: '2' } });
    fireEvent.click(reviewBtn(root));
    expect(root.getByTestId('sqc-review-amount').textContent).toBe('2 TOKEN');
    expect(root.getByRole('button', { name: /Confirm send/i })).toBeTruthy();
  });
});

describe('account explorer links', () => {
  afterEach(() => cleanup());

  it('AccountCard links to the SQ address page', () => {
    const { ctrl } = makeFundedController(makeState());
    const view = render(React.createElement(AccountCard), { wrapper: providerWrap(ctrl) });
    const link = within(view.container).getByRole('link', { name: /Explorer/i });
    expect(link.getAttribute('href')).toBe(`https://explorer.example/address/${ADDR}`);
    expect(link.getAttribute('rel')).toBe('noreferrer');
  });

  it('AccountCard hides the link when the network has no explorer', () => {
    const state = makeState();
    const { ctrl } = makeFundedController({
      ...state,
      network: { ...state.network!, explorerUrl: '' },
    } as ConnectControllerState);
    const view = render(React.createElement(AccountCard), { wrapper: providerWrap(ctrl) });
    expect(within(view.container).queryByRole('link', { name: /Explorer/i })).toBeNull();
  });

  it('AccountModal links to the SQ address page', () => {
    const { ctrl } = makeFundedController(makeState({ modal: 'account' }));
    render(React.createElement(AccountModal), { wrapper: providerWrap(ctrl) });
    const link = screen.getByRole('link', { name: /Open explorer/i });
    expect(link.getAttribute('href')).toBe(`https://explorer.example/address/${ADDR}`);
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
