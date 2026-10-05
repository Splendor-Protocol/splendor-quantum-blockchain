/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, render, renderHook, waitFor } from '@testing-library/react';
import {
  PENDING_TX_KEY,
  PENDING_TX_MAX,
  __resetPendingTxCache,
  addPendingTx,
  isConfirmedResult,
  loadPendingTxs,
  receiptOutcome,
  removePendingTx,
} from '../core/pendingTx.js';
import { usePendingTransactions, useSendTransfer } from '../react/hooks.js';
import { PendingTxList } from '../widgets/parts/PendingTxList.js';
import { TxToastHost } from '../ui/TxToastHost.js';
import { createMockConnectController, mockWallet, MockSplendorProvider } from '../testing.js';

const H1 = 'a1'.repeat(32);
const H2 = 'b2'.repeat(32);
const TO = 'SQ:' + 'cd'.repeat(32);

beforeEach(() => {
  localStorage.clear();
  __resetPendingTxCache();
});
afterEach(() => cleanup());

describe('pending tx store', () => {
  it('validates entries on load and drops junk, stale and duplicate rows', () => {
    const now = Date.now();
    localStorage.setItem(
      PENDING_TX_KEY,
      JSON.stringify([
        { hash: H1, kind: 'transfer', summary: 'ok', network: 'testnet', ts: now },
        { hash: H1, kind: 'transfer', summary: 'dup', network: 'testnet', ts: now },
        { hash: 'nothex', kind: 'x', summary: 's', network: 'testnet', ts: now },
        { hash: H2, kind: 'x', summary: 'bad\u0007text', network: 'testnet', ts: now },
        { hash: H2, kind: 'x', summary: 'old', network: 'testnet', ts: now - 3 * 24 * 3600_000 },
        'garbage',
      ]),
    );
    const list = loadPendingTxs();
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ hash: H1, summary: 'ok' });
  });

  it('caps the list and keeps the newest first', () => {
    for (let i = 0; i < PENDING_TX_MAX + 5; i++) {
      addPendingTx({ hash: i.toString(16).padStart(64, '0'), kind: 't', summary: `n${i}`, network: 'testnet' });
    }
    const list = loadPendingTxs();
    expect(list).toHaveLength(PENDING_TX_MAX);
    expect(list[0]!.summary).toBe(`n${PENDING_TX_MAX + 4}`);
    removePendingTx(list[0]!.hash);
    expect(loadPendingTxs()).toHaveLength(PENDING_TX_MAX - 1);
  });

  it('interprets receipts and confirmed results', () => {
    expect(receiptOutcome({ status: 1 })).toBe('success');
    expect(receiptOutcome({ status: 'reverted' })).toBe('failed');
    expect(receiptOutcome({ status: 'pending' })).toBeNull();
    expect(receiptOutcome(null)).toBeNull();
    expect(isConfirmedResult({ hash: H1, confirmation: { ok: true } })).toBe(true);
    expect(isConfirmedResult({ hash: H1 })).toBe(false);
  });
});

function setup(receipt: unknown = { status: 'success' }) {
  const wallet = mockWallet();
  wallet.connection.getTxReceipt = async () => receipt;
  wallet.sendTransfer = async () => ({ status: 'ok', hash: H1 });
  const ctrl = createMockConnectController({ wallet, network: 'testnet', balance: '5' });
  const Wrap = ({ children }: { children: ReactNode }) =>
    React.createElement(
      MockSplendorProvider,
      { controller: ctrl },
      React.createElement(TxToastHost, { autoDismissMs: 0 }, children),
    );
  return { ctrl, wallet, Wrap };
}

describe('usePendingTransactions', () => {
  it('records an unconfirmed send and survives a reload', async () => {
    const { Wrap } = setup({ status: 'pending' });
    const { result } = renderHook(() => useSendTransfer(), { wrapper: Wrap });
    await act(async () => {
      await result.current.sendTransfer({ to: TO, amountSPLD: '1' });
    });
    expect(JSON.parse(localStorage.getItem(PENDING_TX_KEY)!)[0]).toMatchObject({
      hash: H1,
      kind: 'transfer',
      network: 'testnet',
    });
    __resetPendingTxCache();
    const { result: after } = renderHook(() => usePendingTransactions({ pollMs: 20 }), { wrapper: Wrap });
    expect(after.current.pending.map((p) => p.hash)).toEqual([H1]);
  });

  it('does not record sends that already confirmed', async () => {
    const { Wrap, wallet } = setup();
    wallet.sendTransfer = async () => ({ status: 'ok', hash: H1, confirmation: { ok: true } });
    const { result } = renderHook(() => useSendTransfer(), { wrapper: Wrap });
    await act(async () => {
      await result.current.sendTransfer({ to: TO, amountSPLD: '1', waitForConfirmation: true });
    });
    expect(localStorage.getItem(PENDING_TX_KEY)).toBeNull();
  });

  it('removes the entry and toasts once the receipt arrives', async () => {
    addPendingTx({ hash: H1, kind: 'transfer', summary: 'Send 1 SPLD', network: 'testnet' });
    addPendingTx({ hash: H2, kind: 'transfer', summary: 'Other chain', network: 'mainnet' });
    const { Wrap } = setup({ status: 'success' });
    const view = render(React.createElement(PendingTxList, { pollMs: 20 }), { wrapper: Wrap });
    await waitFor(() => expect(view.queryAllByTestId('sqc-pending-tx')).toHaveLength(0));
    await waitFor(() => expect(document.querySelector('.sqc-tx-toast--success')?.textContent).toMatch(/Send 1 SPLD/));
    expect(loadPendingTxs().map((p) => p.hash)).toEqual([H2]);
  });

  it('lists entries for the current network only and lets the user stop watching', async () => {
    addPendingTx({ hash: H1, kind: 'transfer', summary: 'Mine', network: 'testnet' });
    addPendingTx({ hash: H2, kind: 'transfer', summary: 'Elsewhere', network: 'mainnet' });
    const { Wrap } = setup({ status: 'pending' });
    const view = render(React.createElement(PendingTxList, { pollMs: 1000 }), { wrapper: Wrap });
    const items = view.getAllByTestId('sqc-pending-tx');
    expect(items).toHaveLength(1);
    expect(items[0]!.textContent).toContain('Mine');
    act(() => {
      (view.getByRole('button', { name: /Stop watching/ }) as HTMLButtonElement).click();
    });
    expect(view.queryAllByTestId('sqc-pending-tx')).toHaveLength(0);
  });
});
