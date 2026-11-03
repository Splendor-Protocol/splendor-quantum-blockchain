/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import {
  AccountCard,
  ActivityFeed,
  AddressBook,
  BridgeClaimWidget,
  DeployTokenWidget,
  NFT_PANEL_MAX_IDS,
  NftPanel,
  ReceiveWidget,
  SignInButton,
  TOKEN_BALANCES_MAX,
  TokenBalances,
  TokenPanel,
  TransferWidget,
  ValidatorList,
} from '../../widgets/index.js';
import { __resetAddressBook } from '../../core/addressBook.js';
import { TOPIC_TRANSFER } from '../../core/activity.js';
import {
  MOCK_ADDRESS,
  MockSplendorProvider,
  createMockConnectController,
  mockWallet,
  type MockWallet,
  type MockWalletOptions,
} from '../../testing.js';
import { clickWhenEnabled } from '../test-helpers.js';

const ME = MOCK_ADDRESS;
const OTHER = 'SQ:' + 'cd'.repeat(32);
const TOKEN = 'SQ:' + 'ee'.repeat(32);
const COLLECTION = 'SQ:' + 'c1'.repeat(32);
const BRIDGE = 'SQ:' + 'b1'.repeat(32);
const SECRET = 'SECRET_TOKEN_9f3a';
const LEAKY = () => new Error(`HTTP 500 <pre>panic: db password=${SECRET} at /srv/node/rpc.go:41</pre>`);

/** Answers every module read the widgets make with healthy chain data. */
const healthy: MockWalletOptions['moduleCall'] = ({ module, action, params }) => {
  if (module === 'qrc20') {
    if (action === 'token_info') return { name: 'Lab', symbol: 'LAB', decimals: 18, total_supply: '1' };
    if (action === 'balance_of') return { balance: '5000000000000000000' };
  }
  if (module === 'qrc721') {
    if (action === 'token_info') return { name: 'Members', symbol: 'MEM', owner: ME, total_supply: 1 };
    if (action === 'owner_of') {
      if (String(params.id) === '1') return ME;
      throw new Error('token does not exist');
    }
  }
  if (module === 'bridge' && action === 'bridge_info') {
    return { paused: false, threshold: 1, evm_chain_id: 1, max_claim_amount: '1', pool_balance: '1' };
  }
  if (module === 'validators') {
    if (action === 'list') return [{ address: OTHER, bond: '2500000000000000000000', status: 1 }];
    if (action === 'bond') return { spld: '2500' };
    if (action === 'approved') return { approved: false };
  }
  return {};
};

type Row = { kind: string; gated: boolean; el: () => React.ReactElement };

const WIDGETS: Row[] = [
  { kind: 'transfer', gated: true, el: () => <TransferWidget toast={false} /> },
  { kind: 'token', gated: true, el: () => <TokenPanel tokenAddress={TOKEN} toast={false} /> },
  { kind: 'deploy', gated: true, el: () => <DeployTokenWidget toast={false} /> },
  { kind: 'account', gated: true, el: () => <AccountCard /> },
  { kind: 'signin', gated: true, el: () => <SignInButton /> },
  { kind: 'nft', gated: true, el: () => <NftPanel collectionAddress={COLLECTION} tokenIds="1-2" toast={false} /> },
  { kind: 'bridge', gated: false, el: () => <BridgeClaimWidget bridgeAddress={BRIDGE} /> },
  { kind: 'validators', gated: false, el: () => <ValidatorList pollMs={0} /> },
  { kind: 'activity', gated: true, el: () => <ActivityFeed pollMs={0} /> },
  { kind: 'receive', gated: true, el: () => <ReceiveWidget /> },
  { kind: 'address-book', gated: false, el: () => <AddressBook /> },
  { kind: 'balances', gated: true, el: () => <TokenBalances tokens={TOKEN} /> },
];

function mount(
  el: React.ReactElement,
  opts: { wallet?: MockWallet; walletOpts?: MockWalletOptions; status?: 'connected' | 'disconnected' | 'locked'; walletChainId?: string } = {},
) {
  const wallet = opts.wallet ?? mockWallet({ moduleCall: healthy, height: 10, ...opts.walletOpts });
  const ctrl = createMockConnectController({
    wallet,
    status: opts.status ?? 'connected',
    balance: '12.5',
    walletChainId: opts.walletChainId,
  });
  const view = render(<MockSplendorProvider controller={ctrl}>{el}</MockSplendorProvider>);
  return { wallet, ctrl, view, root: within(view.container) };
}

const sends = (w: MockWallet) =>
  w.calls.filter((c) => ['sendTransfer', 'sendModuleCall', 'callContract', 'deployContract', 'signMessage'].includes(c.method));

/** Let mount effects and their reads settle. */
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 20)); });

beforeEach(() => {
  localStorage.clear();
  __resetAddressBook();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe.each(WIDGETS)('widget prod matrix: $kind', ({ kind, gated, el }) => {
  it('[L1] mounts connected with its root and no React errors', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { view } = mount(el());
    await settle();
    const root = view.container.querySelector(`[data-sqc-widget="${kind}"]`);
    expect(root).toBeTruthy();
    expect(view.container.querySelector('[data-sqc-gate]')).toBeNull();
    const real = errors.mock.calls.filter((c) => !String(c[0]).includes('not wrapped in act'));
    expect(real).toEqual([]);
  });

  it('[L2] disconnected: gated widgets show Connect, read-only ones still render, nothing is signed', async () => {
    const { view, wallet } = mount(el(), { status: 'disconnected' });
    await settle();
    expect(!!view.container.querySelector('[data-sqc-gate="disconnected"]')).toBe(gated);
    expect(sends(wallet)).toEqual([]);
  });

  it('[L2] locked: gated widgets show Unlock instead of their form', async () => {
    const { view, wallet } = mount(el(), { status: 'locked' });
    await settle();
    expect(!!view.container.querySelector('[data-sqc-gate="locked"]')).toBe(gated);
    expect(sends(wallet)).toEqual([]);
  });

  it('[L2] wallet on another chain: a warning shows and every control is disabled', async () => {
    const { view } = mount(el(), { walletChainId: 'some-other-chain' });
    await settle();
    expect(view.container.querySelector('[data-testid="sqc-wrong-network"]')).toBeTruthy();
    const fieldset = view.container.querySelector('fieldset.sqc-widget__fieldset') as HTMLFieldSetElement | null;
    expect(fieldset?.disabled).toBe(true);
  });

  it('[L3] RPC failure on every read: no crash and no raw node body on screen', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const fail = {
      'connection.moduleCall': LEAKY(),
      'connection.getBalance': LEAKY(),
      'connection.getStatus': LEAKY(),
      'connection.getLogs': LEAKY(),
      'connection.estimateGas': LEAKY(),
    };
    const { view } = mount(el(), { walletOpts: { fail } });
    await settle();
    expect(view.container.querySelector(`[data-sqc-widget="${kind}"]`)).toBeTruthy();
    expect(view.container.textContent).not.toContain(SECRET);
    expect(view.container.textContent).not.toContain('rpc.go');
    errors.mockRestore();
  });
});

describe('widget prod: read errors surface as safe alerts', () => {
  const failing = { fail: { 'connection.moduleCall': LEAKY(), 'connection.getStatus': LEAKY() } };

  it.each([
    ['validators', () => <ValidatorList pollMs={0} />],
    ['activity', () => <ActivityFeed pollMs={0} />],
  ])('[L3] %s shows a load-failed alert', async (_kind, el) => {
    const { root } = mount(el(), { walletOpts: failing });
    await waitFor(() => expect(root.getByRole('alert').textContent).toMatch(/\S/));
    expect(root.getByRole('alert').textContent).not.toContain(SECRET);
  });
});

/** Wrap a mock wallet method so it resolves only when released, to hold a send in flight. */
function holdSends(wallet: MockWallet, method: 'sendTransfer' | 'sendModuleCall') {
  const original = wallet[method] as (...a: unknown[]) => Promise<unknown>;
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  wallet[method] = async (...args: unknown[]) => {
    await gate;
    return original(...args);
  };
  return () => act(async () => release());
}

describe('widget prod: send safety', () => {
  it('[L3] TransferWidget: double-clicking Confirm sends once', async () => {
    const wallet = mockWallet({ moduleCall: healthy });
    const release = holdSends(wallet, 'sendTransfer');
    const { root } = mount(<TransferWidget defaultTo={OTHER} defaultAmount="0.5" toast={false} showFee={false} />, { wallet });
    await waitFor(() => expect(root.getByText(/Balance: 12\.5 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/ }));
    const confirm = await waitFor(() => root.getByRole('button', { name: /Confirm send/ }) as HTMLButtonElement);
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    await release();
    await waitFor(() => expect(wallet.calls.filter((c) => c.method === 'sendTransfer')).toHaveLength(1));
  });

  it('[L3] TokenPanel: double-clicking Confirm sends one token transfer', async () => {
    const wallet = mockWallet({ moduleCall: healthy });
    const release = holdSends(wallet, 'sendModuleCall');
    const { root } = mount(<TokenPanel tokenAddress={TOKEN} defaultAmount="1" toast={false} showFee={false} />, { wallet });
    await waitFor(() => expect(root.getByText(/Balance: 5(\.0)? LAB/)).toBeTruthy());
    fireEvent.change(root.getByLabelText(/^To \(SQ/), { target: { value: OTHER } });
    fireEvent.click(root.getByRole('button', { name: /^Review$/ }));
    const confirm = await waitFor(() => root.getByRole('button', { name: /Confirm send/ }) as HTMLButtonElement);
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    await release();
    await waitFor(() => expect(wallet.calls.filter((c) => c.method === 'sendModuleCall')).toHaveLength(1));
  });

  it('[L3] DeployTokenWidget: double-clicking Confirm deploys once', async () => {
    const wallet = mockWallet({ moduleCall: healthy });
    const release = holdSends(wallet, 'sendModuleCall');
    const { root } = mount(<DeployTokenWidget toast={false} showFee={false} />, { wallet });
    fireEvent.click(root.getByRole('button', { name: /^Review deploy$/ }));
    const confirm = await waitFor(() => root.getByRole('button', { name: /^Confirm deploy$/ }) as HTMLButtonElement);
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    await release();
    await waitFor(() => expect(wallet.calls.filter((c) => c.method === 'sendModuleCall')).toHaveLength(1));
  });

  it('[L3] a failed send shows safe copy, never the raw node error', async () => {
    const { root } = mount(<TransferWidget defaultTo={OTHER} defaultAmount="0.5" toast={false} />, {
      walletOpts: { fail: { sendTransfer: LEAKY() } },
    });
    await waitFor(() => expect(root.getByText(/Balance: 12\.5 SPLD/)).toBeTruthy());
    fireEvent.click(root.getByRole('button', { name: /^Review$/ }));
    await clickWhenEnabled(root, /Confirm send/);
    await waitFor(() => expect(root.getByRole('alert').textContent).toMatch(/failed/i));
    expect(root.getByRole('alert').textContent).not.toContain(SECRET);
  });

  it('[L2] sending to yourself, over the balance, or to a bad address never reaches review', async () => {
    const { root, wallet } = mount(<TransferWidget toast={false} />);
    await waitFor(() => expect(root.getByText(/Balance: 12\.5 SPLD/)).toBeTruthy());
    const review = () => root.getByRole('button', { name: /^Review$/ }) as HTMLButtonElement;
    const to = root.getByLabelText(/^To \(SQ/);
    const amount = root.getByLabelText(/^Amount/);
    for (const [addr, amt] of [
      ['0x1234', '1'],
      [ME, '1'],
      [OTHER, '1000'],
      [OTHER, '-1'],
      [OTHER, ''],
    ] as const) {
      fireEvent.change(to, { target: { value: addr } });
      fireEvent.change(amount, { target: { value: amt } });
      expect(review().disabled).toBe(true);
    }
    expect(sends(wallet)).toEqual([]);
  });
});

describe('widget prod: list caps and volume', () => {
  it(`[L4] NftPanel renders at most ${NFT_PANEL_MAX_IDS} tiles for a huge range`, async () => {
    const { view } = mount(<NftPanel collectionAddress={COLLECTION} tokenIds="1-100000" toast={false} />);
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-nft"]').length).toBe(NFT_PANEL_MAX_IDS));
  });

  it(`[L4] TokenBalances keeps at most ${TOKEN_BALANCES_MAX} tokens`, async () => {
    const many = Array.from({ length: TOKEN_BALANCES_MAX + 15 }, (_, i) => 'SQ:' + i.toString(16).padStart(64, '0')).join(',');
    const { view } = mount(<TokenBalances tokens={many} />);
    await waitFor(() =>
      expect(view.container.querySelectorAll('[data-testid="sqc-token-balance"]').length).toBe(TOKEN_BALANCES_MAX),
    );
  });

  it('[L4] ActivityFeed trims 300 transfer logs to its limit, newest first', async () => {
    const hex = (a: string) => a.slice(3);
    const word = (n: bigint) => n.toString(16).padStart(64, '0');
    const logs = Array.from({ length: 300 }, (_, i) => ({
      address: TOKEN,
      topics: [TOPIC_TRANSFER, hex(OTHER), hex(ME)].map((t) => '0x' + t),
      data: '0x' + word(BigInt(i + 1)),
      block_height: i + 1,
      log_index: 0,
    }));
    const { view } = mount(<ActivityFeed pollMs={0} limit={25} />, {
      walletOpts: {
        height: 400,
        logs: (f) => logs.filter((l) => (f.topics ?? []).every((t, i) => !t || l.topics[i] === '0x' + t)),
      },
    });
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-activity-item"]').length).toBe(25));
    expect(view.container.querySelector('[data-testid="sqc-activity-item"]')!.textContent).toContain('300');
  });

  it('[L4] ten widgets on one page share one provider without cross-talk', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { view, wallet } = mount(
      <>
        {WIDGETS.map((w) => (
          <React.Fragment key={w.kind}>{w.el()}</React.Fragment>
        ))}
      </>,
    );
    await settle();
    for (const w of WIDGETS) expect(view.container.querySelector(`[data-sqc-widget="${w.kind}"]`)).toBeTruthy();
    expect(sends(wallet)).toEqual([]);
    expect(errors.mock.calls.filter((c) => !String(c[0]).includes('not wrapped in act'))).toEqual([]);
  });
});
