import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { emptySpec } from '@splendor/dapp-spec';
import { clearDraft, discardStashedDraft, loadDraft, peekStashedDraft, saveDraft, stashDraft, takeStashedDraft } from './storage';

const KEY = 'splendor-playground:draft:v1';
const put = (value: unknown) => localStorage.setItem(KEY, typeof value === 'string' ? value : JSON.stringify(value));

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe('draft storage', () => {
  it('round-trips a draft and stamps savedAt', () => {
    const spec = emptySpec('Mine');
    saveDraft({ spec, projectId: 'p1' });
    const draft = loadDraft();
    expect(draft).toMatchObject({ spec: { name: 'Mine' }, projectId: 'p1' });
    expect(Number.isNaN(Date.parse(draft!.savedAt))).toBe(false);
    clearDraft();
    expect(loadDraft()).toBeNull();
  });

  it('migrates a v0 draft to the current spec version', () => {
    put({ spec: { version: 0, name: 'Old', network: 'devnet', theme: 'dark', blocks: [{ id: 'a', widget: 'hero', props: {} }] } });
    const draft = loadDraft();
    expect(draft?.spec.version).toBe(2);
    expect(draft?.spec.theme).toEqual({ base: 'dark', overrides: {} });
    expect(draft?.spec.pages[0]?.root.kind).toBe('section');
    expect(draft?.projectId).toBeNull();
    expect(draft?.savedAt).toBe(new Date(0).toISOString());
  });

  it('keeps a draft whose values are invalid, so the user can fix them', () => {
    const spec = emptySpec('Half typed');
    spec.contracts = [{ id: 'token', name: 'Token', address: 'SQ:12', functions: [] }];
    put({ spec, projectId: null, savedAt: '2026-01-01T00:00:00.000Z' });
    expect(loadDraft()?.spec.contracts?.[0]?.address).toBe('SQ:12');
  });

  it('drops drafts the editor cannot open', () => {
    put({ spec: { ...emptySpec(), pages: [] } });
    expect(loadDraft()).toBeNull();

    const spec = emptySpec();
    put({ spec: { ...spec, pages: [{ ...spec.pages[0], root: { kind: 'block', block: { id: 'x', widget: 'hero', props: {} } } }] } });
    expect(loadDraft()).toBeNull();

    put({ spec: { ...spec, version: 99 } });
    expect(loadDraft()).toBeNull();

    put('{not json');
    expect(loadDraft()).toBeNull();

    put({ spec: null });
    expect(loadDraft()).toBeNull();
  });

  it('keeps the cloud version and unsaved flag, and drops junk values', () => {
    saveDraft({ spec: emptySpec('Mine'), projectId: 'p1', version: 7, dirty: true });
    expect(loadDraft()).toMatchObject({ projectId: 'p1', version: 7, dirty: true });
    saveDraft({ spec: emptySpec('Mine'), projectId: 'p1', dirty: false });
    const clean = loadDraft()!;
    expect(clean).not.toHaveProperty('version');
    expect(clean).not.toHaveProperty('dirty');
    put({ spec: emptySpec(), projectId: 'p1', version: -1, dirty: 'yes' });
    expect(loadDraft()).not.toHaveProperty('version');
    expect(loadDraft()).not.toHaveProperty('dirty');
  });

  it('stashes a draft that exists nowhere else before it is replaced, and restores it once', () => {
    expect(stashDraft()).toBe(false);
    saveDraft({ spec: emptySpec('Unsaved work'), projectId: null });
    expect(stashDraft()).toBe(true);
    saveDraft({ spec: emptySpec('Template'), projectId: null });
    expect(peekStashedDraft()?.spec.name).toBe('Unsaved work');
    expect(takeStashedDraft()?.spec.name).toBe('Unsaved work');
    expect(takeStashedDraft()).toBeNull();
  });

  it('does not stash an account project the account already has, and can discard a stash', () => {
    saveDraft({ spec: emptySpec('Cloud'), projectId: 'p1', version: 2 });
    expect(stashDraft()).toBe(false);
    expect(peekStashedDraft()).toBeNull();
    saveDraft({ spec: emptySpec('Local'), projectId: null });
    stashDraft();
    discardStashedDraft();
    expect(peekStashedDraft()).toBeNull();
  });

  it('stashes account-project edits that were not saved yet, with the version they started from', () => {
    saveDraft({ spec: emptySpec('Cloud edits'), projectId: 'p1', version: 3, dirty: true });
    expect(stashDraft()).toBe(true);
    saveDraft({ spec: emptySpec('Remix'), projectId: null });
    expect(takeStashedDraft()).toMatchObject({ spec: { name: 'Cloud edits' }, projectId: 'p1', version: 3, dirty: true });
  });

  it('never stashes an untouched draft, so it cannot push real work out of the stash', () => {
    saveDraft({ spec: emptySpec('Real work'), projectId: null });
    expect(stashDraft()).toBe(true);
    // A template opened and left alone, then replaced again.
    saveDraft({ spec: emptySpec('Untouched template'), projectId: null, edited: false });
    expect(loadDraft()?.edited).toBe(false);
    expect(stashDraft()).toBe(false);
    expect(peekStashedDraft()?.spec.name).toBe('Real work');
    // Older drafts without the flag count as edited.
    put({ spec: emptySpec('Legacy'), projectId: null });
    expect(stashDraft()).toBe(true);
  });

  it('reports a refused write and keeps the previous draft, and ignores storage that refuses reads', () => {
    expect(saveDraft({ spec: emptySpec('Last good'), projectId: null })).toBe(true);
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError');
    });
    expect(saveDraft({ spec: emptySpec('Too big'), projectId: null })).toBe(false);
    expect(loadDraft()?.spec.name).toBe('Last good');
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('disabled');
    });
    expect(() => clearDraft()).not.toThrow();
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('disabled');
    });
    expect(loadDraft()).toBeNull();
  });
});
