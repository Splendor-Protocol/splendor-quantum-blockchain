import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  DAPP_SPEC_VERSION,
  createCatalog,
  defaultProps,
  emptySpec,
  generateProject,
  validateSpec,
  wrapBlocks,
  type CatalogEntry,
  type DappBlock,
  type DappSpec,
} from '../index.js';

const catalog = createCatalog(WIDGET_MANIFESTS);
const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };
const FRAMEWORKS = ['vite', 'next'] as const;
const ADDRESS = 'SQ:' + 'ab'.repeat(32);

function oneBlockSpec(block: DappBlock, name = 'Prod'): DappSpec {
  return {
    version: DAPP_SPEC_VERSION,
    name,
    network: 'testnet',
    theme: { base: 'splendor', overrides: {} },
    pages: [{ path: '/', title: name, root: wrapBlocks([block]), blocks: [block] }],
  };
}

/** Defaults, with required address / amount fields filled so the spec validates. */
function filledProps(entry: CatalogEntry): DappBlock['props'] {
  const props = { ...defaultProps(entry) };
  for (const p of entry.props) {
    if (!p.required || props[p.name] !== undefined && props[p.name] !== '') continue;
    if (p.format === 'address') props[p.name] = ADDRESS;
    else if (p.format === 'amount') props[p.name] = '1';
    else if (p.format === 'integer') props[p.name] = '1';
    else if (p.type === 'string') props[p.name] = 'x';
  }
  return props;
}

const pageFile = (files: Record<string, string>, framework: 'vite' | 'next') =>
  files[framework === 'vite' ? 'src/pages/HomePage.tsx' : 'app/HomePage.tsx']!;

const placeable = catalog.filter((e) => e.placeable && !e.props.some((p) => p.format === 'contract' || p.format === 'function'));

describe('generateProject prod — every catalog entry', () => {
  for (const framework of FRAMEWORKS) {
    it.each(placeable.map((e) => [e.id, e] as const))(`[L1] ${framework}: %s renders and imports its component`, (_id, entry) => {
      const spec = oneBlockSpec({ id: 'item', widget: entry.id, props: filledProps(entry) });
      const v = validateSpec(spec, { catalog });
      expect(v.ok, JSON.stringify(!v.ok && v.issues)).toBe(true);
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const page = pageFile(files, framework);
      expect(page).toContain(`<${entry.importName}`);
      expect(page).toMatch(new RegExp(`import \\{[^}]*\\b${entry.importName}\\b`));
    });
  }

  it('[L1] the blank spec validates and generates for both frameworks', () => {
    const spec = emptySpec('Blank');
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    for (const framework of FRAMEWORKS) {
      expect(Object.keys(generateProject(spec, { framework, catalog, versions }).files)).toContain('package.json');
    }
  });
});

describe('generateProject prod — hostile text', () => {
  const nasty = [
    'quote " and \' and `backtick` ${x}',
    '</script><script>alert(1)</script>',
    '{alert(1)} }} {{',
    'line\u2028sep\u2029para\nnewline',
    '\\" escaped \\\\',
    'é ü 漢字 🚀',
  ];

  it.each(nasty)('[L2] a hero heading round-trips through the generated JSX: %s', (heading) => {
    const spec = oneBlockSpec({ id: 'hero', widget: 'hero', props: { heading, text: '' } });
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    for (const framework of FRAMEWORKS) {
      const page = pageFile(generateProject(spec, { framework, catalog, versions }).files, framework);
      const m = page.match(/heading=\{("(?:[^"\\]|\\.)*")\}/);
      expect(m, page).toBeTruthy();
      expect(JSON.parse(m![1]!)).toBe(heading);
      expect(page).not.toContain('</script>');
    }
  });

  it('[L2] the app name is HTML-escaped in index.html and JSON-escaped in code', () => {
    const spec = emptySpec('<img src=x onerror=alert(1)> "Shop"');
    const files = generateProject(spec, { framework: 'vite', catalog, versions }).files;
    expect(files['index.html']).not.toContain('<img');
    expect(files['index.html']).toContain('&lt;img');
    expect(files['src/App.tsx']).toContain(JSON.stringify(spec.name));
  });
});

describe('generateProject prod — fails closed on specs that skipped validation', () => {
  const base = () => oneBlockSpec({ id: 'hero', widget: 'hero', props: { heading: 'x' } });

  it('[L3] a prop name that is not a JSX attribute name', () => {
    const spec = base();
    spec.pages[0]!.blocks![0]!.props = { 'heading={alert(1)} x': 'y' };
    spec.pages[0]!.root = wrapBlocks(spec.pages[0]!.blocks!);
    for (const framework of FRAMEWORKS) {
      expect(() => generateProject(spec, { framework, catalog, versions, validate: false })).toThrow(/prop name/);
    }
  });

  it('[L3] a page path that is not a plain slug', () => {
    const spec = base();
    const block: DappBlock = { id: 'evil', widget: 'hero', props: { heading: 'x' } };
    spec.pages.push({ path: '/x";alert(1);//', title: 'Evil', root: wrapBlocks([block], 'evil-root'), blocks: [block] });
    for (const framework of FRAMEWORKS) {
      expect(() => generateProject(spec, { framework, catalog, versions, validate: false })).toThrow(/page path/);
    }
  });

  it('[L3] a hideOn or container kind outside the allowed set', () => {
    const a = base();
    (a.pages[0]!.root as { style?: Record<string, unknown> }).style = { hideOn: 'x" onClick={alert}' };
    expect(() => generateProject(a, { framework: 'vite', catalog, versions, validate: false })).toThrow(/hideOn/);
    const b = base();
    (b.pages[0]!.root as { kind: string }).kind = 'section" onClick={alert';
    expect(() => generateProject(b, { framework: 'vite', catalog, versions, validate: false })).toThrow(/container kind/);
  });

  it('[L3] by default generateProject validates first and names the failing path', () => {
    const spec = base();
    spec.pages[0]!.blocks![0]!.props = { 'bad name': 'y' };
    spec.pages[0]!.root = wrapBlocks(spec.pages[0]!.blocks!);
    for (const framework of FRAMEWORKS) {
      expect(() => generateProject(spec, { framework, catalog, versions })).toThrow(/invalid spec — pages\[0\]/);
    }
  });

  it('[L3] the validator rejects the same specs, so the UI never reaches codegen with them', () => {
    const spec = base();
    spec.pages[0]!.blocks![0]!.props = { 'bad name': 'y' };
    spec.pages[0]!.root = wrapBlocks(spec.pages[0]!.blocks!);
    expect(validateSpec(spec, { catalog }).ok).toBe(false);
  });
});

describe('generateProject prod — volume', () => {
  it('[L4] 20 pages x 40 blocks validates and generates both frameworks in under 10 s', () => {
    const spec = emptySpec('Big');
    spec.pages = Array.from({ length: 20 }, (_, i) => {
      const blocks: DappBlock[] = Array.from({ length: 40 }, (_, j) => ({ id: `b${i}-${j}`, widget: 'hero', props: { heading: `P${i} B${j}` } }));
      return { path: i === 0 ? '/' : `/page-${i}`, title: `Page ${i}`, root: wrapBlocks(blocks, `root-${i}`), blocks };
    });
    const v = validateSpec(spec, { catalog });
    expect(v.ok, JSON.stringify(!v.ok && v.issues.slice(0, 3))).toBe(true);
    const t0 = performance.now();
    for (const framework of FRAMEWORKS) {
      const files = generateProject(spec, { framework, catalog, versions }).files;
      expect(Object.keys(files).filter((f) => /Page\d*\.tsx$|page\.tsx$/.test(f)).length).toBeGreaterThanOrEqual(20);
    }
    expect(performance.now() - t0).toBeLessThan(10_000);
  });
});
