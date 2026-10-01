/**
 * @vitest-environment jsdom
 */
import { beforeEach, describe, expect, it } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, renderHook } from '@testing-library/react';
import { useTransferForm, type UseTransferFormOptions } from '../widgets/parts/useTransferForm.js';
import { BrandContext } from '../react/context.js';

const SELF = 'SQ:' + 'ab'.repeat(32);
const OTHER = 'SQ:' + 'cd'.repeat(32);

const hook = (opts: UseTransferFormOptions = {}, wrapper?: ({ children }: { children: ReactNode }) => ReactNode) =>
  renderHook(() => useTransferForm({ balance: '2', self: SELF, ...opts }), wrapper ? { wrapper } : undefined);

describe('useTransferForm', () => {
  beforeEach(() => localStorage.clear());

  it('starts empty with no errors and cannot review', () => {
    const { result } = hook();
    expect(result.current.to).toBe('');
    expect(result.current.errors).toEqual({ to: null, amount: null });
    expect(result.current.canReview).toBe(false);
  });

  it('accepts a valid recipient and amount', () => {
    const { result } = hook({ defaultTo: OTHER, defaultAmount: '0.5' });
    expect(result.current.canReview).toBe(true);
    expect(result.current.review).toMatchObject({ ok: true, to: OTHER, amount: '0.5' });
  });

  it('flags 0x addresses, self-sends and amounts above the balance', () => {
    const { result } = hook();
    act(() => result.current.setTo('0x' + '11'.repeat(20)));
    expect(result.current.errors.to).toMatch(/valid SQ: address/);
    act(() => result.current.setTo(SELF));
    expect(result.current.errors.to).toMatch(/own address/);
    act(() => {
      result.current.setTo(OTHER);
      result.current.setAmount('3');
    });
    expect(result.current.errors.amount).toBe('Amount exceeds your SPLD balance');
    expect(result.current.canReview).toBe(false);
  });

  it('uses the symbol in the balance error', () => {
    const { result } = hook({ symbol: 'LAB', defaultTo: OTHER, defaultAmount: '9' });
    expect(result.current.errors.amount).toBe('Amount exceeds your LAB balance');
  });

  it('setMax fills the balance, and does nothing while the balance is unknown', () => {
    const known = hook();
    act(() => known.result.current.setMax());
    expect(known.result.current.amount).toBe('2');
    const unknown = hook({ balance: null, defaultAmount: '1' });
    act(() => unknown.result.current.setMax());
    expect(unknown.result.current.amount).toBe('1');
  });

  it('review starts only when valid and ends when a field changes', () => {
    const { result } = hook({ defaultTo: OTHER, defaultAmount: '1' });
    act(() => result.current.startReview());
    expect(result.current.reviewing).toBe(true);
    act(() => result.current.setAmount('1.5'));
    expect(result.current.reviewing).toBe(false);
    act(() => result.current.setTo('bad'));
    act(() => result.current.startReview());
    expect(result.current.reviewing).toBe(false);
  });

  it('remember stores the recipient in recents, and reset restores the defaults', () => {
    const { result } = hook({ defaultTo: '', defaultAmount: '0.1' });
    act(() => result.current.remember(OTHER));
    expect(result.current.recents[0]).toBe(OTHER);
    act(() => {
      result.current.setTo(OTHER);
      result.current.setAmount('1');
    });
    act(() => result.current.reset());
    expect(result.current.to).toBe('');
    expect(result.current.amount).toBe('0.1');
  });

  it('error text follows provider labels and the labels option', () => {
    const wrapper = ({ children }: { children: ReactNode }) =>
      React.createElement(
        BrandContext.Provider,
        { value: { widgets: { common: { selfSend: 'Not to yourself' } } } },
        children,
      );
    const fromProvider = hook({ defaultTo: SELF }, wrapper);
    expect(fromProvider.result.current.errors.to).toBe('Not to yourself');
    const fromOption = hook({ defaultTo: SELF, labels: { selfSend: 'Pick someone else' } }, wrapper);
    expect(fromOption.result.current.errors.to).toBe('Pick someone else');
  });
});
