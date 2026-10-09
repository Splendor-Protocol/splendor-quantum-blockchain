import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  BLOCK_MANIFESTS,
  TEMPLATES,
  blockSource,
  createCatalog,
  emptySpec,
  generateProject,
  instantiateTemplate,
  migrateSpec,
  validateSpec,
  type DappSpec,
} from '../index.js';

const catalog = createCatalog(WIDGET_MANIFESTS);
const RECIPIENT = 'SQ:' + 'ab'.repeat(32);

function ready(id: string): DappSpec {
  const t = TEMPLATES.find((x) => x.id === id)!;
  const spec = instantiateTemplate(t);
  for (const s of t.setup) {
    const block = spec.pages.flatMap((p) => p.blocks).find((b) => b.id === s.blockId)!;
    block.props[s.prop] = RECIPIENT;
  }
  return spec;
}

describe('catalog', () => {
  it('merges widgets and blocks with unique ids', () => {
    expect(catalog.length).toBe(WIDGET_MANIFESTS.length + BLOCK_MANIFESTS.length);
    expect(new Set(catalog.map((c) => c.id)).size).toBe(catalog.length);
  });

  it('applies admin flags', () => {
    const c = createCatalog(WIDGET_MANIFESTS, { 'nft-drop': { hidden: true }, transfer: { status: 'beta' } });
    expect(c.find((x) => x.id === 'nft-drop')).toBeUndefined();
    expect(c.find((x) => x.id === 'transfer')?.status).toBe('beta');
  });

  it('every block source is embedded', () => {
    for (const b of BLOCK_MANIFESTS) expect(blockSource(b.file)).toContain(`export function ${b.importName}`);
  });
});

describe('validateSpec', () => {
  it('accepts every template once its setup fields are filled', () => {
    for (const t of TEMPLATES) {
      const r = validateSpec(ready(t.id), { catalog });
      expect(r.ok, `${t.id}: ${JSON.stringify(!r.ok && r.issues)}`).toBe(true);
    }
  });

  it('reports setup fields that are still empty', () => {
    const r = validateSpec(instantiateTemplate(TEMPLATES.find((t) => t.id === 'tip-jar')!), { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.issues).toContainEqual({ path: 'pages[0].blocks[1].props.recipient', message: 'required' });
  });

  it('rejects unsafe or malformed input', () => {
    const spec = ready('wallet-dashboard') as unknown as Record<string, any>;
    spec.theme.overrides.accent = 'url(https://evil)';
    spec.pages[0].blocks[1].props.showBalance = 'yes';
    spec.pages[0].blocks.push({ id: 'x', widget: 'fee-row', props: {} });
    spec.pages[0].blocks.push({ id: 'y', widget: 'nope', props: {} });
    spec.pages.push({ path: '/Bad Path', title: 'x', blocks: [] });
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    if (r.ok) return;
    const paths = r.issues.map((i) => i.path);
    expect(paths).toContain('theme.overrides.accent');
    expect(paths).toContain('pages[0].blocks[1].props.showBalance');
    expect(paths).toContain('pages[0].blocks[4].widget');
    expect(paths).toContain('pages[0].blocks[5].widget');
    expect(paths).toContain('pages[1].path');
  });

  it('checks address, amount and number bounds', () => {
    const spec = ready('token-launchpad');
    const deploy = spec.pages[0]!.blocks.find((b) => b.id === 'deploy')!;
    deploy.props.decimals = 30;
    deploy.props.defaultSupply = '1e6';
    spec.pages[1]!.blocks[0]!.props.tokenAddress = '0xabc';
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.issues.map((i) => i.message)).toEqual(expect.arrayContaining(['max 18', 'must be a decimal amount', 'must be an SQ: address']));
  });

  it('requires a home page and never throws on junk', () => {
    expect(validateSpec(null).ok).toBe(false);
    expect(validateSpec('x').ok).toBe(false);
    const spec = ready('wallet-dashboard');
    spec.pages[0]!.path = '/home';
    const r = validateSpec(spec);
    expect(r.ok).toBe(false);
  });
});

describe('migrateSpec', () => {
  it('upgrades a v0 spec', () => {
    const v1 = migrateSpec({ version: 0, name: 'Old', network: 'devnet', theme: 'dark', blocks: [{ id: 'a', widget: 'hero', props: {} }] });
    expect(v1.version).toBe(1);
    expect(v1.theme).toEqual({ base: 'dark', overrides: {} });
    expect(v1.pages[0]!.path).toBe('/');
    expect(validateSpec(v1, { catalog }).ok).toBe(true);
  });

  it('passes v1 through and rejects newer versions', () => {
    const spec = ready('nft-drop');
    expect(migrateSpec(spec)).toEqual(spec);
    expect(() => migrateSpec({ ...spec, version: 99 })).toThrow();
  });
});

describe('generateProject', () => {
  const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };

  for (const framework of ['vite', 'next'] as const) {
    for (const t of TEMPLATES) {
      it(`${framework}: ${t.id}`, () => {
        const project = generateProject(ready(t.id), { framework, catalog, versions });
        expect(Object.keys(project.files)).toMatchSnapshot('files');
        const pkg = JSON.parse(project.files['package.json']!);
        expect(pkg.dependencies['@splendor/connect-react']).toBe('^3.0.0');
        expect(pkg.dependencies['@splendor/sqc-sdk']).toBe('^3.0.0');
        const pages = Object.entries(project.files).filter(([p]) => /(pages\/|app\/).*Page\.tsx$/.test(p));
        expect(Object.fromEntries(pages)).toMatchSnapshot('pages');
      });
    }
  }

  it('pins production WASM digests and only allows unpinned in dev', () => {
    const vite = generateProject(ready('wallet-dashboard'), { framework: 'vite', catalog, versions }).files;
    expect(vite['src/App.tsx']).toContain("required for production builds");
    expect(vite['vite.config.ts']).toContain('sqcWasmPlugin()');
    const next = generateProject(ready('wallet-dashboard'), { framework: 'next', catalog, versions }).files;
    expect(next['scripts/copy-sqc-wasm.mjs']).toContain('NEXT_PUBLIC_SQC_WASM_SHA256');
    expect(JSON.parse(next['package.json']!).scripts.prebuild).toBe('node scripts/copy-sqc-wasm.mjs');
  });

  it('escapes prop strings so they cannot break out of JSX', () => {
    const spec = emptySpec('X');
    spec.pages[0]!.blocks[0]!.props.heading = '"} />{alert(1)}<b x={"';
    const page = generateProject(spec, { framework: 'vite', catalog, versions }).files['src/pages/HomePage.tsx']!;
    expect(page).not.toContain('{alert(1)}');
    expect(page).toContain('\\u007balert(1)\\u007d');
  });

  it('copies only the blocks that are used', () => {
    const files = generateProject(ready('wallet-dashboard'), { framework: 'vite', catalog, versions }).files;
    expect(files['src/blocks/Hero.tsx']).toBeDefined();
    expect(files['src/blocks/NftDrop.tsx']).toBeUndefined();
  });

  it('throws on unknown widgets', () => {
    const spec = emptySpec();
    spec.pages[0]!.blocks[0]!.widget = 'missing';
    expect(() => generateProject(spec, { framework: 'vite', catalog, versions })).toThrow(/unknown widget/);
  });
});
