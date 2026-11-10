/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConnectKit } from '../ui/ConnectKit.js';
import { NetworkBadge } from '../ui/NetworkBadge.js';
import { ModalHost } from '../ui/ModalHost.js';
import { WalletGate } from '../widgets/parts/WalletGate.js';
import { WidgetShell } from '../widgets/parts/WidgetShell.js';
import { ReviewPanel } from '../widgets/parts/ReviewPanel.js';
import { CopyButton } from '../widgets/parts/AddressDisplay.js';
import { TxResult } from '../widgets/parts/TxResult.js';
import { ActivityFeed } from '../widgets/ActivityFeed.js';
import { MOCK_ADDRESS, MockSplendorProvider, createMockConnectController, type MockConnectController } from '../testing.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const mount = (ctrl: MockConnectController, ui: React.ReactElement) =>
  render(<MockSplendorProvider controller={ctrl}>{ui}</MockSplendorProvider>);

describe('ConnectKit', () => {
  it('disconnected: shows the network badge and a Connect button that opens the connect modal', () => {
    const ctrl = createMockConnectController({ status: 'disconnected' });
    mount(ctrl, <ConnectKit connectLabel="Sign in with Splendor" />);
    expect(document.querySelector('.sqc-network-badge')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with Splendor' }));
    expect(ctrl.getState().modal).toBe('connect');
  });

  it('showNetwork={false} hides the badge', () => {
    mount(createMockConnectController({ status: 'disconnected' }), <ConnectKit showNetwork={false} />);
    expect(document.querySelector('.sqc-network-badge')).toBeNull();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeTruthy();
  });

  it('connected: shows the account chip with balance; clicking it opens the account modal', async () => {
    const ctrl = createMockConnectController({ balance: '7.5' });
    mount(ctrl, <ConnectKit />);
    const chip = await screen.findByRole('button', { name: 'Account' });
    await waitFor(() => expect(within(chip).getByText('7.5 SPLD')).toBeTruthy());
    expect(chip.textContent).toContain(MOCK_ADDRESS.slice(0, 6));
    fireEvent.click(chip);
    expect(ctrl.getState().modal).toBe('account');
  });

  it('locked: offers Unlock wallet', () => {
    const ctrl = createMockConnectController({ status: 'locked' });
    mount(ctrl, <ConnectKit showNetwork={false} />);
    fireEvent.click(screen.getByRole('button', { name: 'Unlock wallet' }));
    expect(ctrl.getState().modal).toBe('connect');
  });
});

describe('NetworkBadge', () => {
  it('opens a listbox, moves with the arrow keys and switches on Enter', async () => {
    const ctrl = createMockConnectController({ network: 'local' });
    mount(ctrl, <NetworkBadge />);
    const badge = document.querySelector<HTMLButtonElement>('.sqc-network-badge')!;
    expect(badge.disabled).toBe(false);
    fireEvent.click(badge);
    const list = screen.getByRole('listbox');
    const options = within(list).getAllByRole('option');
    expect(options.length).toBeGreaterThan(1);
    expect(badge.getAttribute('aria-expanded')).toBe('true');
    const currentIndex = options.findIndex((o) => o.getAttribute('aria-selected') === 'true');
    // Move off the current network, then pick.
    for (let i = 0; i <= currentIndex; i++) fireEvent.keyDown(document, { key: 'ArrowDown' });
    await act(async () => {
      fireEvent.keyDown(document, { key: 'Enter' });
    });
    await waitFor(() => expect(ctrl.getState().network?.id).not.toBe('local'));
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('Escape closes the menu without switching', () => {
    const ctrl = createMockConnectController({ network: 'local' });
    mount(ctrl, <NetworkBadge />);
    const badge = document.querySelector<HTMLButtonElement>('.sqc-network-badge')!;
    expect(badge.disabled).toBe(false);
    fireEvent.click(badge);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(ctrl.getState().network?.id).toBe('local');
  });

  it('switchable={false} renders a disabled label', () => {
    mount(createMockConnectController(), <NetworkBadge switchable={false} />);
    const badge = document.querySelector<HTMLButtonElement>('.sqc-network-badge')!;
    expect(badge.disabled).toBe(true);
    expect(badge.getAttribute('aria-haspopup')).toBeNull();
  });
});

describe('ModalHost', () => {
  it('renders nothing while every modal is closed', () => {
    mount(createMockConnectController({ status: 'disconnected' }), <ModalHost />);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows the connect dialog, keeps focus inside it and closes on Escape', async () => {
    const ctrl = createMockConnectController({ status: 'disconnected' });
    mount(ctrl, <ModalHost />);
    act(() => ctrl.openModal('connect'));
    const dialog = await screen.findByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    fireEvent.keyDown(document.activeElement ?? dialog, { key: 'Escape' });
    await waitFor(() => expect(ctrl.getState().modal).toBe('closed'));
  });

  it('shows the account dialog for a connected wallet', async () => {
    const ctrl = createMockConnectController();
    mount(ctrl, <ModalHost />);
    act(() => ctrl.openModal('account'));
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain(MOCK_ADDRESS.slice(0, 6));
  });
});

describe('WalletGate', () => {
  it('renders children only when connected', () => {
    mount(createMockConnectController(), <WalletGate>secret content</WalletGate>);
    expect(screen.getByText('secret content')).toBeTruthy();
  });

  it('disconnected: a Connect prompt that opens the modal', () => {
    const ctrl = createMockConnectController({ status: 'disconnected' });
    mount(ctrl, <WalletGate disconnectedText="Connect first">secret content</WalletGate>);
    expect(screen.queryByText('secret content')).toBeNull();
    expect(screen.getByText('Connect first')).toBeTruthy();
    expect(document.querySelector('[data-sqc-gate="disconnected"]')).not.toBeNull();
    fireEvent.click(screen.getByRole('button'));
    expect(ctrl.getState().modal).toBe('connect');
  });

  it('locked: an Unlock prompt; fallback replaces the prompt', () => {
    const ctrl = createMockConnectController({ status: 'locked' });
    const { rerender } = mount(ctrl, <WalletGate>secret content</WalletGate>);
    expect(document.querySelector('[data-sqc-gate="locked"]')).not.toBeNull();
    rerender(
      <MockSplendorProvider controller={ctrl}>
        <WalletGate fallback={<em>custom</em>}>secret content</WalletGate>
      </MockSplendorProvider>,
    );
    expect(screen.getByText('custom')).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe('WidgetShell', () => {
  it('sets the data attributes and only shows headerExtra while connected', () => {
    mount(
      createMockConnectController({ status: 'disconnected' }),
      <WidgetShell kind="demo" status="pending" title="Demo" headerExtra={<span>extra</span>}>
        body
      </WidgetShell>,
    );
    const root = document.querySelector('[data-sqc-widget="demo"]')!;
    expect(root.getAttribute('data-status')).toBe('pending');
    expect(root.classList.contains('sqc-widget--demo')).toBe(true);
    expect(screen.getByRole('heading', { name: 'Demo' })).toBeTruthy();
    expect(screen.queryByText('extra')).toBeNull();
  });

  it('disables every control while the wallet is on another chain', () => {
    const ctrl = createMockConnectController({ walletChainId: 'some-other-chain' });
    mount(
      ctrl,
      <WidgetShell kind="demo">
        <button type="button">Send</button>
      </WidgetShell>,
    );
    expect(screen.getByTestId('sqc-wrong-network')).toBeTruthy();
    const fieldset = document.querySelector('fieldset')!;
    expect(fieldset.disabled).toBe(true);
  });

  it('gate={false} renders the body even when disconnected', () => {
    mount(createMockConnectController({ status: 'disconnected' }), <WidgetShell kind="demo" gate={false}>open body</WidgetShell>);
    expect(screen.getByText('open body')).toBeTruthy();
  });
});

describe('ReviewPanel', () => {
  it('lists the rows and wires Back / Confirm', () => {
    const onBack = vi.fn();
    const onConfirm = vi.fn();
    render(
      <ReviewPanel to={MOCK_ADDRESS} amount="1.5" symbol="SPLD" networkName="Local" pending={false} onBack={onBack} onConfirm={onConfirm} />,
    );
    expect(screen.getByTestId('sqc-review-to').textContent).toBe(MOCK_ADDRESS);
    expect(screen.getByTestId('sqc-review-amount').textContent).toBe('1.5 SPLD');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    fireEvent.click(screen.getByRole('button', { name: /Confirm/ }));
    expect(onBack).toHaveBeenCalledOnce();
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('pending disables both buttons; confirmDisabled disables only Confirm', () => {
    const props = { to: MOCK_ADDRESS, amount: '1', onBack: () => {}, onConfirm: () => {} };
    const { rerender } = render(<ReviewPanel {...props} pending />);
    for (const b of screen.getAllByRole('button')) expect((b as HTMLButtonElement).disabled).toBe(true);
    rerender(<ReviewPanel {...props} pending={false} confirmDisabled />);
    expect((screen.getByRole('button', { name: 'Back' }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole('button', { name: /Confirm/ }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('CopyButton and TxResult', () => {
  it('CopyButton copies, flips to Copied and reports the text', async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    const onCopied = vi.fn();
    render(<CopyButton text="hello" onCopied={onCopied} />);
    fireEvent.click(screen.getByRole('button', { name: /copy/i }));
    await waitFor(() => expect(screen.getByRole('button').textContent).toMatch(/copied/i));
    expect(writeText).toHaveBeenCalledWith('hello');
    expect(onCopied).toHaveBeenCalledWith('hello');
  });

  it('TxResult says Submitted or Confirmed and shows the extra row', () => {
    const hash = 'ab'.repeat(32);
    const { rerender } = render(<TxResult hash={hash} />);
    expect(screen.getByRole('status').textContent).toMatch(/Submitted/);
    rerender(<TxResult hash={hash} confirmed extra={{ label: 'Token', value: MOCK_ADDRESS }} />);
    expect(screen.getByRole('status').textContent).toMatch(/Confirmed/);
    expect(screen.getByRole('status').textContent).toMatch(/Token:/);
  });

  it('TxResult renders nothing without a hash or extra row', () => {
    const { container } = render(<TxResult />);
    expect(container.innerHTML).toBe('');
  });
});

describe('ActivityFeed refresh', () => {
  it('keeps its accessible name while loading', async () => {
    mount(createMockConnectController(), <ActivityFeed />);
    const refresh = await screen.findByRole('button', { name: 'Refresh' });
    expect(refresh.getAttribute('aria-label')).toBe('Refresh');
    fireEvent.click(refresh);
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
  });
});
