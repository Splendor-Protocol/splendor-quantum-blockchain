/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConnectModal, SEED_CLIPBOARD_CLEAR_MS, SEED_CONFIRM_CHARS, SEED_REVEAL_MS } from '../ui/ConnectModal.js';
import type { Connector } from '../connectors/types.js';
import { createMockConnectController, MockSplendorProvider, MOCK_ADDRESS } from '../testing.js';

const SEED_CONNECTOR = {
  id: 'seed',
  name: 'Seed',
  keyOrigin: 'native',
  postQuantumSecure: true,
  connectFields: [{ name: 'seed', label: 'seed', type: 'password', placeholder: '64 hex' }],
  ready: async () => true,
  connect: async () => ({ address: MOCK_ADDRESS }),
  disconnect: async () => undefined,
} as unknown as Connector;

function setup(connect: (id: string, opts?: Record<string, unknown>) => Promise<unknown>) {
  const ctrl = createMockConnectController({ status: 'disconnected', connectors: [SEED_CONNECTOR] });
  ctrl.setState({ modal: 'connect' });
  const spy = vi.fn(connect);
  ctrl.connect = spy as never;
  render(React.createElement(MockSplendorProvider, { controller: ctrl }, React.createElement(ConnectModal)));
  return { ctrl, spy, dialog: within(document.body) };
}

async function openSeed(dialog: ReturnType<typeof within>) {
  const item = await dialog.findByRole('button', { name: /Seed/ });
  await waitFor(() => expect((item as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(item);
}

async function createSeed(dialog: ReturnType<typeof within>): Promise<string> {
  fireEvent.click(dialog.getByRole('button', { name: /Create a new wallet/i }));
  fireEvent.click(dialog.getByRole('button', { name: /^Reveal$/ }));
  const seed = document.querySelector('[data-sentry-mask="true"]:not(input)')!.textContent!;
  expect(seed).toMatch(/^[0-9a-f]{64}$/);
  return seed;
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('ConnectModal seed hygiene', () => {
  it('marks the seed input as a secret and wipes it when import fails', async () => {
    const { spy, dialog } = setup(async () => {
      throw new Error('bad seed');
    });
    await openSeed(dialog);
    const input = document.querySelector('input[type="password"]') as HTMLInputElement;
    expect(input.getAttribute('autocomplete')).toBe('new-password');
    expect(input.getAttribute('data-private')).toBe('true');
    expect(input.getAttribute('data-sentry-mask')).toBe('true');

    fireEvent.change(input, { target: { value: 'ab'.repeat(32) } });
    fireEvent.click(dialog.getByRole('button', { name: /Import wallet/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith('seed', { seed: 'ab'.repeat(32) }));
    await waitFor(() => expect(input.value).toBe(''));
  });

  it('requires characters of a new seed to be typed back before Unlock', async () => {
    const { spy, dialog } = setup(async () => ({ address: MOCK_ADDRESS }));
    await openSeed(dialog);
    const seed = await createSeed(dialog);
    const unlock = dialog.getByRole('button', { name: /^Unlock$/ }) as HTMLButtonElement;
    expect(unlock.disabled).toBe(true);

    fireEvent.click(dialog.getByRole('checkbox'));
    const label = dialog.getByText(/Type characters \d+–\d+ of your seed/).textContent!;
    const start = Number(/characters (\d+)/.exec(label)![1]) - 1;
    const expected = seed.slice(start, start + SEED_CONFIRM_CHARS);
    const confirm = dialog.getByTestId('sqc-seed-confirm');

    const wrong = expected.split('').map((c) => (c === '0' ? '1' : '0')).join('');
    fireEvent.change(confirm, { target: { value: wrong } });
    expect(unlock.disabled).toBe(true);
    fireEvent.change(confirm, { target: { value: expected.toUpperCase() } });
    expect(unlock.disabled).toBe(false);

    fireEvent.click(unlock);
    await waitFor(() => expect(spy).toHaveBeenCalledWith('seed', { seed }));
  });

  it('clears a copied seed from the clipboard only if it is still there', async () => {
    let clip = '';
    const clipboard = {
      writeText: vi.fn(async (t: string) => {
        clip = t;
      }),
      readText: vi.fn(async () => clip),
    };
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true });
    const { dialog } = setup(async () => ({ address: MOCK_ADDRESS }));
    await openSeed(dialog);
    const seed = await createSeed(dialog);

    vi.useFakeTimers();
    await act(async () => {
      fireEvent.click(dialog.getByRole('button', { name: /^Copy$/ }));
    });
    expect(clip).toBe(seed);
    await act(async () => {
      vi.advanceTimersByTime(SEED_CLIPBOARD_CLEAR_MS);
    });
    expect(clip).toBe('');

    await act(async () => {
      fireEvent.click(dialog.getByRole('button', { name: /^Copied$/ }));
    });
    clip = 'something else the user copied';
    await act(async () => {
      vi.advanceTimersByTime(SEED_CLIPBOARD_CLEAR_MS);
    });
    expect(clip).toBe('something else the user copied');
  });

  it('re-masks a revealed seed after the reveal timeout', async () => {
    const { dialog } = setup(async () => ({ address: MOCK_ADDRESS }));
    await openSeed(dialog);
    vi.useFakeTimers();
    await createSeed(dialog);
    expect(dialog.getByRole('button', { name: /^Hide$/ })).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(SEED_REVEAL_MS);
    });
    expect(screen.getByRole('button', { name: /^Reveal$/ })).toBeTruthy();
  });
});
