import { cleanup, createEvent, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TEMPLATES, blocksFromTree, createCatalog, instantiateTemplate, type CatalogEntry, type DappSpec } from '@splendor/dapp-spec';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { MockSplendorProvider } from '@splendor/connect-react/testing';
import { createPlaygroundMock } from '../preview/mock';
import { SpecRenderer } from '../preview/SpecRenderer';
import { ContractsPanel, describeTry, parseFunctionsJson, uniqueContractId } from './ContractsPanel';
import { FlowsPanel, collectLinks } from './FlowsPanel';
import { ONBOARDING_FORCE_KEY, ONBOARDING_KEY, Onboarding, shouldShowOnboarding } from './Onboarding';
import { PagesPanel } from './PagesPanel';
import { LayersPanel } from './LayersPanel';
import { PropsForm } from '../components/PropsForm';
import { parseRows, serializeRows } from '../builder/ListEditor';
import { setupItems } from '../builder/issues';
import { initBuilder } from '../builder/state';

const launchpad = (): DappSpec => initBuilder(instantiateTemplate(TEMPLATES.find((t) => t.id === 'token-launchpad')!)).spec;

/** Launchpad plus a button on `/` that links to `/manage`. */
function withLink(): DappSpec {
  const spec = launchpad();
  const home = spec.pages[0]!;
  const block = { id: 'go', widget: 'action-button', props: { label: 'Manage tokens', href: '/manage' } };
  home.blocks = [...(home.blocks ?? []), block];
  const stack = (home.root as { children: { children: unknown[] }[] }).children[0]!;
  stack.children.push({ kind: 'block', block });
  return spec;
}

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe('PagesPanel: duplicate and set home', () => {
  it('dispatches duplicatePage and setHomePage; the home page has no set-home button', () => {
    const dispatch = vi.fn();
    render(<PagesPanel spec={launchpad()} pagePath="/" dispatch={dispatch} newPageTitle={null} setNewPageTitle={() => undefined} />);
    fireEvent.click(screen.getByRole('button', { name: 'Duplicate page Manage' }));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'duplicatePage', path: '/manage' });
    fireEvent.click(screen.getByRole('button', { name: 'Set Manage as home page' }));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'setHomePage', path: '/manage' });
    expect(screen.queryByRole('button', { name: /Set Launch as home page/ })).toBeNull();
    expect(screen.getByTestId('page-is-home')).toBeTruthy();
  });
});

describe('FlowsPanel', () => {
  it('collects internal links only, with the source block id', () => {
    const links = collectLinks(withLink());
    expect(links).toEqual([{ fromPath: '/', fromTitle: 'Launch', fromBlockId: 'go', to: '/manage', label: 'Manage tokens' }]);
    const spec = withLink();
    spec.pages[0]!.blocks!.find((b) => b.id === 'go')!.props.href = 'https://example.com';
    expect(collectLinks(spec)).toEqual([]);
  });

  it('selects the source block when an edge or list entry is clicked', () => {
    const onSelectBlock = vi.fn();
    render(<FlowsPanel spec={withLink()} onSelectBlock={onSelectBlock} />);
    fireEvent.click(screen.getByTestId('flow-edge'));
    expect(onSelectBlock).toHaveBeenLastCalledWith('/', 'go');
    fireEvent.keyDown(screen.getByTestId('flow-edge'), { key: 'Enter' });
    expect(onSelectBlock).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByTestId('flow-link'));
    expect(onSelectBlock).toHaveBeenCalledTimes(3);
  });
});

describe('ContractsPanel Try', () => {
  const spec: DappSpec = {
    ...launchpad(),
    contracts: [{ id: 'counter', name: 'Counter', address: 'SQ:' + 'ab'.repeat(32), functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' }] }],
  };

  it('describes the call without sending anything', () => {
    expect(describeTry({ name: 'Counter', address: 'SQ:1' }, { signature: 'get()', kind: 'read' }, 'mock')).toBe('Would call get() on SQ:1 in mock mode');
    expect(describeTry({ name: 'C', address: ' ' }, { signature: 'inc()', kind: 'write' }, 'live')).toBe('Would call inc() on (no address yet) in live mode');
  });

  it('opens a dialog naming the signature, address and mode', () => {
    render(<ContractsPanel spec={spec} dispatch={() => undefined} onInsertContractBlock={() => undefined} mode="live" />);
    fireEvent.click(screen.getByTestId('contract-try-counter-get'));
    expect(screen.getByTestId('contract-try-result').textContent).toContain('Would call get() on SQ:');
    expect(screen.getByTestId('contract-try-result').textContent).toContain('in live mode');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('contract-try-dialog')).toBeNull();
  });
});

describe('Onboarding', () => {
  it('walks three steps and remembers that it was finished', () => {
    const onRailChange = vi.fn();
    render(<Onboarding onRailChange={onRailChange} />);
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 1 of 3');
    expect(onRailChange).toHaveBeenCalledWith('components');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 2 of 3');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 1 of 3');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-next').textContent).toBe('Done');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.queryByTestId('onboarding')).toBeNull();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe('1');
  });

  it('skip also sets the flag, and a set flag keeps it closed', () => {
    const { unmount } = render(<Onboarding />);
    fireEvent.click(screen.getByTestId('onboarding-skip'));
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe('1');
    unmount();
    render(<Onboarding />);
    expect(screen.queryByTestId('onboarding')).toBeNull();
  });

  it('stays out of the way of browser automation unless forced', () => {
    Object.defineProperty(navigator, 'webdriver', { value: true, configurable: true });
    try {
      expect(shouldShowOnboarding()).toBe(false);
      localStorage.setItem(ONBOARDING_FORCE_KEY, '1');
      expect(shouldShowOnboarding()).toBe(true);
    } finally {
      Object.defineProperty(navigator, 'webdriver', { value: false, configurable: true });
    }
  });
});

describe('FlowsPanel broken links', () => {
  const broken = (): DappSpec => {
    const spec = withLink();
    spec.pages[0]!.blocks!.find((b) => b.id === 'go')!.props.href = '/gone';
    return spec;
  };

  it('keeps links to missing pages and flags them', () => {
    expect(collectLinks(broken())).toEqual([{ fromPath: '/', fromTitle: 'Launch', fromBlockId: 'go', to: '/gone', label: 'Manage tokens', missing: true }]);
  });

  it('scans every action prop and navbar links, not just three widgets', () => {
    const spec = withLink();
    const home = spec.pages[0]!;
    home.blocks!.push(
      { id: 'nav', widget: 'navbar', props: { links: 'Home|/,Manage|/manage,Nowhere|/nowhere', ctaPath: '/manage', ctaLabel: 'Go' } },
      { id: 'x', widget: 'hero-split', props: { ctaPath: '#/manage', ctaLabel: 'Hero go' } },
    );
    const links = collectLinks(spec);
    expect(links.filter((l) => l.fromBlockId === 'nav').map((l) => [l.label, l.to, Boolean(l.missing)])).toEqual([
      ['Go', '/manage', false],
      ['Home', '/', false],
      ['Manage', '/manage', false],
      ['Nowhere', '/nowhere', true],
    ]);
    expect(links.find((l) => l.fromBlockId === 'x')).toMatchObject({ to: '/manage', label: 'Hero go' });
  });

  it('draws a broken edge in red text and says "missing page"', () => {
    const onSelectBlock = vi.fn();
    render(<FlowsPanel spec={broken()} onSelectBlock={onSelectBlock} />);
    expect(screen.queryByTestId('flow-edge')).toBeNull();
    const edge = screen.getByTestId('flow-edge-broken');
    expect(edge.textContent).toContain('missing page');
    expect(edge.getAttribute('aria-label')).toContain('missing page');
    expect(screen.getByTestId('flow-link-missing').textContent).toContain('missing page');
    expect(screen.getByTestId('flows-broken-summary').textContent).toContain('1 link');
    fireEvent.click(edge);
    expect(onSelectBlock).toHaveBeenLastCalledWith('/', 'go');
  });

  it('page nodes are keyboard buttons', () => {
    const onSelectPage = vi.fn();
    render(<FlowsPanel spec={launchpad()} onSelectPage={onSelectPage} />);
    const node = screen.getAllByTestId('flow-page')[1]!;
    expect(node.getAttribute('role')).toBe('button');
    expect(node.getAttribute('tabindex')).toBe('0');
    fireEvent.keyDown(node, { key: 'Enter' });
    fireEvent.keyDown(node, { key: ' ' });
    expect(onSelectPage).toHaveBeenCalledTimes(2);
    expect(onSelectPage).toHaveBeenLastCalledWith('/manage');
  });
});

describe('ContractsPanel validation', () => {
  const address = `SQ:${'ab'.repeat(32)}`;
  const empty: DappSpec = { ...launchpad(), contracts: [] };

  const fill = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label, { exact: true }), { target: { value } });

  it('rejects a bad address inline, with aria-invalid and a description, and adds nothing', () => {
    const dispatch = vi.fn();
    render(<ContractsPanel spec={empty} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fill('Address', 'SQ:123');
    fireEvent.click(screen.getByRole('button', { name: 'Add contract' }));
    const input = screen.getByLabelText('Address');
    expect(input.getAttribute('aria-invalid')).toBe('true');
    const errorId = input.getAttribute('aria-describedby')!;
    expect(document.getElementById(errorId)?.textContent).toMatch(/SQ:/);
    expect(dispatch).not.toHaveBeenCalled();
    // Typing again clears the error.
    fill('Address', address);
    expect(screen.getByLabelText('Address').getAttribute('aria-invalid')).toBeNull();
  });

  it('rejects a malformed function signature', () => {
    const dispatch = vi.fn();
    render(<ContractsPanel spec={empty} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fill('Address', address);
    fill('Function signature', 'transfer');
    fireEvent.click(screen.getByRole('button', { name: 'Add contract' }));
    expect(screen.getByLabelText('Function signature').getAttribute('aria-invalid')).toBe('true');
    expect(dispatch).not.toHaveBeenCalled();
  });

  it('reports invalid ABI JSON instead of failing silently', () => {
    const dispatch = vi.fn();
    render(<ContractsPanel spec={empty} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fill('Address', address);
    fireEvent.change(screen.getByLabelText(/Or functions JSON/), { target: { value: '{nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add contract' }));
    const field = screen.getByLabelText(/Or functions JSON/);
    expect(field.getAttribute('aria-invalid')).toBe('true');
    expect(document.getElementById(field.getAttribute('aria-describedby')!)?.textContent).toMatch(/Not valid JSON/);
    expect(dispatch).not.toHaveBeenCalled();
  });

  it('derives params from the signature and saves a valid contract', () => {
    const dispatch = vi.fn();
    render(<ContractsPanel spec={empty} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fill('Name', 'Token');
    fill('Address', address);
    fill('Function signature', 'transfer(address,uint256)');
    fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'write' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add contract' }));
    expect(dispatch).toHaveBeenCalledWith({
      type: 'setContracts',
      contracts: [
        {
          id: 'token',
          name: 'Token',
          address,
          functions: [{ name: 'transfer', signature: 'transfer(address,uint256)', params: [{ name: 'arg0', type: 'address' }, { name: 'arg1', type: 'uint256' }], kind: 'write' }],
        },
      ],
    });
  });

  it('gives a duplicate name a unique id instead of overwriting', () => {
    const dispatch = vi.fn();
    const spec: DappSpec = { ...launchpad(), contracts: [{ id: 'counter', name: 'Counter', address, functions: [] }, { id: 'counter-2', name: 'Counter', address, functions: [] }] };
    render(<ContractsPanel spec={spec} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fill('Name', 'Counter');
    fill('Address', address);
    fireEvent.click(screen.getByRole('button', { name: 'Add contract' }));
    const saved = dispatch.mock.calls[0]![0].contracts as { id: string }[];
    expect(saved.map((c) => c.id)).toEqual(['counter', 'counter-2', 'counter-3']);
    expect(uniqueContractId('counter', ['counter'])).toBe('counter-2');
  });

  it('asks before removing a contract', async () => {
    const dispatch = vi.fn();
    const spec: DappSpec = { ...launchpad(), contracts: [{ id: 'counter', name: 'Counter', address, functions: [] }] };
    render(<ContractsPanel spec={spec} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove Counter' }));
    expect(screen.getByTestId('confirm-dialog')).toBeTruthy();
    fireEvent.click(screen.getByTestId('confirm-cancel'));
    await waitFor(() => expect(screen.queryByTestId('confirm-dialog')).toBeNull());
    expect(dispatch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove Counter' }));
    fireEvent.click(screen.getByTestId('confirm-ok'));
    await waitFor(() => expect(dispatch).toHaveBeenCalledWith({ type: 'setContracts', contracts: [] }));
  });

  it('edits a contract address in place, validating it first', () => {
    const dispatch = vi.fn();
    const spec: DappSpec = { ...launchpad(), contracts: [{ id: 'token', name: 'Token', address: '', kind: 'token', functions: [] }] };
    render(<ContractsPanel spec={spec} dispatch={dispatch} onInsertContractBlock={() => undefined} />);
    const input = screen.getByRole('textbox', { name: 'Token address' });
    fireEvent.change(input, { target: { value: 'SQ:nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(dispatch).not.toHaveBeenCalled();
    expect(input.getAttribute('aria-invalid')).toBe('true');
    fireEvent.change(input, { target: { value: address } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(dispatch).toHaveBeenCalledWith({ type: 'setContractAddress', id: 'token', address });
  });

  it('announces the Try result as a status', () => {
    const spec: DappSpec = { ...launchpad(), contracts: [{ id: 'c', name: 'C', address, functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' }] }] };
    render(<ContractsPanel spec={spec} dispatch={() => undefined} onInsertContractBlock={() => undefined} />);
    fireEvent.click(screen.getByTestId('contract-try-c-get'));
    const result = screen.getByTestId('contract-try-result');
    expect(result.getAttribute('role')).toBe('status');
    expect(result.getAttribute('aria-live')).toBe('polite');
  });

  describe('parseFunctionsJson', () => {
    it('accepts the plain format and fills params from the signature', () => {
      const r = parseFunctionsJson('[{"name":"get","signature":"get()","kind":"read"},{"name":"set","signature":"set(uint256 value)","kind":"write"}]');
      expect(r.error).toBeNull();
      expect(r.functions[1]).toMatchObject({ kind: 'write', params: [{ name: 'value', type: 'uint256' }] });
    });

    it('maps an sqc-solc style ABI: view/pure read, everything else write; skips non-functions', () => {
      const r = parseFunctionsJson(
        JSON.stringify([
          { type: 'constructor', inputs: [] },
          { type: 'function', name: 'balanceOf', inputs: [{ name: 'who', type: 'address' }], outputs: [{ type: 'uint256' }], stateMutability: 'view' },
          { name: 'ping', inputs: [], stateMutability: 'pure' },
          { type: 'function', name: 'mint', inputs: [{ name: 'to', type: 'address' }, { name: 'n', type: 'uint256' }], stateMutability: 'nonpayable' },
        ]),
      );
      expect(r.error).toBeNull();
      expect(r.functions.map((f) => [f.signature, f.kind])).toEqual([
        ['balanceOf(address)', 'read'],
        ['ping()', 'read'],
        ['mint(address,uint256)', 'write'],
      ]);
      expect(r.functions[0]).toMatchObject({ params: [{ name: 'who', type: 'address' }], returns: 'uint256' });
    });

    it.each([
      ['not json', /Not valid JSON/],
      ['{"a":1}', /array/],
      ['[]', /No functions/],
      ['[1]', /Entry 1/],
      ['[{"name":"x","signature":"x(","kind":"read"}]', /signature/],
      ['[{"name":"x","signature":"x()","kind":"maybe"}]', /kind/],
      ['[{"name":"x"}]', /Entry 1/],
      ['[{"type":"function","name":"t","inputs":[{"type":"tuple","components":[{"type":"uint256"}]}],"stateMutability":"view"}]', /tuple/],
    ])('rejects %s', (text, message) => {
      const r = parseFunctionsJson(text);
      expect(r.error).toMatch(message);
      expect(r.functions).toEqual([]);
    });
  });
});

describe('Onboarding focus', () => {
  it('takes focus when shown and Escape skips it', () => {
    render(<Onboarding />);
    const card = screen.getByTestId('onboarding');
    expect(document.activeElement).toBe(card);
    fireEvent.keyDown(card, { key: 'Escape' });
    expect(screen.queryByTestId('onboarding')).toBeNull();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe('1');
  });
});

describe('SpecRenderer', () => {
  const catalog = createCatalog(WIDGET_MANIFESTS);
  const renderSpec = (props: Omit<Parameters<typeof SpecRenderer>[0], 'catalog'>) =>
    render(
      <MockSplendorProvider controller={createPlaygroundMock({ network: 'testnet', state: 'connected' })}>
        <SpecRenderer catalog={catalog} {...props} />
      </MockSplendorProvider>,
    );

  it('applies block style and hide-on to leaf blocks and wraps the tree like exports', () => {
    const spec = withLink();
    const node = (spec.pages[0]!.root as { children: { children: { kind: string; block?: { id: string; style?: unknown } }[] }[] }).children[0]!.children.find((c) => c.block?.id === 'go')!;
    node.block!.style = { maxWidth: '300px', hideOn: 'mobile' };
    const { container } = renderSpec({ spec, pagePath: '/' });
    const leaf = container.querySelector<HTMLElement>('[data-block-id="go"]')!;
    expect(leaf.style.maxWidth).toBe('300px');
    expect(leaf.className).toContain('dapp-hide-mobile');
    expect(container.querySelector('main > .dapp-page')).not.toBeNull();
    expect(container.querySelector('main.dapp-grid')).toBeNull();
  });

  it('navigates inside the dApp for #/path and plain /path links to existing pages', () => {
    const onNavigate = vi.fn();
    const spec = withLink();
    const { container } = renderSpec({ spec, pagePath: '/', onNavigate });
    const link = container.querySelector<HTMLAnchorElement>('[data-block-id="go"] a')!;
    expect(link.getAttribute('href')).toBe('#/manage');
    expect(fireEvent.click(link)).toBe(false); // preventDefault was called
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(onNavigate).toHaveBeenLastCalledWith('/manage');

    link.setAttribute('href', '/manage');
    fireEvent.click(link);
    expect(onNavigate).toHaveBeenCalledTimes(2);
    expect(onNavigate).toHaveBeenLastCalledWith('/manage');

    link.setAttribute('href', '/not-a-page');
    expect(fireEvent.click(link)).toBe(true); // left alone
    link.setAttribute('href', 'https://example.com/manage');
    expect(fireEvent.click(link)).toBe(true);
    expect(onNavigate).toHaveBeenCalledTimes(2);
  });
});

describe('LayersPanel', () => {
  const catalog = createCatalog(WIDGET_MANIFESTS);
  const noop = () => undefined;
  const props = () => ({
    catalog,
    onSelect: vi.fn(),
    onDuplicate: vi.fn(),
    onRemove: vi.fn(),
    onMove: vi.fn(),
    onDrop: vi.fn(),
    acceptsDrag: () => true,
    startDrag: vi.fn(),
    endDrag: noop,
  });

  it('lists exactly one <li> per component, with containers as group headers', () => {
    const spec = launchpad();
    const page = spec.pages[0]!;
    render(<LayersPanel page={page} selectedId={null} {...props()} />);
    const tree = screen.getByTestId('block-list');
    expect(tree.querySelectorAll('li')).toHaveLength(blocksFromTree(page.root).length);
    expect(tree.querySelectorAll('[data-kind="section"], [data-kind="stack"]').length).toBeGreaterThan(0);
    // The row is presentational; its tree item holds no buttons.
    expect(tree.querySelector('li')!.getAttribute('role')).toBe('none');
    const item = tree.querySelector('li [role="treeitem"]')!;
    expect(item.querySelector('button')).toBeNull();
    expect(tree.querySelectorAll('[role="treeitem"][tabindex="0"]')).toHaveLength(1);
  });

  it('duplicates a component with Ctrl+D on its tree item', () => {
    const page = launchpad().pages[0]!;
    const p = props();
    const first = blocksFromTree(page.root)[0]!;
    render(<LayersPanel page={page} selectedId={null} {...p} />);
    const item = screen.getByTestId('block-list').querySelector<HTMLElement>('li [data-layer-select]')!;
    fireEvent.keyDown(item, { key: 'd', ctrlKey: true });
    expect(p.onDuplicate).toHaveBeenLastCalledWith(first.id);
    fireEvent.keyDown(item, { key: 'Enter' });
    expect(p.onSelect).toHaveBeenLastCalledWith(first.id);
  });

  it('selects on click (leaf and container) and marks the selection', () => {
    const spec = launchpad();
    const page = spec.pages[0]!;
    const p = props();
    const first = blocksFromTree(page.root)[0]!;
    render(<LayersPanel page={page} selectedId={first.id} {...p} />);
    expect(screen.getByTestId('block-list').querySelectorAll('li.is-active')).toHaveLength(1);
    fireEvent.click(screen.getAllByRole('treeitem', { name: 'Page' })[0]!);
    expect(p.onSelect).toHaveBeenLastCalledWith((page.root as { id: string }).id);
  });

  it('duplicates and removes through named buttons', () => {
    const spec = launchpad();
    const page = spec.pages[0]!;
    const p = props();
    const first = blocksFromTree(page.root)[0]!;
    const title = catalog.find((e) => e.id === first.widget)?.title ?? first.widget;
    render(<LayersPanel page={page} selectedId={null} {...p} />);
    fireEvent.click(screen.getAllByRole('button', { name: `Duplicate ${title}` })[0]!);
    expect(p.onDuplicate).toHaveBeenLastCalledWith(first.id);
    fireEvent.click(screen.getAllByRole('button', { name: `Remove ${title}` })[0]!);
    expect(p.onRemove).toHaveBeenLastCalledWith(first.id);
  });

  it('keyboard: arrows move focus, Alt+Arrow moves within the parent, Delete removes', () => {
    const spec = launchpad();
    const page = spec.pages[0]!;
    const p = props();
    const blocks = blocksFromTree(page.root);
    expect(blocks.length).toBeGreaterThan(1);
    render(<LayersPanel page={page} selectedId={null} {...p} />);
    const rows = [...screen.getByTestId('block-list').querySelectorAll<HTMLElement>('li [data-layer-select]')];
    rows[0]!.focus();
    fireEvent.keyDown(rows[0]!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(rows[1]);
    fireEvent.keyDown(rows[1]!, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(rows[0]);

    // First child moves later: slot after its next sibling (slots count before the move).
    fireEvent.keyDown(rows[0]!, { key: 'ArrowDown', altKey: true });
    expect(p.onMove).toHaveBeenLastCalledWith(expect.objectContaining({ id: blocks[0]!.id, index: 2 }));
    // The first child cannot move earlier.
    p.onMove.mockClear();
    fireEvent.keyDown(rows[0]!, { key: 'ArrowUp', altKey: true });
    expect(p.onMove).not.toHaveBeenCalled();
    fireEvent.keyDown(rows[1]!, { key: 'ArrowUp', altKey: true });
    expect(p.onMove).toHaveBeenLastCalledWith(expect.objectContaining({ id: blocks[1]!.id, index: 0 }));

    fireEvent.keyDown(rows[0]!, { key: 'Delete' });
    expect(p.onRemove).toHaveBeenLastCalledWith(blocks[0]!.id);
  });

  it('dropping next to a component hands the studio its parent and slot', () => {
    const spec = launchpad();
    const page = spec.pages[0]!;
    const p = props();
    render(<LayersPanel page={page} selectedId={null} {...p} />);
    const li = screen.getByTestId('block-list').querySelector('li')!;
    li.getBoundingClientRect = () => ({ top: 0, height: 20, left: 0, right: 100, bottom: 20, width: 100, x: 0, y: 0, toJSON: () => ({}) });
    const dataTransfer = { getData: () => '', types: [] as string[], dropEffect: 'move', effectAllowed: 'all' };
    // jsdom has no DragEvent: give the plain event the pointer position the handler reads.
    const dropAt = (clientY: number) => {
      const event = createEvent.drop(li, { dataTransfer });
      Object.defineProperty(event, 'clientY', { value: clientY });
      fireEvent(li, event);
    };
    dropAt(2);
    expect(p.onDrop).toHaveBeenLastCalledWith(expect.objectContaining({ index: 0, moveId: null }));
    dropAt(18);
    expect(p.onDrop).toHaveBeenLastCalledWith(expect.objectContaining({ index: 1 }));
  });
});

describe('data pickers in PropsForm', () => {
  const address = `SQ:${'ab'.repeat(32)}`;
  const entry = (contractKind: 'token' | 'contract' | 'any'): CatalogEntry => ({
    id: 'probe',
    title: 'Probe',
    description: '',
    category: 'modules',
    kind: 'block',
    importName: 'Probe',
    status: 'stable',
    placeable: true,
    defaults: {},
    props: [
      { name: 'contractId', label: 'Contract', type: 'string', format: 'contract', contractKind, addressProp: 'contract' },
      { name: 'contract', label: 'Contract address', type: 'string', format: 'address' },
      { name: 'functionSig', label: 'Function', type: 'string', format: 'function', contractProp: 'contractId', functionKind: 'read' },
      {
        name: 'items',
        label: 'Allocation',
        type: 'string',
        format: 'list',
        columns: [
          { key: 'label', label: 'Group' },
          { key: 'percent', label: 'Share' },
        ],
      },
    ],
  });
  const contracts = [
    { id: 'tok', name: 'MyToken', address, kind: 'token' as const, functions: [] },
    {
      id: 'ctr',
      name: 'Counter',
      address,
      functions: [
        { name: 'get', signature: 'get()', params: [], kind: 'read' as const },
        { name: 'set', signature: 'set(uint256)', params: [{ name: 'arg0', type: 'uint256' }], kind: 'write' as const },
      ],
    },
  ];
  const optionsOf = (select: HTMLElement) => [...select.querySelectorAll('option')].map((o) => o.textContent);

  it('offers only matching contracts, with "— none —" and an Add contract button', () => {
    const onChange = vi.fn();
    const onOpenContracts = vi.fn();
    const view = render(<PropsForm entry={entry('token')} values={{}} onChange={onChange} contracts={contracts} onOpenContracts={onOpenContracts} />);
    const select = screen.getByLabelText('Contract', { exact: true });
    expect(optionsOf(select)).toEqual(['— none —', 'MyToken']);
    fireEvent.change(select, { target: { value: 'tok' } });
    expect(onChange).toHaveBeenLastCalledWith('contractId', 'tok');
    fireEvent.click(screen.getByRole('button', { name: '+ Add contract' }));
    expect(onOpenContracts).toHaveBeenCalledTimes(1);
    view.unmount();
    render(<PropsForm entry={entry('contract')} values={{}} onChange={onChange} contracts={contracts} />);
    expect(optionsOf(screen.getByLabelText('Contract', { exact: true }))).toEqual(['— none —', 'Counter']);
    expect(screen.queryByRole('button', { name: '+ Add contract' })).toBeNull();
  });

  it('lists the chosen contract\'s functions of the right kind and hides the raw address', () => {
    const onChange = vi.fn();
    render(<PropsForm entry={entry('any')} values={{ contractId: 'ctr' }} onChange={onChange} contracts={contracts} />);
    const fn = screen.getByLabelText('Function', { exact: true });
    expect(fn.tagName).toBe('SELECT');
    expect(optionsOf(fn)).toEqual(['— pick a function —', 'get() · read']);
    fireEvent.change(fn, { target: { value: 'get()' } });
    expect(onChange).toHaveBeenLastCalledWith('functionSig', 'get()');
    expect(screen.getByText('Address comes from Counter')).toBeTruthy();
    expect(screen.queryByLabelText('Contract address')).toBeNull();
  });

  it('keeps the raw address field when it already has a value, and the text input when there is no function list', () => {
    render(<PropsForm entry={entry('any')} values={{ contractId: 'tok', contract: address }} onChange={() => undefined} contracts={contracts} />);
    expect(screen.getByLabelText('Contract address')).toBeTruthy();
    expect(screen.queryByText(/Address comes from/)).toBeNull();
    // MyToken has no functions: the signature stays a plain text field.
    expect(screen.getByLabelText('Function', { exact: true }).tagName).toBe('INPUT');
  });

  it('falls back to text fields without a contracts list (widget gallery)', () => {
    render(<PropsForm entry={entry('any')} values={{}} onChange={() => undefined} />);
    expect(screen.getByLabelText('Contract', { exact: true }).tagName).toBe('INPUT');
  });

  it('edits list rows: labelled inputs, add, remove, reorder, newline + pipe storage', () => {
    const onChange = vi.fn();
    render(<PropsForm entry={entry('any')} values={{ items: 'Core|40\nTreasury|60' }} onChange={onChange} contracts={contracts} />);
    expect((screen.getByLabelText('Group row 1') as HTMLInputElement).value).toBe('Core');
    expect((screen.getByLabelText('Share row 2') as HTMLInputElement).value).toBe('60');
    fireEvent.change(screen.getByLabelText('Share row 1'), { target: { value: '45' } });
    expect(onChange).toHaveBeenLastCalledWith('items', 'Core|45\nTreasury|60');
    fireEvent.click(screen.getByRole('button', { name: 'Move row 2 up' }));
    expect(onChange).toHaveBeenLastCalledWith('items', 'Treasury|60\nCore|45');
    fireEvent.click(screen.getByRole('button', { name: 'Remove row 1' }));
    expect(onChange).toHaveBeenLastCalledWith('items', 'Core|45');
    fireEvent.click(screen.getByRole('button', { name: '+ Add row' }));
    expect(screen.getByLabelText('Group row 2')).toBeTruthy();
  });

  it('gives old comma-separated props a row editor, with page picks for navbar links', () => {
    const navbar: CatalogEntry = { ...entry('any'), id: 'navbar', props: [{ name: 'links', label: 'Links', type: 'string', format: 'text' }] };
    const onChange = vi.fn();
    render(
      <PropsForm
        entry={navbar}
        values={{ links: 'Home|/,About|/about' }}
        onChange={onChange}
        pages={[
          { path: '/', title: 'Home' },
          { path: '/about', title: 'About' },
          { path: '/pricing', title: 'Pricing' },
        ]}
      />,
    );
    expect((screen.getByLabelText('Label row 2') as HTMLInputElement).value).toBe('About');
    const link = screen.getByLabelText('Link row 2') as HTMLSelectElement;
    expect(link.value).toBe('/about');
    fireEvent.change(link, { target: { value: '/pricing' } });
    expect(onChange).toHaveBeenLastCalledWith('links', 'Home|/\nAbout|/pricing');
  });

  it('parses and writes rows the way the blocks read them', () => {
    expect(parseRows('Fast|Ship quickly,Secure|Post-quantum', 2, 'auto')).toEqual([['Fast', 'Ship quickly'], ['Secure', 'Post-quantum']]);
    expect(parseRows('a, b\nc|d', 2, 'auto')).toEqual([['a, b', ''], ['c', 'd']]);
    expect(parseRows('1,2,3', 1, 'auto')).toEqual([['1'], ['2'], ['3']]);
    expect(serializeRows([['a', 'b'], ['', ''], ['c', '']], 'newline')).toBe('a|b\nc|');
    // A lone row with a comma must not be read as two rows: the trailing newline switches the rule.
    expect(serializeRows([['Fast', 'Ship, quickly']], 'auto')).toBe('Fast|Ship, quickly\n');
  });
});

describe('page settings in PagesPanel', () => {
  it('sets who can see a page, nav visibility and order for the selected page', () => {
    const dispatch = vi.fn();
    render(<PagesPanel spec={launchpad()} pagePath="/manage" dispatch={dispatch} newPageTitle={null} setNewPageTitle={() => undefined} />);
    const access = screen.getByLabelText('Who can see this page') as HTMLSelectElement;
    expect([...access.options].map((o) => o.text)).toEqual(['Public', 'Wallet connected', 'Signed in']);
    fireEvent.change(access, { target: { value: 'wallet' } });
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'setPageAccess', path: '/manage', access: 'wallet' });
    fireEvent.click(screen.getByLabelText('Show in navigation'));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'setNavVisible', path: '/manage', visible: false });
    fireEvent.click(screen.getByRole('button', { name: /Move page Manage earlier/ }));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'movePage', path: '/manage', delta: -1 });
    expect((screen.getByRole('button', { name: /Move page Manage later/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('keeps the home page public', () => {
    render(<PagesPanel spec={launchpad()} pagePath="/" dispatch={() => undefined} newPageTitle={null} setNewPageTitle={() => undefined} />);
    expect((screen.getByLabelText('Who can see this page') as HTMLSelectElement).disabled).toBe(true);
    expect(screen.getByText('The home page is always public')).toBeTruthy();
    expect(within(screen.getByTestId('page-settings')).getByLabelText('Show in navigation')).toBeTruthy();
  });
});

describe('setupItems (Finish setup)', () => {
  const entry: CatalogEntry = {
    id: 'probe',
    title: 'Probe',
    description: '',
    category: 'modules',
    kind: 'block',
    importName: 'Probe',
    status: 'stable',
    placeable: true,
    defaults: {},
    props: [
      { name: 'contractId', label: 'Token', type: 'string', format: 'contract', contractKind: 'token', addressProp: 'token' },
      { name: 'token', label: 'Token address', type: 'string', format: 'address', required: true },
    ],
  };

  it('lists contracts without an address and components with nothing bound', () => {
    const spec = launchpad();
    spec.contracts = [
      { id: 'tok', name: 'My token', kind: 'token', address: '', functions: [] },
      { id: 'ctr', name: 'Counter', address: ' ', functions: [] },
      { id: 'ok', name: 'Fine', address: `SQ:${'ab'.repeat(32)}`, functions: [] },
    ];
    const items = setupItems(spec, [entry]);
    expect(items.map((i) => i.message)).toEqual(['Add your token address', 'Add an address for Counter']);
    expect(items.every((i) => i.kind === 'contracts')).toBe(true);
  });

  it('flags a component that needs a token, until a contract is picked or an address typed', () => {
    const spec = launchpad();
    spec.contracts = [];
    const page = spec.pages[0]!;
    page.blocks = [...(page.blocks ?? []), { id: 'probe-1', widget: 'probe', props: {} }];
    const stack = (page.root as { children: { children: unknown[] }[] }).children[0]!;
    stack.children.push({ kind: 'block', block: page.blocks[page.blocks.length - 1] });
    const [item] = setupItems(spec, [entry]);
    expect(item).toMatchObject({ kind: 'component', message: 'Probe needs a token.', target: { blockId: 'probe-1', prop: 'contractId' } });
    page.blocks[page.blocks.length - 1]!.props = { contractId: 'tok' };
    expect(setupItems(spec, [entry])).toEqual([]);
    page.blocks[page.blocks.length - 1]!.props = { token: `SQ:${'ab'.repeat(32)}` };
    expect(setupItems(spec, [entry])).toEqual([]);
  });
});