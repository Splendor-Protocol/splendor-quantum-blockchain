/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { type ReactNode } from 'react';
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { MODULE_ROUTER_ADDRESS } from '@splendor/sqc-sdk';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { TokenPanel } from '../widgets/TokenPanel.js';
import { DeployTokenWidget } from '../widgets/DeployTokenWidget.js';
import { ControllerContext, NetworkSwitchContext } from '../react/context.js';
import type { ConnectController } from '../core/controller.js';
import type { ConnectControllerState } from '../core/types.js';
import { clickWhenEnabled } from './test-helpers.js';

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

type Estimate = { fee: string; fee_is_floor?: boolean; reverted?: boolean; revert_reason?: string };

function setup(estimate: Estimate, balanceSpld = '1.5') {
  let state = {
    status: 'connected',
    connectingStep: null,
    account: { address: ADDR, connectorId: 'seed', keyOrigin: 'native', postQuantumSecure: true },
    info: { address: ADDR, public_key_b64: 'x', public_key_hex: '00', postQuantumSecure: true, keyOrigin: 'native' },
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
    },
  } as unknown as ConnectControllerState;
  const estimateGas = vi.fn(async (_req: Record<string, unknown>) => ({ gas: 21000, ...estimate }));
  const sendTransfer = vi.fn(async () => ({ status: 'ok', hash: 'h1', confirmation: { ok: true } }));
  const wallet = { sendTransfer, connection: { estimateGas } };
  const provider = { getBalance: vi.fn(async () => ({ balance: '0', balance_spld: balanceSpld })) };
  const listeners = new Set<() => void>();
  const setAccount = (address: string) => {
    state = {
      ...state,
      account: { ...state.account!, address },
      info: { ...state.info!, address },
    } as ConnectControllerState;
    listeners.forEach((fn) => fn());
  };
  const ctrl = {
    getState: () => state,
    subscribe: (fn: () => void) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
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
  return { Wrap, estimateGas, sendTransfer, setAccount };
}

function decodeEnvelope(data: unknown): { module: string; action: string; params: Record<string, unknown> } {
  const hex = String(data).replace(/^0x/, '');
  return JSON.parse(Buffer.from(hex, 'hex').toString('utf8'));
}

beforeEach(() => {
  localStorage.clear();
  transferHuman.mockClear();
  deployQRC20.mockClear();
});
afterEach(() => cleanup());

describe('TransferWidget review fee', () => {
  it('sends with the estimated fee', async () => {
    const { Wrap, sendTransfer } = setup({ fee: '2000' });
    const view = render(React.createElement(TransferWidget, { defaultTo: OTHER, defaultAmount: '0.25' }), {
      wrapper: Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    await clickWhenEnabled(root, /Confirm send/i);
    await waitFor(() => expect(sendTransfer).toHaveBeenCalledTimes(1));
    expect(sendTransfer).toHaveBeenCalledWith({ to: OTHER, amountSPLD: '0.25', waitForConfirmation: true, fee: '2000' });
  });

  it('blocks Confirm when amount plus the estimated fee exceeds the balance', async () => {
    const { Wrap, sendTransfer } = setup({ fee: '1000000000000000' });
    const view = render(React.createElement(TransferWidget, { defaultTo: OTHER, defaultAmount: '1.4999' }), {
      wrapper: Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 1\.5 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    await waitFor(() => expect(root.getByTestId('sqc-review-over-balance')).toBeTruthy());
    const confirm = root.getByRole('button', { name: /Confirm send/i }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(confirm);
    expect(sendTransfer).not.toHaveBeenCalled();
  });
});

describe('TokenPanel review fee', () => {
  async function toReview(estimate: Estimate, balanceSpld?: string) {
    const ctx = setup(estimate, balanceSpld);
    const view = render(React.createElement(TokenPanel, { tokenAddress: TOKEN, defaultAmount: '2' }), {
      wrapper: ctx.Wrap,
    });
    const root = within(view.container);
    await waitFor(() => expect(root.getByText(/Balance: 5(\.0)? LAB/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/i), { target: { value: OTHER } });
    fireEvent.click(root.getByRole('button', { name: /^Review$/i }));
    return { ...ctx, root };
  }

  it('estimates the qrc20 transfer through the module router', async () => {
    const { root, estimateGas } = await toReview({ fee: '1000' });
    await waitFor(() => expect(estimateGas).toHaveBeenCalled());
    const req = estimateGas.mock.calls.at(-1)![0];
    expect(req.from).toBe(ADDR);
    expect(req.to).toBe(MODULE_ROUTER_ADDRESS);
    expect(decodeEnvelope(req.data)).toEqual({
      module: 'qrc20',
      action: 'transfer',
      params: { token: TOKEN, to: OTHER, amount: '2000000000000000000' },
    });
    await clickWhenEnabled(root, /Confirm send/i);
    await waitFor(() => expect(transferHuman).toHaveBeenCalledTimes(1));
  });

  it('sends the reviewed fee with the token transfer', async () => {
    const { root } = await toReview({ fee: '4321' });
    await clickWhenEnabled(root, /Confirm send/i);
    await waitFor(() => expect(transferHuman).toHaveBeenCalledTimes(1));
    const args = transferHuman.mock.calls[0] as unknown[];
    expect(args.slice(1, 3)).toEqual([OTHER, '2']);
    expect(args[3]).toMatchObject({ fee: '4321' });
  });

  it('blocks Confirm when the SPLD balance cannot pay the network fee', async () => {
    const { root } = await toReview({ fee: '1000000000000000' }, '0.0000001');
    await waitFor(() => expect(root.getByTestId('sqc-review-fee-unaffordable')).toBeTruthy());
    const confirm = root.getByRole('button', { name: /Confirm send/i }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(confirm);
    expect(transferHuman).not.toHaveBeenCalled();
  });

  it('blocks Confirm when the node predicts a revert', async () => {
    const { root } = await toReview({ fee: '1000', reverted: true, revert_reason: 'insufficient token balance' });
    await waitFor(() => expect(root.getByTestId('sqc-review-revert').textContent).toMatch(/insufficient/));
    const confirm = root.getByRole('button', { name: /Confirm send/i }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(confirm);
    expect(transferHuman).not.toHaveBeenCalled();
  });
});

describe('DeployTokenWidget review fee', () => {
  async function toReview(estimate: Estimate) {
    const ctx = setup(estimate);
    const view = render(React.createElement(DeployTokenWidget, {}), { wrapper: ctx.Wrap });
    const root = within(view.container);
    fireEvent.click(root.getByRole('button', { name: /^Review deploy$/i }));
    return { ...ctx, root };
  }

  it('estimates a qrc20 deploy envelope and deploys with that fee', async () => {
    const { root, estimateGas } = await toReview({ fee: '5000' });
    await waitFor(() => expect(estimateGas).toHaveBeenCalled());
    const req = estimateGas.mock.calls.at(-1)![0];
    expect(req.to).toBe(MODULE_ROUTER_ADDRESS);
    const env = decodeEnvelope(req.data);
    expect(env.module).toBe('qrc20');
    expect(env.action).toBe('deploy');
    expect(env.params).toHaveProperty('name');
    expect(env.params).toHaveProperty('total_supply');
    await clickWhenEnabled(root, /^Confirm deploy$/i);
    await waitFor(() => expect(deployQRC20).toHaveBeenCalledTimes(1));
    expect((deployQRC20.mock.calls[0] as unknown[])[2]).toMatchObject({ fee: '5000' });
  });

  it('blocks Confirm when the deploy is predicted to revert', async () => {
    const { root } = await toReview({ fee: '1000', reverted: true, revert_reason: 'symbol taken' });
    await waitFor(() => expect(root.getByTestId('sqc-review-revert').textContent).toMatch(/symbol taken/));
    const confirm = root.getByRole('button', { name: /^Confirm deploy$/i }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    fireEvent.click(confirm);
    expect(deployQRC20).not.toHaveBeenCalled();
  });

  it('shows Submitted, not Confirmed, when the deploy result has no confirmation', async () => {
    const { root } = await toReview({ fee: '5000' });
    await clickWhenEnabled(root, /^Confirm deploy$/i);
    await waitFor(() => expect(root.getByRole('status').textContent).toMatch(/Submitted/));
    expect(root.getByRole('status').textContent).not.toMatch(/Confirmed/);
  });

  it('shows Confirmed when the deploy result is confirmed', async () => {
    deployQRC20.mockResolvedValueOnce({
      address: 'SQ:' + '77'.repeat(32),
      result: { hash: 'dep-hash', confirmation: { confirmed: true } },
    } as never);
    const { root } = await toReview({ fee: '5000' });
    await clickWhenEnabled(root, /^Confirm deploy$/i);
    await waitFor(() => expect(root.getByRole('status').textContent).toMatch(/Confirmed/));
  });

  it('closes the review when the wallet changes', async () => {
    const { root, setAccount } = await toReview({ fee: '5000' });
    await waitFor(() => expect(root.getByRole('button', { name: /^Confirm deploy$/i })).toBeTruthy());
    act(() => setAccount(OTHER));
    await waitFor(() => expect(root.queryByRole('button', { name: /^Confirm deploy$/i })).toBeNull());
    expect(root.getByRole('button', { name: /^Review deploy$/i })).toBeTruthy();
    expect(deployQRC20).not.toHaveBeenCalled();
  });

  it('estimates and blocks an unaffordable deploy when review is off', async () => {
    const ctx = setup({ fee: '1000000000000000' }, '0.0000001');
    const view = render(React.createElement(DeployTokenWidget, { review: false }), { wrapper: ctx.Wrap });
    const root = within(view.container);
    await waitFor(() => expect(ctx.estimateGas).toHaveBeenCalled());
    await waitFor(() => expect(root.getByTestId('sqc-review-fee-unaffordable')).toBeTruthy());
    const deploy = root.getByRole('button', { name: /^Deploy/i }) as HTMLButtonElement;
    expect(deploy.disabled).toBe(true);
    fireEvent.click(deploy);
    expect(deployQRC20).not.toHaveBeenCalled();
  });
});
