import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { TEMPLATES, createCatalog, instantiateTemplate, updateBlockProps, type DappSpec } from '@splendor/dapp-spec';
import { validateForPreview } from './validatePreview';

const catalog = createCatalog(WIDGET_MANIFESTS);
const tipJar = () => instantiateTemplate(TEMPLATES.find((t) => t.id === 'tip-jar')!);
/** Set a prop on the layout tree (the validator reads the tree; the flat list is derived from it). */
const setRecipient = (spec: DappSpec, value: string) => {
  const page = spec.pages[0]!;
  page.root = updateBlockProps(page.root, 'tips', { ...page.blocks!.find((b) => b.id === 'tips')!.props, recipient: value }) as typeof page.root;
};

describe('validateForPreview', () => {
  it('renders a spec whose only issue is an empty required field, and still reports it', () => {
    const r = validateForPreview(tipJar(), catalog);
    expect(r.spec).not.toBeNull();
    expect(r.issues).toEqual([{ path: 'pages[0].blocks[1].props.recipient', message: 'required' }]);
  });

  it('blocks rendering on any other issue', () => {
    const spec = tipJar();
    setRecipient(spec, '0xnope');
    spec.theme.overrides.accent = 'url(https://evil)';
    const r = validateForPreview(spec, catalog);
    expect(r.spec).toBeNull();
    expect(r.issues.map((i) => i.path)).toContain('theme.overrides.accent');
  });

  it('passes a valid spec through with no issues', () => {
    const spec = tipJar();
    setRecipient(spec, 'SQ:' + 'ab'.repeat(32));
    expect(validateForPreview(spec, catalog)).toMatchObject({ issues: [] });
  });
});
