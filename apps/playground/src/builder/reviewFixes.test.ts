import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createCatalog, emptySpec, instantiateTemplate, TEMPLATES, type DappSpec } from '@splendor/dapp-spec';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { builderReducer, initBuilder, pagePathError } from './state';
import { describeIssue } from './issues';
import {
  DRAFT_LOCK_TTL_MS,
  TAB_ID,
  __resetDraftTabState,
  claimDraft,
  draftHeldElsewhere,
  loadDraft,
  releaseDraft,
  saveDraft,
  splitOffDraft,
} from './storage';
import { collectLinks, parseNavbarLinks } from '../studio/FlowsPanel';
import { isEditorKey } from '../preview/protocol';

const LOCK_KEY = 'splendor-playground:draft-lock:v1';

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  __resetDraftTabState();
});
afterEach(() => __resetDraftTabState());

describe('draft shared between tabs', () => {
  it('sees another live tab, ignores a stale lock and its own', () => {
    expect(draftHeldElsewhere()).toBe(false);
    localStorage.setItem(LOCK_KEY, JSON.stringify({ tab: 'other', at: Date.now() }));
    expect(draftHeldElsewhere()).toBe(true);
    expect(draftHeldElsewhere(Date.now() + DRAFT_LOCK_TTL_MS + 1)).toBe(false);
    claimDraft();
    expect(JSON.parse(localStorage.getItem(LOCK_KEY)!).tab).toBe(TAB_ID);
    expect(draftHeldElsewhere()).toBe(false);
    releaseDraft();
    expect(localStorage.getItem(LOCK_KEY)).toBeNull();
  });

  it('a separate copy lives in this tab only and leaves the shared draft alone', () => {
    saveDraft({ spec: emptySpec('Shared'), projectId: null });
    localStorage.setItem(LOCK_KEY, JSON.stringify({ tab: 'other', at: Date.now() }));
    expect(splitOffDraft({ spec: emptySpec('Mine'), projectId: null })).toBe(true);
    expect(draftHeldElsewhere()).toBe(false);
    saveDraft({ spec: emptySpec('Mine, edited'), projectId: null });
    expect(loadDraft()?.spec.name).toBe('Mine, edited');
    expect(JSON.parse(localStorage.getItem('splendor-playground:draft:v1')!).spec.name).toBe('Shared');
    claimDraft();
    expect(JSON.parse(localStorage.getItem(LOCK_KEY)!).tab).toBe('other');
  });
});

describe('editor keys forwarded from the canvas', () => {
  it('allows only builder shortcuts', () => {
    const k = (key: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean }> = {}) => ({
      key,
      ctrlKey: false,
      metaKey: false,
      shiftKey: false,
      altKey: false,
      ...mods,
    });
    for (const key of ['z', 'y', 'c', 'v', 'd', 'k', 'Z']) expect(isEditorKey(k(key, { ctrlKey: true })), key).toBe(true);
    expect(isEditorKey(k('z', { metaKey: true, shiftKey: true }))).toBe(true);
    for (const key of ['Delete', 'Backspace', 'Escape', '?']) expect(isEditorKey(k(key)), key).toBe(true);
    for (const key of ['a', 'Enter', 'Tab', 'z']) expect(isEditorKey(k(key)), key).toBe(false);
    expect(isEditorKey(k('r', { ctrlKey: true }))).toBe(false);
    expect(isEditorKey(k('Delete', { altKey: true }))).toBe(false);
    expect(isEditorKey({ key: 7 as never })).toBe(false);
  });
});

describe('pages, nav and layer names', () => {
  const launchpad = () => initBuilder(instantiateTemplate(TEMPLATES.find((t) => t.id === 'token-launchpad')!));

  it('changes a page path, remaps links to it and rejects bad or taken paths', () => {
    let state = launchpad();
    const home = state.spec.pages[0]!;
    const block = { id: 'go', widget: 'action-button', props: { label: 'Manage', href: '/manage' } };
    home.blocks = [...(home.blocks ?? []), block];
    (home.root as { children: { children: unknown[] }[] }).children[0]!.children.push({ kind: 'block', block });
    expect(pagePathError(state.spec, '/manage', '/Manage')).toMatch(/lowercase/);
    const withAbout = builderReducer(state, { type: 'addPage', title: 'About' }).spec;
    expect(pagePathError(withAbout, '/manage', '/about')).toMatch(/already uses/);
    expect(pagePathError(state.spec, '/', '/x')).toMatch(/always/);
    state = builderReducer(state, { type: 'setPagePath', path: '/manage', nextPath: '/tokens' });
    expect(state.spec.pages.map((p) => p.path)).toEqual(['/', '/tokens']);
    expect(state.spec.pages[0]!.blocks!.find((b) => b.id === 'go')!.props.href).toBe('/tokens');
    expect(builderReducer(state, { type: 'setPagePath', path: '/tokens', nextPath: 'bad path' })).toBe(state);
  });

  it('reorders pages by drag and keeps an explicit nav order in step', () => {
    let state = launchpad();
    state = builderReducer(state, { type: 'addPage', title: 'About' });
    state = builderReducer(state, { type: 'setNavVisible', path: '/manage', visible: false });
    state = builderReducer(state, { type: 'reorderPage', path: '/about', index: 0 });
    expect(state.spec.pages.map((p) => p.path)).toEqual(['/about', '/', '/manage']);
    expect(state.spec.nav?.links).toEqual(['/about', '/']);
  });

  it('edits brand, footer and the Connect button', () => {
    let state = launchpad();
    state = builderReducer(state, { type: 'setNav', nav: { brand: 'Acme', footer: '© Acme', showConnect: false } });
    expect(state.spec.nav).toMatchObject({ brand: 'Acme', footer: '© Acme', showConnect: false });
    state = builderReducer(state, { type: 'setNav', nav: { brand: undefined, showConnect: undefined } });
    expect(state.spec.nav).not.toHaveProperty('brand');
    expect(state.spec.nav).not.toHaveProperty('showConnect');
  });

  it('names a container for the layers panel and clears it with an empty name', () => {
    let state = launchpad();
    const root = state.spec.pages[0]!.root as { id: string; children: { id: string }[] };
    const id = root.children[0]!.id;
    state = builderReducer(state, { type: 'renameNode', id, label: 'Hero area' });
    expect((state.spec.pages[0]!.root as { children: { label?: string }[] }).children[0]!.label).toBe('Hero area');
    state = builderReducer(state, { type: 'renameNode', id, label: '  ' });
    expect((state.spec.pages[0]!.root as { children: { label?: string }[] }).children[0]).not.toHaveProperty('label');
  });

  it('issue targets point at style fields and fixing panels', () => {
    const spec = instantiateTemplate(TEMPLATES.find((t) => t.id === 'tip-jar')!);
    const catalog = createCatalog(WIDGET_MANIFESTS);
    expect(describeIssue(spec, catalog, { path: 'pages[0].blocks[1].style.width', message: 'x' }).target).toMatchObject({ blockId: 'tips', style: 'width' });
    expect(describeIssue(spec, catalog, { path: 'theme.overrides.accent', message: 'x' }).panel).toBe('theme');
    expect(describeIssue(spec, catalog, { path: 'nav.links', message: 'x' }).panel).toBe('pages');
    expect(describeIssue(spec, catalog, { path: 'contracts[0].address', message: 'x' }).panel).toBe('contracts');
  });
});

describe('navbar links written by the list editor (one per line)', () => {
  function withNavbar(links: string): DappSpec {
    const spec = instantiateTemplate(TEMPLATES.find((t) => t.id === 'token-launchpad')!);
    const home = spec.pages[0]!;
    const block = { id: 'nav', widget: 'navbar', props: { brand: 'X', links } };
    home.blocks = [...(home.blocks ?? []), block];
    (home.root as { children: { children: unknown[] }[] }).children[0]!.children.push({ kind: 'block', block });
    return spec;
  }

  it('parses newline and comma separated items', () => {
    expect(parseNavbarLinks('Home|/\nManage|/manage')).toEqual([
      { label: 'Home', path: '/' },
      { label: 'Manage', path: '/manage' },
    ]);
    expect(parseNavbarLinks('Home|/,Manage|/manage')).toHaveLength(2);
    expect(collectLinks(withNavbar('Home|/\nManage|/manage')).filter((l) => l.fromBlockId === 'nav').map((l) => l.to)).toEqual(['/', '/manage']);
  });

  it('removing a page drops its newline-separated navbar link', () => {
    let state = initBuilder(withNavbar('Home|/\nManage|/manage\nDocs|https://example.com'));
    state = builderReducer(state, { type: 'removePage', path: '/manage' });
    const nav = state.spec.pages[0]!.blocks!.find((b) => b.id === 'nav')!;
    expect(nav.props.links).toBe('Home|/\nDocs|https://example.com');
  });
});
