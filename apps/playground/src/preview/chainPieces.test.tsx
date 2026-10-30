import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TEMPLATES, createCatalog, emptySpec, instantiateTemplate, validateSpec, type DappBlock, type DappSpec } from '@splendor/dapp-spec';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { MockSplendorProvider } from '@splendor/connect-react/testing';
import { DEMO_RECIPIENT, DEMO_VALIDATORS, createPlaygroundMock } from './mock';
import { SpecRenderer } from './SpecRenderer';

const catalog = createCatalog(WIDGET_MANIFESTS);
const BRIDGE = 'SQ:' + 'b1'.repeat(32);
const TOKEN = 'SQ:' + 'de'.repeat(32);

function renderSpec(spec: DappSpec, pagePath = '/', balance = '10') {
  const ctrl = createPlaygroundMock({ network: 'testnet', state: 'connected', balance });
  const view = render(
    <MockSplendorProvider controller={ctrl}>
      <SpecRenderer catalog={catalog} spec={spec} pagePath={pagePath} />
    </MockSplendorProvider>,
  );
  return { ctrl, view, root: within(view.container) };
}

/** One page with the given blocks, flat and in the layout tree. */
function specWith(blocks: DappBlock[]): DappSpec {
  const spec = emptySpec('Chain pieces');
  const page = spec.pages[0]!;
  page.blocks = blocks;
  const stack = (page.root as { children: { children: unknown[] }[] }).children[0]!;
  stack.children = blocks.map((block) => ({ kind: 'block', block }));
  return spec;
}

const block = (id: string, widget: string, props: DappBlock['props'] = {}): DappBlock => ({ id, widget, props });

beforeEach(() => localStorage.clear());
afterEach(() => cleanup());

describe('chain-native pieces in the mock preview', () => {
  it('every new piece renders with its catalog defaults without a block error', async () => {
    const ids = ['nft-panel', 'bridge-claim', 'validator-list', 'activity-feed', 'receive', 'address-book', 'token-balances', 'qr-code', 'nft721-gallery', 'x402-paywall'];
    const blocks = ids.map((id, i) => {
      const entry = catalog.find((c) => c.id === id)!;
      const props = { ...entry.defaults } as DappBlock['props'];
      if (id === 'bridge-claim') props.bridgeAddress = BRIDGE;
      return block(`b${i}`, id, props);
    });
    const spec = specWith(blocks);
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    const { view } = renderSpec(spec);
    await waitFor(() => expect(view.container.querySelector('[data-sqc-widget="validators"] [data-testid="sqc-validator"]')).toBeTruthy());
    expect(view.container.querySelector('.pg-block-error')).toBeNull();
    for (const kind of ['nft', 'bridge', 'validators', 'activity', 'receive', 'address-book', 'balances', 'nft721-gallery', 'x402-paywall']) {
      expect(view.container.querySelector(`[data-sqc-widget="${kind}"]`), kind).toBeTruthy();
    }
    expect(view.container.querySelector('svg.sqc-qr')).toBeTruthy();
  });

  it('NFT membership template: gallery shows ownership, members page sends a pass and lists activity', async () => {
    const spec = instantiateTemplate(TEMPLATES.find((t) => t.id === 'nft-membership')!);
    expect(validateSpec(spec, { catalog }).ok).toBe(true);

    const home = renderSpec(spec, '/');
    await waitFor(() => expect(home.view.container.querySelectorAll('[data-testid="dapp-nft721"]')).toHaveLength(12));
    await waitFor(() => expect(home.view.container.querySelectorAll('[data-testid="dapp-nft721"][data-owned="true"]')).toHaveLength(3));
    cleanup();

    const members = renderSpec(spec, '/members');
    const c = members.view.container;
    await waitFor(() => expect(c.querySelectorAll('[data-testid="sqc-nft"][data-owned="true"]')).toHaveLength(3));
    expect(c.querySelector('svg.sqc-qr')).toBeTruthy();
    await waitFor(() => expect(c.querySelectorAll('[data-testid="sqc-activity-item"]')).toHaveLength(3));

    const panel = within(c.querySelector('[data-sqc-widget="nft"]') as HTMLElement);
    fireEvent.click(panel.getByRole('button', { name: /Token #2/ }));
    fireEvent.change(panel.getByLabelText(/^To \(SQ/), { target: { value: DEMO_RECIPIENT } });
    fireEvent.click(panel.getByRole('button', { name: /Send NFT #2/ }));
    await waitFor(() => expect(c.querySelectorAll('[data-testid="sqc-nft"][data-owned="true"]')).toHaveLength(2));
    expect(members.ctrl.wallet.calls.some((x) => x.method === 'sendModuleCall')).toBe(true);
  });

  it('validator directory, bridge status and token balances read the mock chain', async () => {
    const spec = specWith([
      block('v', 'validator-list', { maxRows: 10 }),
      block('br', 'bridge-claim', { bridgeAddress: BRIDGE }),
      block('tb', 'token-balances', { tokens: TOKEN }),
    ]);
    const { view, root } = renderSpec(spec);
    await waitFor(() => expect(view.container.querySelectorAll('[data-testid="sqc-validator"]')).toHaveLength(DEMO_VALIDATORS.length));
    expect(root.getByText('3 active · bond 2500 SPLD')).toBeTruthy();
    await waitFor(() => expect(root.getByTestId('sqc-bridge-info').textContent).toContain('Active'));
    const bridge = within(view.container.querySelector('[data-sqc-widget="bridge"]') as HTMLElement);
    fireEvent.change(bridge.getByLabelText('Burn id'), { target: { value: '1' } });
    fireEvent.click(bridge.getByRole('button', { name: 'Check' }));
    await waitFor(() => expect(bridge.getByText('Burn #1 was claimed at block 1050.')).toBeTruthy());
    await waitFor(() => expect(root.getByTestId('sqc-token-balance').textContent).toContain('DEMO'));
  });

  it('x402 paywall demo pays with the wallet and unlocks the content', async () => {
    const spec = specWith([block('pay', 'x402-paywall', { payTo: DEMO_RECIPIENT, price: '0.5', maxPrice: '1', content: 'Secret alpha' })]);
    const { ctrl, root } = renderSpec(spec, '/', '10');
    fireEvent.click(root.getByRole('button', { name: 'Unlock' }));
    expect(root.getByTestId('dapp-x402-quote').textContent).toContain('0.5 SPLD');
    fireEvent.click(root.getByRole('button', { name: 'Pay and unlock' }));
    await waitFor(() => expect(root.getByTestId('dapp-x402-content').textContent).toContain('Secret alpha'));
    const pay = ctrl.wallet.calls.find((x) => x.method === 'payX402')!;
    expect(pay.args[0]).toMatchObject({ to: DEMO_RECIPIENT, amount: String(5n * 10n ** 17n), settle: true, maxAmount: String(10n ** 18n) });
  });

  it('x402 paywall refuses a price above the reader limit', () => {
    const spec = specWith([block('pay', 'x402-paywall', { payTo: DEMO_RECIPIENT, price: '5', maxPrice: '1' })]);
    const { root } = renderSpec(spec);
    fireEvent.click(root.getByRole('button', { name: 'Unlock' }));
    expect(root.getByText(/above your limit of 1 SPLD/)).toBeTruthy();
    expect((root.getByRole('button', { name: 'Pay and unlock' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
