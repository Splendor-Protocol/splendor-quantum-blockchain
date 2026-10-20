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
});
