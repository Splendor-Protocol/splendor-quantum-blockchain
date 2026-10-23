import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, getTemplate, instantiateTemplate, validateSpec } from '@splendor/dapp-spec';
import { describeIssue } from './issues';

const catalog = createCatalog(WIDGET_MANIFESTS);

describe('describeIssue', () => {
  it('names the page, block and field of a prop issue and points at the block', () => {
    const spec = instantiateTemplate(getTemplate('tip-jar')!);
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(describeIssue(spec, catalog, r.issues[0]!)).toEqual({
      where: 'Tip → Tip jar → Recipient',
      message: 'needs a value',
      target: { pagePath: '/', blockId: 'tips', prop: 'recipient', entryId: 'tip-jar' },
    });
  });

  it('handles page, theme and top-level paths without a target', () => {
    const spec = instantiateTemplate(getTemplate('tip-jar')!);
    expect(describeIssue(spec, catalog, { path: 'pages[0].title', message: 'title is required (max 60 chars)' })).toMatchObject({
      where: 'Tip → title',
      target: null,
    });
    expect(describeIssue(spec, catalog, { path: 'theme.overrides.accent', message: 'x' }).where).toBe('Theme → accent');
    expect(describeIssue(spec, catalog, { path: 'name', message: 'x' }).where).toBe('Name');
  });

  it('names style, layout-tree, contract and nav paths, and targets the node', () => {
    const spec = instantiateTemplate(getTemplate('tip-jar')!);
    spec.contracts = [{ id: 'c', name: 'Counter', address: '', functions: [{ name: 'inc', signature: 'inc', params: [], kind: 'write' }] }];
    expect(describeIssue(spec, catalog, { path: 'pages[0].blocks[1].style.width', message: 'x' })).toMatchObject({
      where: 'Tip → Tip jar → Style → width',
      target: { blockId: 'tips' },
    });
    const root = spec.pages[0]!.root as { id: string };
    expect(describeIssue(spec, catalog, { path: 'pages[0].root.style.gap', message: 'x' })).toMatchObject({
      where: 'Tip → Section → Style → gap',
      target: { blockId: root.id },
    });
    expect(describeIssue(spec, catalog, { path: 'pages[0].root.children[0].children[1].block.id', message: 'x' })).toMatchObject({
      where: 'Tip → Tip jar → id',
      target: { blockId: 'tips' },
    });
    expect(describeIssue(spec, catalog, { path: 'contracts[0].functions[0].signature', message: 'x' }).where).toBe('Contracts → Counter → inc → signature');
    expect(describeIssue(spec, catalog, { path: 'nav.links', message: 'x' }).where).toBe('Navigation → links');
  });
});
