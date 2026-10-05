/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { createMockConnectController, MockSplendorProvider } from '../testing.js';

const TOKEN_A = 'SQ:' + '11'.repeat(32);
const TOKEN_B = 'SQ:' + '22'.repeat(32);
const TO = 'SQ:' + 'cd'.repeat(32);

const decimalsByToken: Record<string, number> = {};

vi.mock('@splendor/sqc-sdk/tokens', () => ({
  QRC20: class {
    constructor(
      _conn: unknown,
      readonly address: string,
    ) {}
    pinDecimals() {
      return this;
    }
    balanceOf() {
      return Promise.resolve('5000000000000000000');
    }
    tokenInfo() {
      return Promise.resolve({ decimals: decimalsByToken[this.address] ?? 18, symbol: 'TKN' });
    }
    transferHuman() {
      return Promise.resolve({ status: 'ok', hash: 'ef'.repeat(32) });
    }
  },
}));

function renderPanel(props: Record<string, unknown>) {
  const ctrl = createMockConnectController();
  const view = render(
    React.createElement(MockSplendorProvider, { controller: ctrl }, React.createElement(TokenPanel, { showFee: false, ...props })),
  );
  return { ctrl, view, root: within(view.container) };
}

beforeEach(() => localStorage.clear());
afterEach(() => cleanup());

describe('TokenPanel review integrity', () => {
  it('hides the token field while reviewing and shows the contract being called', async () => {
    const { root } = renderPanel({});
    fireEvent.change(root.getByLabelText(/^Token \(SQ/i), { target: { value: TOKEN_A } });
    await waitFor(() => expect(root.getByText(/Balance: 5/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: TO } });
    fireEvent.change(root.getByLabelText(/Amount/i), { target: { value: '1' } });
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));

    expect(root.queryByLabelText(/^Token \(SQ/i)).toBeNull();
    expect(root.getByText(TOKEN_A.slice(0, 10), { exact: false })).toBeTruthy();
    expect(root.getByRole('button', { name: /Confirm send/i })).toBeTruthy();
  });

  it('closes an open review when the token changes', async () => {
    const { view, root, ctrl } = renderPanel({ tokenAddress: TOKEN_A });
    await waitFor(() => expect(root.getByText(/Balance: 5/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: TO } });
    fireEvent.change(root.getByLabelText(/Amount/i), { target: { value: '1' } });
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    expect(root.getByRole('button', { name: /Confirm send/i })).toBeTruthy();

    view.rerender(
      React.createElement(MockSplendorProvider, { controller: ctrl }, React.createElement(TokenPanel, { showFee: false, tokenAddress: TOKEN_B })),
    );
    expect(root.queryByRole('button', { name: /Confirm send/i })).toBeNull();
  });

  it('enforces the token decimals on the amount', async () => {
    decimalsByToken[TOKEN_B] = 2;
    const { root } = renderPanel({ tokenAddress: TOKEN_B });
    await waitFor(() => expect(root.getByText(/Balance:/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: TO } });
    fireEvent.change(root.getByLabelText(/Amount/i), { target: { value: '0.001' } });
    expect((root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(root.getByLabelText(/Amount/i), { target: { value: '0.01' } });
    expect((root.getByRole('button', { name: /^Review$/i }) as HTMLButtonElement).disabled).toBe(false);
  });
});
