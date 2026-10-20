import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { TEMPLATES, createCatalog, instantiateTemplate } from '@splendor/dapp-spec';
import { validateForPreview } from './validatePreview';

const catalog = createCatalog(WIDGET_MANIFESTS);
const tipJar = () => instantiateTemplate(TEMPLATES.find((t) => t.id === 'tip-jar')!);

describe('validateForPreview', () => {
  it('renders a spec whose only issue is an empty required field, and still reports it', () => {
    const r = validateForPreview(tipJar(), catalog);
    expect(r.spec).not.toBeNull();
    expect(r.issues).toEqual([{ path: 'pages[0].blocks[1].props.recipient', message: 'required' }]);
  });

  it('blocks rendering on any other issue', () => {
    const spec = tipJar();
    spec.pages[0]!.blocks[1]!.props.recipient = '0xnope';
    spec.theme.overrides.accent = 'url(https://evil)';
    const r = validateForPreview(spec, catalog);
    expect(r.spec).toBeNull();
    expect(r.issues.map((i) => i.path)).toContain('theme.overrides.accent');
  });

  it('passes a valid spec through with no issues', () => {
    const spec = tipJar();
    spec.pages[0]!.blocks[1]!.props.recipient = 'SQ:' + 'ab'.repeat(32);
    expect(validateForPreview(spec, catalog)).toMatchObject({ issues: [] });
  });
});
