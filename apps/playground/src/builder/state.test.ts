import { describe, expect, it, vi } from 'vitest';
import { emptySpec, getTemplate, instantiateTemplate } from '@splendor/dapp-spec';
import { builderReducer, initBuilder, type BuilderAction, type BuilderState } from './state';

const run = (s: BuilderState, ...actions: BuilderAction[]) => actions.reduce(builderReducer, s);

describe('builderReducer', () => {
  it('adds blocks with unique ids and selects them', () => {
    const s = run(initBuilder(emptySpec()), { type: 'addBlock', widget: 'transfer', props: {} }, { type: 'addBlock', widget: 'transfer', props: {} });
    const ids = s.spec.pages[0]!.blocks.map((b) => b.id);
    expect(ids).toEqual(['hero', 'transfer-1', 'transfer-2']);
    expect(s.selectedBlockId).toBe('transfer-2');
  });

  it('undoes and redoes structural edits', () => {
    const a = run(initBuilder(emptySpec()), { type: 'addBlock', widget: 'transfer', props: {} });
    const b = run(a, { type: 'undo' });
    expect(b.spec.pages[0]!.blocks).toHaveLength(1);
    expect(b.selectedBlockId).toBeNull();
    const c = run(b, { type: 'redo' });
    expect(c.spec.pages[0]!.blocks).toHaveLength(2);
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
    expect(s.spec.pages[0]!.blocks[0]!.props.heading).toBe('Hello');
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

  it('moves and removes blocks on the current page', () => {
    const spec = instantiateTemplate(getTemplate('wallet-dashboard')!);
    let s = run(initBuilder(spec), { type: 'moveBlock', id: 'send', delta: -1 });
    expect(s.spec.pages[0]!.blocks.map((b) => b.id)).toEqual(['hero', 'send', 'account', 'pending']);
    s = run(s, { type: 'selectBlock', id: 'send' }, { type: 'removeBlock', id: 'send' });
    expect(s.selectedBlockId).toBeNull();
    expect(s.spec.pages[0]!.blocks.map((b) => b.id)).toEqual(['hero', 'account', 'pending']);
  });

  it('duplicates a block right after the original with its own id and props', () => {
    const spec = instantiateTemplate(getTemplate('wallet-dashboard')!);
    let s = run(initBuilder(spec), { type: 'duplicateBlock', id: 'send' });
    expect(s.spec.pages[0]!.blocks.map((b) => b.id)).toEqual(['hero', 'account', 'send', 'transfer-1', 'pending']);
    expect(s.selectedBlockId).toBe('transfer-1');
    s = run(s, { type: 'setProp', id: 'transfer-1', name: 'defaultAmount', value: '5' });
    expect(s.spec.pages[0]!.blocks.find((b) => b.id === 'send')!.props.defaultAmount).toBe('0.01');
    s = run(s, { type: 'undo' }, { type: 'undo' });
    expect(s.spec.pages[0]!.blocks).toHaveLength(4);
  });

  it('reorders pages', () => {
    const spec = instantiateTemplate(getTemplate('token-launchpad')!);
    const s = run(initBuilder(spec), { type: 'movePage', path: '/manage', delta: -1 });
    expect(s.spec.pages.map((p) => p.path)).toEqual(['/manage', '/']);
    expect(run(s, { type: 'movePage', path: '/manage', delta: -1 })).toBe(s);
  });

  it('clears a prop when set to undefined', () => {
    const s = run(initBuilder(emptySpec()), { type: 'setProp', id: 'hero', name: 'text', value: undefined });
    expect('text' in s.spec.pages[0]!.blocks[0]!.props).toBe(false);
  });
});
