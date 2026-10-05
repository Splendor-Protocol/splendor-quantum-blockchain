/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it } from 'vitest';
import React from 'react';
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { AccountCard } from '../widgets/AccountCard.js';
import { TransferWidget } from '../widgets/TransferWidget.js';
import {
  createMockConnectController,
  mockConnector,
  mockWallet,
  MockSplendorProvider,
  MOCK_ADDRESS,
} from '../testing.js';

const TO = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

describe('@splendor/connect-react/testing', () => {
  it('drives a widget end to end and records the wallet call', async () => {
    const ctrl = createMockConnectController({ balance: '3' });
    const view = render(
      React.createElement(
        MockSplendorProvider,
        { controller: ctrl },
        React.createElement(TransferWidget, { defaultTo: TO, defaultAmount: '1', showFee: false }),
      ),
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 3 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    fireEvent.click(root.getByRole('button', { name: /Confirm send/i }));
    await waitFor(() => expect(ctrl.wallet.calls.some((c) => c.method === 'sendTransfer')).toBe(true));
    const call = ctrl.wallet.calls.find((c) => c.method === 'sendTransfer')!;
    expect(call.args[0]).toMatchObject({ to: TO, amountSPLD: '1' });
  });

  it('fail makes a method reject so error paths can be tested', async () => {
    const wallet = mockWallet({ fail: { sendTransfer: new Error('User rejected transaction') } });
    const ctrl = createMockConnectController({ wallet, balance: '3' });
    const view = render(
      React.createElement(
        MockSplendorProvider,
        { controller: ctrl },
        React.createElement(TransferWidget, { defaultTo: TO, defaultAmount: '1', showFee: false }),
      ),
    );
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 3 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    fireEvent.click(root.getByRole('button', { name: /Confirm send/i }));
    await waitFor(() => expect(view.container.querySelector('.sqc-widget__msg--error')).toBeTruthy());
  });

  it('connect / disconnect through the mock connector updates the UI', async () => {
    const ctrl = createMockConnectController({ status: 'disconnected', connectors: [mockConnector({ id: 'm' })] });
    const view = render(React.createElement(MockSplendorProvider, { controller: ctrl }, React.createElement(AccountCard)));
    expect(view.container.querySelector('[data-sqc-gate="disconnected"]')).toBeTruthy();
    await act(async () => {
      await ctrl.connect('m');
    });
    expect(ctrl.getState().account?.address).toBe(MOCK_ADDRESS);
    expect(view.container.querySelector('[data-sqc-gate="disconnected"]')).toBeNull();
    await act(async () => {
      await ctrl.disconnect();
    });
    expect(view.container.querySelector('[data-sqc-gate="disconnected"]')).toBeTruthy();
  });

  it('setState and rebindNetwork notify subscribers', async () => {
    const ctrl = createMockConnectController({ network: 'local' });
    let notified = 0;
    ctrl.subscribe(() => {
      notified += 1;
    });
    ctrl.setState({ walletChainId: 'other' });
    await ctrl.rebindNetwork('testnet');
    expect(notified).toBe(2);
    expect(ctrl.getNetwork().id).toBe('testnet');
  });

  it('a failing connector puts the controller in error', async () => {
    const ctrl = createMockConnectController({
      status: 'disconnected',
      connectors: [mockConnector({ id: 'bad', fail: new Error('nope') })],
    });
    await expect(ctrl.connect('bad')).rejects.toThrow('nope');
    expect(ctrl.getState().status).toBe('error');
  });
});
