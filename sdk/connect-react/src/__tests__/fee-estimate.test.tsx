/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { cleanup, fireEvent, render, renderHook, waitFor, within } from '@testing-library/react';
import { useEstimateFee } from '../react/hooks.js';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { FeeRow } from '../widgets/parts/FeeRow.js';
import { createMockConnectController, mockWallet, MockSplendorProvider, MOCK_ADDRESS } from '../testing.js';

const TO = 'SQ:' + 'cd'.repeat(32);

afterEach(() => cleanup());

function wrap(estimate: Parameters<typeof mockWallet>[0]['estimate']) {
  const wallet = mockWallet({ estimate });
  const ctrl = createMockConnectController({ wallet, balance: '10' });
  const Wrap = ({ children }: { children: ReactNode }) =>
    React.createElement(MockSplendorProvider, { controller: ctrl }, children);
  return { ctrl, wallet, Wrap };
}

describe('useEstimateFee', () => {
  it('returns the fee in SPLD and the floor flag', async () => {
    const { Wrap } = wrap({ fee: '2000000000000000', fee_is_floor: false, gas: 21000 });
    const { result } = renderHook(() => useEstimateFee({ from: MOCK_ADDRESS, to: TO, value: '1' }, { debounceMs: 0 }), {
      wrapper: Wrap,
    });
    await waitFor(() => expect(result.current.feeSpld).toBe('0.002'));
    expect(result.current.feeIsFloor).toBe(false);
    expect(result.current.gas).toBe(21000);
    expect(result.current.reverted).toBe(false);
  });

  it('reports a predicted revert with its reason', async () => {
    const { Wrap } = wrap({ fee: '1000', reverted: true, revert_reason: 'insufficient balance' });
    const { result } = renderHook(() => useEstimateFee({ from: MOCK_ADDRESS, to: TO }, { debounceMs: 0 }), {
      wrapper: Wrap,
    });
    await waitFor(() => expect(result.current.reverted).toBe(true));
    expect(result.current.revertReason).toBe('insufficient balance');
  });

  it('treats an empty node response as an error instead of loading forever', async () => {
    const { Wrap, wallet } = wrap(undefined);
    (wallet.connection as { estimateGas: unknown }).estimateGas = async () => null;
    const { result } = renderHook(() => useEstimateFee({ from: MOCK_ADDRESS, to: TO, value: '1' }, { debounceMs: 0 }), {
      wrapper: Wrap,
    });
    await waitFor(() => expect(result.current.error).toBeTruthy());
    expect(result.current.loading).toBe(false);
    expect(result.current.fee).toBeNull();
  });

  it('does nothing for a null request', () => {
    const { Wrap, wallet } = wrap(undefined);
    const { result } = renderHook(() => useEstimateFee(null, { debounceMs: 0 }), { wrapper: Wrap });
    expect(result.current.loading).toBe(false);
    expect(result.current.fee).toBeNull();
    expect(wallet.calls.some((c) => c.method === 'connection.estimateGas')).toBe(false);
  });
});

describe('FeeRow', () => {
  it('shows floor, estimating and unavailable states', () => {
    const base = { feeSpld: null, feeIsFloor: false, reverted: false, revertReason: null, loading: false, error: null };
    const a = render(React.createElement(FeeRow, { estimate: { ...base, loading: true } }));
    expect(a.getByTestId('sqc-review-fee').textContent).toBe('Estimating…');
    cleanup();
    const b = render(React.createElement(FeeRow, { estimate: { ...base, feeSpld: '0.001', feeIsFloor: true } }));
    expect(b.getByTestId('sqc-review-fee').textContent).toBe('at least 0.001 SPLD');
    cleanup();
    const c = render(React.createElement(FeeRow, { estimate: base }));
    expect(c.getByTestId('sqc-review-fee').textContent).toBe('Unavailable');
  });
});

describe('TransferWidget fee row', () => {
  async function toReview(estimate: Parameters<typeof mockWallet>[0]['estimate']) {
    const ctx = wrap(estimate);
    const view = render(React.createElement(TransferWidget, { defaultTo: TO, defaultAmount: '1' }), {
      wrapper: ctx.Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 10 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    return { ...ctx, root };
  }

  it('shows the fee in the review step', async () => {
    const { root } = await toReview({ fee: '1000000000000000', fee_is_floor: true });
    await waitFor(() => expect(root.getByTestId('sqc-review-fee').textContent).toBe('at least 0.001 SPLD'));
    expect((root.getByRole('button', { name: /Confirm send/i }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('blocks Confirm when the node predicts a revert', async () => {
    const { root, wallet } = await toReview({ fee: '1000', reverted: true, revert_reason: 'nope' });
    await waitFor(() => expect(root.getByTestId('sqc-review-revert').textContent).toMatch(/nope/));
    const confirm = root.getByRole('button', { name: /Confirm send/i }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(confirm);
    expect(wallet.calls.some((c) => c.method === 'sendTransfer')).toBe(false);
  });

  it('showFee={false} hides the row', async () => {
    const ctx = wrap({ fee: '1000' });
    const view = render(React.createElement(TransferWidget, { defaultTo: TO, defaultAmount: '1', showFee: false }), {
      wrapper: ctx.Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 10 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    expect(root.queryByTestId('sqc-review-fee')).toBeNull();
  });
});
