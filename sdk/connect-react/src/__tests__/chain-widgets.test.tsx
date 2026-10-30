/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import React from 'react';
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { NftPanel, parseTokenIds } from '../widgets/NftPanel.js';
import { BridgeClaimWidget, parseAttestations } from '../widgets/BridgeClaimWidget.js';
import { ValidatorList } from '../widgets/ValidatorList.js';
import { ActivityFeed } from '../widgets/ActivityFeed.js';
import { ReceiveWidget } from '../widgets/ReceiveWidget.js';
import { AddressBook } from '../widgets/AddressBook.js';
import { TokenBalances, parseTokenList } from '../widgets/TokenBalances.js';
import { TransferWidget } from '../widgets/TransferWidget.js';
import { TOPIC_TRANSFER } from '../core/activity.js';
import { __resetAddressBook, ADDRESS_BOOK_KEY, ADDRESS_BOOK_MAX, getContacts, saveContact } from '../core/addressBook.js';
import { buildPaymentUri, parsePaymentUri } from '../core/paymentUri.js';
import { MOCK_ADDRESS, MockSplendorProvider, createMockConnectController, mockWallet, type MockWalletOptions } from '../testing.js';

const ME = MOCK_ADDRESS;
const OTHER = 'SQ:' + 'cd'.repeat(32);
const COLLECTION = 'SQ:' + 'c1'.repeat(32);
const BRIDGE = 'SQ:' + 'b1'.repeat(32);
const TOKEN = 'SQ:' + 'ee'.repeat(32);
const meBytes = Array.from({ length: 32 }, () => 0xab);

function mount(el: React.ReactElement, walletOpts: MockWalletOptions = {}, status: 'connected' | 'disconnected' = 'connected') {
  const wallet = mockWallet(walletOpts);
  const ctrl = createMockConnectController({ wallet, status, balance: '12.5' });
  const view = render(React.createElement(MockSplendorProvider, { controller: ctrl }, el));
  return { ctrl, wallet, view, root: within(view.container) };
}

beforeEach(() => {
  localStorage.clear();
  __resetAddressBook();
});
afterEach(() => cleanup());

describe('NftPanel (QRC-721)', () => {
  const owners: Record<string, string> = { '1': ME, '2': OTHER };
  const moduleCall: MockWalletOptions['moduleCall'] = ({ module, action, params }) => {
    if (module !== 'qrc721') return {};
    if (action === 'token_info') return { name: 'Members', symbol: 'MEM', owner: meBytes, total_supply: 2 };
    if (action === 'owner_of') {
      const o = owners[String(params.id)];
      if (!o) throw new Error('token does not exist');
      return o;
    }
    return {};
  };

  it('parses id ranges with a cap', () => {
    expect(parseTokenIds('1-3, 7 3 x 9-8')).toEqual(['1', '2', '3', '7']);
    expect(parseTokenIds('1-1000')).toHaveLength(60);
    expect(parseTokenIds('')).toEqual([]);
  });

  it('shows ownership, sends an owned NFT and offers mint to the collection owner', async () => {
    const { root, view, wallet } = mount(
      React.createElement(NftPanel, { collectionAddress: COLLECTION, tokenIds: '1-3', toast: false }),
      { moduleCall },
    );
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-nft"]')).toHaveLength(3));
    expect(root.getByText(/Members \(MEM\) · 2 minted · You own 1/)).toBeTruthy();
    const tiles = view.container.querySelectorAll('[data-testid="sqc-nft"]');
    expect(tiles[0]!.getAttribute('data-owned')).toBe('true');
    expect(tiles[1]!.getAttribute('data-owned')).toBe('false');
    expect(tiles[2]!.getAttribute('data-minted')).toBe('false');
    expect(root.getByRole('button', { name: /Mint #3/ })).toBeTruthy();

    fireEvent.click(root.getByRole('button', { name: /Token #1/ }));
    fireEvent.change(root.getByLabelText(/^To \(SQ/), { target: { value: OTHER } });
    fireEvent.click(root.getByRole('button', { name: /Send NFT #1/ }));
    await waitFor(() => expect(wallet.calls.some((c) => c.method === 'sendModuleCall')).toBe(true));
    const send = wallet.calls.find((c) => c.method === 'sendModuleCall')!.args[0] as Record<string, unknown>;
    expect(send).toMatchObject({ module: 'qrc721', action: 'transfer', params: { collection: COLLECTION, to: OTHER, id: '1' } });
  });

  it('hides mint from non-owners and blocks re-minting an existing id', async () => {
    const notOwner: MockWalletOptions['moduleCall'] = (env) =>
      env.action === 'token_info' ? { name: 'X', symbol: 'X', owner: OTHER, total_supply: 1 } : moduleCall(env);
    const a = mount(React.createElement(NftPanel, { collectionAddress: COLLECTION, tokenIds: '1-2' }), { moduleCall: notOwner });
    await waitFor(() => expect(a.view.container.querySelectorAll('[data-testid="sqc-nft"]')).toHaveLength(2));
    expect(a.root.queryByRole('button', { name: /^Mint/ })).toBeNull();
    cleanup();
    const b = mount(React.createElement(NftPanel, { collectionAddress: COLLECTION, tokenIds: '1-3' }), { moduleCall });
    await waitFor(() => expect(b.root.getByLabelText('Token id')).toBeTruthy());
    fireEvent.change(b.root.getByLabelText('Token id'), { target: { value: '2' } });
    expect(b.root.getByText('Token #2 is already minted')).toBeTruthy();
    expect((b.root.getByRole('button', { name: /^Mint #2/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('is wallet gated', () => {
    const { view } = mount(React.createElement(NftPanel, { collectionAddress: COLLECTION }), { moduleCall }, 'disconnected');
    expect(view.container.querySelector('[data-sqc-gate="disconnected"]')).toBeTruthy();
  });
});

describe('BridgeClaimWidget', () => {
  const moduleCall: MockWalletOptions['moduleCall'] = ({ module, action, params }) => {
    if (module !== 'bridge') return {};
    if (action === 'bridge_info') {
      return {
        paused: false,
        threshold: 2,
        evm_chain_id: 1,
        max_claim_amount: '5000000000000000000',
        pool_balance: '100000000000000000000',
        claimed_total_spld: '0',
        claimed_total_splv: '0',
      };
    }
    if (action === 'claim_status') return Number(params.id) === 1 ? { claimed: true, height: 99 } : { claimed: false };
    return {};
  };

  it('shows status and looks up burns without a wallet; claim form is off by default', async () => {
    const { root, view } = mount(React.createElement(BridgeClaimWidget, { bridgeAddress: BRIDGE }), { moduleCall }, 'disconnected');
    await waitFor(() => expect(view.container.querySelector('[data-testid="sqc-bridge-info"]')).toBeTruthy());
    expect(root.getByText('Active')).toBeTruthy();
    expect(root.getByText('5')).toBeTruthy();
    fireEvent.change(root.getByLabelText('Burn id'), { target: { value: '1' } });
    fireEvent.click(root.getByRole('button', { name: 'Check' }));
    await waitFor(() => expect(root.getByText('Burn #1 was claimed at block 99.')).toBeTruthy());
    fireEvent.change(root.getByLabelText('Burn id'), { target: { value: '0' } });
    expect(root.getByText(/Burn ids are whole numbers/)).toBeTruthy();
    expect(root.queryByRole('button', { name: /Submit claim/ })).toBeNull();
  });

  it('submits an attested claim from the operator form', async () => {
    const { root, wallet } = mount(React.createElement(BridgeClaimWidget, { bridgeAddress: BRIDGE, allowClaim: true, toast: false }), { moduleCall });
    await waitFor(() => expect(root.getByRole('button', { name: /Submit claim/ })).toBeTruthy());
    const claimIdInput = root.getAllByLabelText('Burn id').find((i) => i.id.endsWith('-id'))!;
    fireEvent.change(claimIdInput, { target: { value: '3' } });
    fireEvent.change(root.getByLabelText('Amount (base units)'), { target: { value: '1000' } });
    fireEvent.change(root.getByLabelText(/^Recipient/), { target: { value: OTHER } });
    fireEvent.change(root.getByLabelText('EVM burn tx hash'), { target: { value: '0xabc' } });
    fireEvent.change(root.getByLabelText(/^Attestations/), { target: { value: '[{"pubkey":"p","signature":"s"}]' } });
    fireEvent.click(root.getByRole('button', { name: /Submit claim/ }));
    await waitFor(() => expect(wallet.calls.some((c) => c.method === 'sendModuleCall')).toBe(true));
    expect(wallet.calls.find((c) => c.method === 'sendModuleCall')!.args[0]).toMatchObject({
      module: 'bridge',
      action: 'claim',
      params: { bridge: BRIDGE, id: 3, asset: 'spld', amount: '1000', recipient: OTHER, evm_tx: '0xabc' },
    });
  });

  it('validates attestations JSON', () => {
    expect(parseAttestations('[{"pubkey":"a","signature":"b"}]')).toEqual([{ pubkey: 'a', signature: 'b' }]);
    expect(parseAttestations('[]')).toBeNull();
    expect(parseAttestations('[{"pubkey":"a"}]')).toBeNull();
    expect(parseAttestations('nope')).toBeNull();
  });
});

describe('ValidatorList', () => {
  it('lists active validators without a wallet and checks admission', async () => {
    const { root, view } = mount(React.createElement(ValidatorList, {}), {
      moduleCall: ({ module, action, params }) => {
        if (module !== 'validators') return {};
        if (action === 'list') return [{ address: OTHER, bond: '2500000000000000000000', status: 1, joined_height: 12 }];
        if (action === 'bond') return { spld: '2500' };
        if (action === 'approved') return { approved: params.address === OTHER };
        return {};
      },
    }, 'disconnected');
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-validator"]')).toHaveLength(1));
    expect(root.getByText('1 active · bond 2500 SPLD')).toBeTruthy();
    expect(root.getByText('2500 SPLD')).toBeTruthy();
    fireEvent.change(root.getByLabelText(/^Address/), { target: { value: OTHER } });
    fireEvent.click(root.getByRole('button', { name: 'Check' }));
    await waitFor(() => expect(root.getByText('This address holds an admission approval.')).toBeTruthy());
  });
});

describe('ActivityFeed', () => {
  it('renders sent / received rows with token symbols and contact names', async () => {
    saveContact({ address: OTHER, label: 'Alice', network: 'local' });
    const hex = (a: string) => a.slice(3);
    const word = (n: bigint) => n.toString(16).padStart(64, '0');
    const logs = [
      { address: TOKEN, topics: [TOPIC_TRANSFER, hex(OTHER), hex(ME)].map((t) => '0x' + t), data: '0x' + word(15n * 10n ** 17n), block_height: 90, log_index: 0 },
      { address: TOKEN, topics: [TOPIC_TRANSFER, hex(ME), hex(OTHER)].map((t) => '0x' + t), data: '0x' + word(10n ** 18n), block_height: 80, log_index: 0 },
    ];
    const { root, view } = mount(React.createElement(ActivityFeed, { pollMs: 0 }), {
      height: 100,
      logs: (f) => logs.filter((l) => (f.topics ?? []).every((t, i) => !t || l.topics[i] === '0x' + t)),
      moduleCall: ({ module, action }) => (module === 'qrc20' && action === 'token_info' ? { symbol: 'LAB', decimals: 18 } : {}),
    });
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-activity-item"]')).toHaveLength(2));
    const rows = view.container.querySelectorAll('[data-testid="sqc-activity-item"]');
    expect(rows[0]!.getAttribute('data-direction')).toBe('in');
    await waitFor(() => expect(rows[0]!.textContent).toContain('1.5 LAB'));
    expect(rows[0]!.textContent).toContain('Alice');
    expect(rows[1]!.textContent).toMatch(/Sent/);
    expect(root.getByText('Block 90')).toBeTruthy();
  });

  it('shows the empty state', async () => {
    const { root } = mount(React.createElement(ActivityFeed, { pollMs: 0 }), { height: 50 });
    await waitFor(() => expect(root.getByText(/No token transfers in the last 50 blocks/)).toBeTruthy());
  });
});

describe('ReceiveWidget and payment links', () => {
  it('shows a QR of the address, and a chain-bound payment link once an amount is set', () => {
    const { root, view } = mount(React.createElement(ReceiveWidget, {}));
    const svg = view.container.querySelector('svg.sqc-qr')!;
    expect(svg.getAttribute('aria-label')).toBe(`QR code for ${ME}`);
    fireEvent.change(root.getByLabelText(/Request amount/), { target: { value: '2.5' } });
    const label = view.container.querySelector('svg.sqc-qr')!.getAttribute('aria-label')!;
    expect(label).toMatch(/^QR code for splendor:SQ:[0-9a-f]{64}\?amount=2\.5&chain=/);
    fireEvent.change(root.getByLabelText(/Request amount/), { target: { value: 'abc' } });
    expect(root.getByText('Enter a valid amount greater than zero')).toBeTruthy();
  });

  it('round-trips payment URIs and rejects malformed ones', () => {
    const uri = buildPaymentUri({ address: 'SQ:' + 'AB'.repeat(32), amount: '1.25', chainId: 'splendor-quantum-1' });
    expect(uri).toBe(`splendor:${ME}?amount=1.25&chain=splendor-quantum-1`);
    expect(parsePaymentUri(uri)).toEqual({ address: ME, amount: '1.25', chainId: 'splendor-quantum-1' });
    expect(parsePaymentUri(ME)).toEqual({ address: ME });
    expect(parsePaymentUri('splendor:0x1234')).toBeNull();
    expect(parsePaymentUri(`splendor:${ME}?amount=-1`)).toBeNull();
    expect(() => buildPaymentUri({ address: ME, amount: '0' })).toThrow();
  });
});

describe('AddressBook', () => {
  it('saves, lists, filters and removes contacts per network, and feeds transfer quick picks', async () => {
    const { root, view } = mount(
      React.createElement(React.Fragment, null, React.createElement(AddressBook, {}), React.createElement(TransferWidget, { showFee: false })),
    );
    const book = within(view.container.querySelector('[data-sqc-widget="address-book"]') as HTMLElement);
    fireEvent.change(book.getByLabelText('Name'), { target: { value: '  Bob\u202e ' } });
    fireEvent.change(book.getByLabelText(/^Address/), { target: { value: OTHER } });
    fireEvent.click(book.getByRole('button', { name: 'Save contact' }));
    await waitFor(() => expect(book.getAllByTestId('sqc-contact')).toHaveLength(1));
    expect(getContacts('local')).toEqual([{ address: OTHER, label: 'Bob', network: 'local' }]);
    expect(getContacts('testnet')).toEqual([]);
    const transfer = within(view.container.querySelector('[data-sqc-widget="transfer"]') as HTMLElement);
    fireEvent.click(transfer.getByRole('button', { name: 'Bob' }));
    expect((transfer.getByLabelText(/^To \(SQ/) as HTMLInputElement).value).toBe(OTHER);
    fireEvent.click(book.getByRole('button', { name: 'Remove Bob' }));
    await waitFor(() => expect(root.getByText('No contacts on this network yet.')).toBeTruthy());
  });

  it('ignores tampered storage and caps the book per network', () => {
    localStorage.setItem(
      ADDRESS_BOOK_KEY,
      JSON.stringify([{ address: '0xdead', label: 'x', network: 'local' }, { address: OTHER, label: '', network: 'local' }, 'junk']),
    );
    __resetAddressBook();
    expect(getContacts('local')).toEqual([]);
    for (let i = 0; i < ADDRESS_BOOK_MAX; i++) {
      expect(saveContact({ address: 'SQ:' + i.toString(16).padStart(64, '0'), label: `c${i}`, network: 'local' }).ok).toBe(true);
    }
    expect(saveContact({ address: OTHER, label: 'one too many', network: 'local' })).toEqual({ ok: false, reason: 'full' });
    expect(saveContact({ address: OTHER, label: 'x'.repeat(41), network: 'local' })).toEqual({ ok: false, reason: 'invalid' });
  });
});

describe('TokenBalances', () => {
  it('shows SPLD and each configured token, skipping invalid entries', async () => {
    expect(parseTokenList(`${TOKEN}, 0x12, ${TOKEN} ${OTHER}`)).toEqual([TOKEN, OTHER]);
    const { view } = mount(React.createElement(TokenBalances, { tokens: `${TOKEN}, bogus` }), {
      moduleCall: ({ module, action }) => {
        if (module !== 'qrc20') return {};
        if (action === 'token_info') return { symbol: 'LAB', decimals: 18 };
        if (action === 'balance_of') return { balance: '3000000000000000000' };
        return {};
      },
    });
    await waitFor(() => expect(view.container.querySelector('[data-testid="sqc-token-balance"]')?.textContent).toContain('LAB'));
    await waitFor(() => expect(view.container.querySelector('[data-testid="sqc-token-balance"]')?.textContent).toMatch(/3(\.0)?/));
    expect(view.container.querySelector('[data-testid="sqc-native-balance"]')?.textContent).toContain('12.5');
  });
});
