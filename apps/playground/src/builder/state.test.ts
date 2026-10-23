import { describe, expect, it, vi } from 'vitest';
import { blocksFromTree, emptySpec, findNode, getTemplate, instantiateTemplate, validateSpec, type ContainerNode, type DappSpec } from '@splendor/dapp-spec';
import { MAX_BLOCKS_PER_PAGE, builderReducer, initBuilder, type BuilderAction, type BuilderState } from './state';

const run = (s: BuilderState, ...actions: BuilderAction[]) => actions.reduce(builderReducer, s);

describe('builderReducer', () => {
  it('repairs invalid ids from older drafts on load (e.g. `/dashboard-hero`)', () => {
    const spec = emptySpec();
    const root: ContainerNode = {
      kind: 'section',
      id: '/dashboard-root',
      children: [
        { kind: 'block', block: { id: '/dashboard-hero', widget: 'hero', props: {} } },
        { kind: 'block', block: { id: 'dashboard-hero', widget: 'hero', props: {} } },
      ],
    };
    spec.pages.push({ path: '/dashboard', title: 'Dashboard', root, blocks: blocksFromTree(root) });
    const page = initBuilder(spec).spec.pages[1]!;
    const ids = page.blocks!.map((b) => b.id);
    expect(ids.every((id) => /^[A-Za-z0-9_-]{1,64}$/.test(id))).toBe(true);
    expect(new Set(ids).size).toBe(ids.length);
    expect((page.root as ContainerNode).id).toBe('dashboard-root');
  });

  it('adds pages from templates with valid ids', () => {
    const s = run(initBuilder(emptySpec()), { type: 'addPageFromTemplate', title: 'Dashboard', templateId: 'dashboard' });
    const page = s.spec.pages.find((p) => p.path === '/dashboard')!;
    expect(page.blocks!.map((b) => b.id)).toEqual(['dashboard-hero', 'dashboard-account', 'dashboard-pending']);
  });

  it('adds blocks with unique ids and selects them', () => {
    const s = run(initBuilder(emptySpec()), { type: 'addBlock', widget: 'transfer', props: {} }, { type: 'addBlock', widget: 'transfer', props: {} });
    const ids = s.spec.pages[0]!.blocks!.map((b) => b.id);
    expect(ids).toEqual(['hero', 'transfer-1', 'transfer-2']);
    expect(s.selectedBlockId).toBe('transfer-2');
  });

  it('undoes and redoes structural edits', () => {
    const a = run(initBuilder(emptySpec()), { type: 'addBlock', widget: 'transfer', props: {} });
    const b = run(a, { type: 'undo' });
    expect(b.spec.pages[0]!.blocks!).toHaveLength(1);
    expect(b.selectedBlockId).toBeNull();
    const c = run(b, { type: 'redo' });
    expect(c.spec.pages[0]!.blocks!).toHaveLength(2);
  });

  it('coalesces rapid edits of the same prop into one undo step', () => {
    vi.useFakeTimers();
    let s = initBuilder(emptySpec());
    for (const ch of ['H', 'He', 'Hel', 'Hell', 'Hello']) s = builderReducer(s, { type: 'setProp', id: 'hero', name: 'heading', value: ch });
    expect(s.past).toHaveLength(1);
    vi.advanceTimersByTime(1500);
    s = builderReducer(s, { type: 'setProp', id: 'hero', name: 'heading', value: 'Hello!' });
    expect(s.past).toHaveLength(2);
    s = builderReducer(s, { type: 'undo' });
    expect(s.spec.pages[0]!.blocks![0]!.props.heading).toBe('Hello');
    vi.useRealTimers();
  });

  it('adds pages with safe unique paths and keeps the home page', () => {
    let s = run(initBuilder(emptySpec()), { type: 'addPage', title: 'About Us!' }, { type: 'addPage', title: 'About Us!' });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/about-us', '/about-us-2']);
    expect(s.pagePath).toBe('/about-us-2');
    s = run(s, { type: 'removePage', path: '/' }, { type: 'removePage', path: '/about-us-2' });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/about-us']);
    expect(s.pagePath).toBe('/');
  });

  it('gives every added page (blank or template) unique node ids, so the spec stays valid', () => {
    const s = run(
      initBuilder(emptySpec()),
      { type: 'addPage', title: 'One' },
      { type: 'addPage', title: 'Two' },
      { type: 'addPageFromTemplate', title: 'Dash', templateId: 'dashboard' },
      { type: 'addPageFromTemplate', title: 'Dash', templateId: 'dashboard' },
    );
    const ids = s.spec.pages.flatMap((p) => {
      const out: string[] = [];
      const visit = (n: ContainerNode | { kind: 'block'; block: { id: string } }) => {
        if (n.kind === 'block') out.push(n.block.id);
        else {
          out.push(n.id);
          n.children.forEach((c) => visit(c as ContainerNode));
        }
      };
      visit(p.root as ContainerNode);
      return out;
    });
    expect(new Set(ids).size).toBe(ids.length);
    expect(validateSpec(s.spec).ok).toBe(true);
  });

  it('enforces the page limit in the reducer, not only in the UI', () => {
    let s = initBuilder(emptySpec());
    for (let i = 0; i < 30; i++) s = builderReducer(s, { type: 'addPage', title: `P${i}` });
    expect(s.spec.pages).toHaveLength(20);
  });

  it('clears links (buttons, navbar items, nav order) to a page when it is deleted', () => {
    let s = run(
      initBuilder(emptySpec()),
      { type: 'addPage', title: 'About' },
      { type: 'selectPage', path: '/' },
      { type: 'addBlock', widget: 'action-button', props: { label: 'About', href: '/about' } },
      { type: 'addBlock', widget: 'navbar', props: { links: 'Home|/,About|/about' } },
    );
    s = { ...s, spec: { ...s.spec, nav: { links: ['/', '/about'] } } };
    s = run(s, { type: 'removePage', path: '/about' });
    const home = s.spec.pages[0]!.blocks!;
    expect(home.find((b) => b.widget === 'action-button')!.props.href).toBe('');
    expect(home.find((b) => b.widget === 'navbar')!.props.links).toBe('Home|/');
    expect(s.spec.nav!.links).toEqual(['/']);
  });

  it('keeps the style when duplicating a block', () => {
    const spec = instantiateTemplate(getTemplate('wallet-dashboard')!);
    let s = run(initBuilder(spec), { type: 'setBlockStyle', id: 'send', style: { padding: '2rem' } });
    s = run(s, { type: 'duplicateBlock', id: 'send' });
    expect(s.spec.pages[0]!.blocks!.find((b) => b.id === 'transfer-1')!.style).toEqual({ padding: '2rem' });
  });

  it('moves and removes blocks on the current page', () => {
    const spec = instantiateTemplate(getTemplate('wallet-dashboard')!);
    let s = run(initBuilder(spec), { type: 'moveBlock', id: 'send', delta: -1 });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['hero', 'send', 'account', 'pending']);
    s = run(s, { type: 'selectBlock', id: 'send' }, { type: 'removeBlock', id: 'send' });
    expect(s.selectedBlockId).toBeNull();
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['hero', 'account', 'pending']);
  });

  it('duplicates a block right after the original with its own id and props', () => {
    const spec = instantiateTemplate(getTemplate('wallet-dashboard')!);
    let s = run(initBuilder(spec), { type: 'duplicateBlock', id: 'send' });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['hero', 'account', 'send', 'transfer-1', 'pending']);
    expect(s.selectedBlockId).toBe('transfer-1');
    s = run(s, { type: 'setProp', id: 'transfer-1', name: 'defaultAmount', value: '5' });
    expect(s.spec.pages[0]!.blocks!.find((b) => b.id === 'send')!.props.defaultAmount).toBe('0.01');
    s = run(s, { type: 'undo' }, { type: 'undo' });
    expect(s.spec.pages[0]!.blocks!).toHaveLength(4);
  });

  it('inserts a block at an index and reorders by dropping', () => {
    let s = run(initBuilder(emptySpec()), { type: 'insertBlock', widget: 'transfer', props: { a: '1' }, index: 0 });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['transfer-1', 'hero']);
    expect(s.selectedBlockId).toBe('transfer-1');
    s = run(s, { type: 'reorderBlock', id: 'hero', index: 0 });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['hero', 'transfer-1']);
    expect(run(s, { type: 'reorderBlock', id: 'hero', index: 0 })).toBe(s);
    s = run(s, { type: 'reorderBlock', id: 'hero', index: 2 });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['transfer-1', 'hero']);
  });

  it('reorders pages', () => {
    const spec = instantiateTemplate(getTemplate('token-launchpad')!);
    const s = run(initBuilder(spec), { type: 'movePage', path: '/manage', delta: -1 });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/manage', '/']);
    expect(run(s, { type: 'movePage', path: '/manage', delta: -1 })).toBe(s);
  });

  it('clears a prop when set to undefined', () => {
    const s = run(initBuilder(emptySpec()), { type: 'setProp', id: 'hero', name: 'text', value: undefined });
    expect('text' in s.spec.pages[0]!.blocks![0]!.props).toBe(false);
  });
});

describe('setContractAddress', () => {
  it('changes the contract once: bound components follow, and raw widget props holding the old address too', () => {
    const mine = `SQ:${'ab'.repeat(32)}`;
    const s0 = initBuilder(instantiateTemplate(getTemplate('token-project')!));
    const old = s0.spec.contracts!.find((c) => c.id === 'token')!.address;
    const s = builderReducer(s0, { type: 'setContractAddress', id: 'token', address: mine });
    expect(s.spec.contracts!.find((c) => c.id === 'token')!.address).toBe(mine);
    const blocks = s.spec.pages.flatMap((p) => p.blocks ?? []);
    expect(blocks.find((b) => b.id === 'send-token')!.props.tokenAddress).toBe(mine);
    expect(blocks.find((b) => b.id === 'stats')!.props).toMatchObject({ contractId: 'token' });
    expect(JSON.stringify(s.spec.pages)).not.toContain(old);
    expect(builderReducer(s, { type: 'undo' }).spec.contracts!.find((c) => c.id === 'token')!.address).toBe(old);
    expect(builderReducer(s, { type: 'setContractAddress', id: 'ghost', address: mine })).toBe(s);
  });
});

describe('tree edits (in-canvas drag/drop)', () => {
  const block = (id: string) => ({ kind: 'block' as const, block: { id, widget: 'transfer', props: {} } });
  const nested = (): DappSpec => {
    const spec = emptySpec();
    const root: ContainerNode = {
      kind: 'section',
      id: 'r',
      children: [
        { kind: 'row', id: 'row-1', children: [] },
        { kind: 'column', id: 'col-1', children: [block('a'), block('b')] },
      ],
    };
    spec.pages[0]!.root = root;
    spec.pages[0]!.blocks = blocksFromTree(root);
    return spec;
  };
  const root = (s: BuilderState) => s.spec.pages[0]!.root as ContainerNode;
  const ids = (s: BuilderState) => (s.spec.pages[0]!.blocks ?? []).map((b) => b.id);
  const childIds = (s: BuilderState, id: string) => (findNode(root(s), id) as ContainerNode).children.map((c) => (c.kind === 'block' ? c.block.id : c.id));

  it('keeps nested containers when loading and when flat edits happen', () => {
    let s = initBuilder(nested());
    expect(root(s).children.map((c) => c.kind)).toEqual(['row', 'column']);
    s = run(s, { type: 'addBlock', widget: 'transfer', props: {} });
    expect(root(s).children).toHaveLength(2);
    expect(ids(s)).toEqual(['a', 'b', 'transfer-1']);
    expect(s.selectedBlockId).toBe('transfer-1');
  });

  it('inserts layout shells as real empty containers (columns → row of N columns)', () => {
    let s = initBuilder(emptySpec());
    const stackId = ((s.spec.pages[0]!.root as ContainerNode).children[0] as ContainerNode).id;
    s = run(s, { type: 'insertIntoTree', parentId: stackId, index: 0, widget: 'section', props: {} });
    const section = findNode(s.spec.pages[0]!.root, 'section-1') as ContainerNode;
    expect(section.kind).toBe('section');
    expect(section.children).toEqual([]);
    s = run(s, { type: 'insertIntoTree', parentId: 'section-1', index: 0, widget: 'columns', props: { cols: '2' } });
    const cols = findNode(s.spec.pages[0]!.root, 'columns-1') as ContainerNode;
    expect(cols.kind).toBe('row');
    expect(cols.children).toHaveLength(2);
    expect(cols.children.every((c) => c.kind === 'column')).toBe(true);
  });

  it('inserts a widget into an empty container', () => {
    const s = run(initBuilder(nested()), { type: 'insertIntoTree', parentId: 'row-1', index: 0, widget: 'transfer', props: {} });
    expect(childIds(s, 'row-1')).toEqual(['transfer-1']);
    expect(s.selectedBlockId).toBe('transfer-1');
    expect(ids(s)).toEqual(['transfer-1', 'a', 'b']);
    expect(run(s, { type: 'insertIntoTree', parentId: 'nope', index: 0, widget: 'transfer', props: {} })).toBe(s);
  });

  it('moves a node between containers and within one (index is the pre-move slot)', () => {
    let s = run(initBuilder(nested()), { type: 'moveNode', id: 'a', parentId: 'row-1', index: 0 });
    expect(childIds(s, 'row-1')).toEqual(['a']);
    expect(childIds(s, 'col-1')).toEqual(['b']);
    s = run(initBuilder(nested()), { type: 'moveNode', id: 'a', parentId: 'col-1', index: 2 });
    expect(childIds(s, 'col-1')).toEqual(['b', 'a']);
    expect(run(s, { type: 'moveNode', id: 'a', parentId: 'col-1', index: 2 })).toBe(s);
    expect(s.past).toHaveLength(1);
  });

  it('refuses to move a container into itself or the page root away', () => {
    const s = initBuilder(nested());
    expect(run(s, { type: 'moveNode', id: 'col-1', parentId: 'col-1', index: 0 })).toBe(s);
    expect(run(s, { type: 'moveNode', id: 'r', parentId: 'row-1', index: 0 })).toBe(s);
  });

  it('duplicates blocks and containers with fresh ids', () => {
    let s = run(initBuilder(nested()), { type: 'duplicateNode', id: 'col-1' });
    expect(root(s).children.map((c) => c.kind)).toEqual(['row', 'column', 'column']);
    expect(new Set(ids(s)).size).toBe(4);
    const copy = root(s).children[2] as ContainerNode;
    expect(copy.id).not.toBe('col-1');
    expect(s.selectedBlockId).toBe(copy.id);
    s = run(initBuilder(nested()), { type: 'duplicateNode', id: 'a' });
    expect(ids(s)).toEqual(['a', 'transfer-1', 'b']);
    expect(run(s, { type: 'duplicateNode', id: 'r' })).toBe(s);
  });

  it('removes nodes, clears the selection, and never removes the page root', () => {
    let s = run(initBuilder(nested()), { type: 'selectBlock', id: 'col-1' }, { type: 'removeNodeFromTree', id: 'col-1' });
    expect(root(s).children.map((c) => c.kind)).toEqual(['row']);
    expect(ids(s)).toEqual([]);
    expect(s.selectedBlockId).toBeNull();
    s = run(s, { type: 'undo' });
    expect(ids(s)).toEqual(['a', 'b']);
    expect(run(s, { type: 'removeNodeFromTree', id: 'r' })).toBe(s);
  });

  it('reorders blocks across containers through the flat list', () => {
    let s = run(initBuilder(nested()), { type: 'moveNode', id: 'a', parentId: 'row-1', index: 0 });
    expect(ids(s)).toEqual(['a', 'b']);
    s = run(s, { type: 'moveBlock', id: 'b', delta: -1 });
    expect(ids(s)).toEqual(['b', 'a']);
    expect(childIds(s, 'row-1')).toEqual(['b', 'a']);
  });
});

describe('page actions', () => {
  const launchpad = () => initBuilder(instantiateTemplate(getTemplate('token-launchpad')!));

  it('duplicates a page with a fresh path, fresh ids and the copy selected', () => {
    const s = run(launchpad(), { type: 'duplicatePage', path: '/manage' });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/manage', '/manage-copy']);
    expect(s.pagePath).toBe('/manage-copy');
    const copy = s.spec.pages[2]!;
    expect(copy.title).toBe('Manage copy');
    expect(copy.blocks!.map((b) => b.widget)).toEqual(s.spec.pages[1]!.blocks!.map((b) => b.widget));
    const ids = s.spec.pages.flatMap((p) => p.blocks!.map((b) => b.id));
    expect(new Set(ids).size).toBe(ids.length);
    // The copy is independent of the original.
    const edited = run(s, { type: 'setProp', id: copy.blocks![0]!.id, name: 'defaultAmount', value: '9' });
    expect(edited.spec.pages[1]!.blocks![0]!.props.defaultAmount).not.toBe('9');
    expect(run(s, { type: 'undo' }).spec.pages).toHaveLength(2);
    expect(run(s, { type: 'duplicatePage', path: '/nope' })).toBe(s);
  });

  it('numbers repeated copies', () => {
    const s = run(launchpad(), { type: 'duplicatePage', path: '/manage' }, { type: 'duplicatePage', path: '/manage' });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/manage', '/manage-copy-2', '/manage-copy']);
  });

  it('sets a page as home: it moves to / and the old home keeps its content under a title path', () => {
    const before = launchpad();
    const oldHomeIds = before.spec.pages[0]!.blocks!.map((b) => b.id);
    const s = run(before, { type: 'setHomePage', path: '/manage' });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/launch']);
    expect(s.spec.pages[0]!.title).toBe('Manage');
    expect(s.spec.pages[1]!.title).toBe('Launch');
    expect(s.spec.pages[1]!.blocks!.map((b) => b.id)).toEqual(oldHomeIds);
    expect(s.pagePath).toBe('/');
    expect(run(s, { type: 'setHomePage', path: '/' })).toBe(s);
    expect(run(before, { type: 'setHomePage', path: '/nope' })).toBe(before);
    expect(run(s, { type: 'undo' }).spec.pages.map((p) => p.path)).toEqual(['/', '/manage']);
  });

  it('rewrites button links when the home page changes', () => {
    let s = launchpad();
    s = run(
      s,
      { type: 'selectPage', path: '/' },
      { type: 'addBlock', widget: 'action-button', props: { label: 'Manage', href: '/manage' } },
      { type: 'addBlock', widget: 'action-button', props: { label: 'Docs', href: 'https://docs.splendor.org' } },
    );
    const buttons = s.spec.pages[0]!.blocks!.filter((b) => b.widget === 'action-button');
    s = run(s, { type: 'setHomePage', path: '/manage' });
    const moved = s.spec.pages.find((p) => p.path === '/launch')!;
    const hrefs = Object.fromEntries(moved.blocks!.filter((b) => b.widget === 'action-button').map((b) => [b.id, b.props.href]));
    expect(hrefs[buttons[0]!.id]).toBe('/');
    expect(hrefs[buttons[1]!.id]).toBe('https://docs.splendor.org');
  });
});

describe('tree insert limits', () => {
  it('refuses to insert past the page block cap and into unknown parents', () => {
    let s = initBuilder(emptySpec());
    const rootId = (s.spec.pages[0]!.root as ContainerNode).id;
    const stackId = ((s.spec.pages[0]!.root as ContainerNode).children[0] as ContainerNode).id;
    for (let i = 0; i < MAX_BLOCKS_PER_PAGE + 5; i++) {
      s = builderReducer(s, { type: 'insertIntoTree', parentId: stackId, index: 0, widget: 'transfer', props: {} });
    }
    expect(s.spec.pages[0]!.blocks).toHaveLength(MAX_BLOCKS_PER_PAGE);
    expect(builderReducer(s, { type: 'insertIntoTree', parentId: 'nope', index: 0, widget: 'transfer', props: {} })).toBe(s);
    expect(rootId).toBeTruthy();
  });

  it('inserts at an index inside a nested container and keeps the flat list in tree order', () => {
    const spec = emptySpec();
    const root: ContainerNode = {
      kind: 'section',
      id: 'r',
      children: [
        { kind: 'column', id: 'c1', children: [{ kind: 'block', block: { id: 'a', widget: 'transfer', props: {} } }] },
        { kind: 'column', id: 'c2', children: [{ kind: 'block', block: { id: 'b', widget: 'transfer', props: {} } }] },
      ],
    };
    spec.pages[0]!.root = root;
    spec.pages[0]!.blocks = blocksFromTree(root);
    let s = run(initBuilder(spec), { type: 'insertIntoTree', parentId: 'c2', index: 0, widget: 'hero', props: {} });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['a', 'hero-1', 'b']);
    s = run(s, { type: 'insertIntoTree', parentId: 'c1', index: 1, widget: 'hero', props: {} });
    expect(s.spec.pages[0]!.blocks!.map((b) => b.id)).toEqual(['a', 'hero-2', 'hero-1', 'b']);
  });

  it('refuses a move that would nest deeper than the validator allows', () => {
    const spec = emptySpec();
    let node: ContainerNode = { kind: 'stack', id: 'deep-6', children: [{ kind: 'block', block: { id: 'leaf', widget: 'transfer', props: {} } }] };
    for (let i = 5; i >= 3; i--) node = { kind: 'stack', id: `deep-${i}`, children: [node] };
    const root: ContainerNode = { kind: 'section', id: 'r', children: [node, { kind: 'column', id: 'side', children: [{ kind: 'block', block: { id: 'x', widget: 'transfer', props: {} } }] }] };
    spec.pages[0]!.root = root;
    spec.pages[0]!.blocks = blocksFromTree(root);
    const s = initBuilder(spec);
    // r > deep-3 > deep-4 > deep-5 > deep-6 > leaf is 6 levels. Moving `side` (2 levels with its block)
    // under deep-6 would make 7.
    expect(run(s, { type: 'moveNode', id: 'side', parentId: 'deep-6', index: 0 })).toBe(s);
    // One level up it still fits.
    const ok = run(s, { type: 'moveNode', id: 'side', parentId: 'deep-5', index: 0 });
    expect(ok).not.toBe(s);
    expect(ok.spec.pages[0]!.blocks!.map((b) => b.id).sort()).toEqual(['leaf', 'x']);
  });
});
describe('page settings', () => {
  const three = () => run(initBuilder(emptySpec()), { type: 'addPage', title: 'About' }, { type: 'addPage', title: 'Pricing' });

  it('sets who can see a page; public clears the field; the home page stays public', () => {
    let s = three();
    s = run(s, { type: 'setPageAccess', path: '/about', access: 'wallet' });
    expect(s.spec.pages.find((p) => p.path === '/about')!.access).toBe('wallet');
    s = run(s, { type: 'setPageAccess', path: '/about', access: 'signin' });
    expect(s.spec.pages.find((p) => p.path === '/about')!.access).toBe('signin');
    s = run(s, { type: 'setPageAccess', path: '/about', access: 'public' });
    expect('access' in s.spec.pages.find((p) => p.path === '/about')!).toBe(false);
    const before = s;
    expect(run(s, { type: 'setPageAccess', path: '/', access: 'wallet' })).toBe(before);
    expect(run(s, { type: 'setPageAccess', path: '/nope', access: 'wallet' })).toBe(before);
    expect(run(s, { type: 'setPageAccess', path: '/about', access: 'public' })).toBe(before);
    expect(s.spec.pages.find((p) => p.path === '/')!.access).toBeUndefined();
  });

  it('hides a page from the navigation with an explicit ordered list, and shows it again in place', () => {
    let s = three();
    expect(s.spec.nav!.links).toBeUndefined();
    s = run(s, { type: 'setNavVisible', path: '/about', visible: false });
    expect(s.spec.nav!.links).toEqual(['/', '/pricing']);
    s = run(s, { type: 'setNavVisible', path: '/pricing', visible: false });
    expect(s.spec.nav!.links).toEqual(['/']);
    s = run(s, { type: 'setNavVisible', path: '/pricing', visible: true });
    expect(s.spec.nav!.links).toEqual(['/', '/pricing']);
    s = run(s, { type: 'setNavVisible', path: '/about', visible: true });
    // Everything shown, in page order: back to the default (no explicit list).
    expect(s.spec.nav!.links).toBeUndefined();
    expect(s.spec.nav!.showConnect).toBe(true);
    expect(run(s, { type: 'setNavVisible', path: '/about', visible: true })).toBe(s);
    expect(run(s, { type: 'setNavVisible', path: '/nope', visible: false })).toBe(s);
  });

  it('keeps the explicit nav list in step with page moves and new pages', () => {
    let s = three();
    s = run(s, { type: 'setNavVisible', path: '/about', visible: false });
    s = run(s, { type: 'addPage', title: 'Docs' });
    expect(s.spec.nav!.links).toEqual(['/', '/pricing', '/docs']);
    s = run(s, { type: 'movePage', path: '/docs', delta: -1 });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/', '/about', '/docs', '/pricing']);
    expect(s.spec.nav!.links).toEqual(['/', '/docs', '/pricing']);
    s = run(s, { type: 'duplicatePage', path: '/pricing' });
    expect(s.spec.nav!.links).toEqual(['/', '/docs', '/pricing', '/pricing-copy']);
    // Moving past a hidden page leaves the shown order alone.
    s = run(s, { type: 'movePage', path: '/docs', delta: -1 });
    expect(s.spec.nav!.links).toEqual(['/', '/docs', '/pricing', '/pricing-copy']);
  });

  it('keeps a validated spec valid', () => {
    let s = three();
    s = run(s, { type: 'setPageAccess', path: '/about', access: 'wallet' }, { type: 'setNavVisible', path: '/pricing', visible: false });
    expect(validateSpec(s.spec).ok).toBe(true);
  });
});
