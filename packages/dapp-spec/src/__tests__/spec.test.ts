import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  BLOCK_MANIFESTS,
  TEMPLATES,
  THEME_PRESETS,
  actionsSource,
  applyThemePreset,
  blockSource,
  createCatalog,
  emptySpec,
  generateProject,
  instantiateTemplate,
  migrateSpec,
  insertChild,
  treeDepth,
  updateBlockProps,
  validateSpec,
  type ContainerNode,
  type DappBlock,
  type DappSpec,
  type LayoutNode,
} from '../index.js';

const catalog = createCatalog(WIDGET_MANIFESTS);
const RECIPIENT = 'SQ:' + 'ab'.repeat(32);

/** Copy flat locks[].props edits into each page's layout tree (instantiateTemplate deep-clones, so they diverge). */
function sync(spec: DappSpec): DappSpec {
  for (const p of spec.pages) {
    for (const b of p.blocks ?? []) p.root = updateBlockProps(p.root, b.id, b.props) as typeof p.root;
  }
  return spec;
}

function ready(id: string): DappSpec {
  const t = TEMPLATES.find((x) => x.id === id)!;
  const spec = instantiateTemplate(t);
  for (const s of t.setup) {
    const block = spec.pages.flatMap((p) => p.blocks ?? []).find((b) => b.id === s.blockId)!;
    block.props[s.prop] = RECIPIENT;
  }
  return sync(spec);
}

/** Append a block to page 0: flat list and layout tree. */
function addBlock(spec: DappSpec, block: DappBlock, page = 0): void {
  const p = spec.pages[page]!;
  (p.blocks ??= []).push(block);
  const parent = (p.root as ContainerNode).children[0] as ContainerNode;
  p.root = insertChild(p.root, parent.id, parent.children.length, { kind: 'block', block })!;
}

/** A section chain `levels` deep (the outermost counts as level 1) ending in one hero block. */
function nested(levels: number): ContainerNode {
  let node: LayoutNode = { kind: 'block', block: { id: 'leaf', widget: 'hero', props: { heading: 'x' } } };
  for (let i = levels; i >= 1; i -= 1) {
    node = { kind: i === 1 ? 'section' : 'stack', id: `n${i}`, children: [node] };
  }
  return node as ContainerNode;
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
    // Mutate the tree leaf (same object as flat blocks) and append bad leaves into the stack.
    const stack = spec.pages[0].root.children[0];
    stack.children.push({ kind: 'block', block: { id: 'x', widget: 'fee-row', props: {} } });
    stack.children.push({ kind: 'block', block: { id: 'y', widget: 'nope', props: {} } });
    spec.pages.push({ path: '/Bad Path', title: 'x', root: { kind: 'section', id: 'bad', children: [] }, blocks: [] });
    sync(spec as unknown as DappSpec);
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
    const deploy = spec.pages[0]!.blocks!.find((b) => b.id === 'deploy')!;
    deploy.props.decimals = 30;
    deploy.props.defaultSupply = '1e6';
    spec.pages[1]!.blocks![0]!.props.tokenAddress = '0xabc';
    sync(spec);
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.issues.map((i) => i.message)).toEqual(expect.arrayContaining(['max 18', 'must be a decimal amount', 'must be an SQ: address']));
  });

  it('returns a normalized copy without unknown fields', () => {
    const spec = ready('wallet-dashboard') as unknown as Record<string, any>;
    spec.owner = 'SQ:' + '00'.repeat(32);
    spec.theme.extra = 'x';
    spec.pages[0].hidden = true;
    spec.pages[0].blocks[0].script = 'alert(1)';
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.spec).not.toBe(spec);
    expect(Object.keys(r.spec).sort()).toEqual(['contracts', 'description', 'name', 'nav', 'network', 'pages', 'theme', 'version']);
    expect(Object.keys(r.spec.theme).sort()).toEqual(['base', 'overrides', 'preset']);
    expect(Object.keys(r.spec.pages[0]!).sort()).toEqual(['blocks', 'path', 'root', 'title']);
    expect(Object.keys(r.spec.pages[0]!.blocks![0]!).sort()).toEqual(['id', 'props', 'widget']);
    expect(r.spec.pages[0]!.blocks![0]!.props).toEqual(spec.pages[0].blocks[0].props);
  });

  it('rejects prototype keys in props, with or without a catalog', () => {
    const spec = ready('wallet-dashboard');
    const parsed = JSON.parse(JSON.stringify(spec).replace('"heading":', '"__proto__":{"polluted":true},"heading":')) as unknown;
    for (const r of [validateSpec(parsed), validateSpec(parsed, { catalog })]) {
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.issues).toContainEqual({ path: 'pages[0].blocks[0].props.__proto__', message: 'reserved prop name' });
    }
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
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
  it('upgrades a v0 spec to v2', () => {
    const v2 = migrateSpec({ version: 0, name: 'Old', network: 'devnet', theme: 'dark', blocks: [{ id: 'a', widget: 'hero', props: {} }] });
    expect(v2.version).toBe(2);
    expect(v2.theme).toEqual({ base: 'dark', overrides: {} });
    expect(v2.pages[0]!.path).toBe('/');
    expect(v2.pages[0]!.root).toBeTruthy();
    expect(validateSpec(v2, { catalog }).ok).toBe(true);
  });

  it('upgrades v1 flat blocks to a layout tree', () => {
    const v1 = {
      version: 1,
      name: 'Flat',
      network: 'testnet',
      theme: { base: 'splendor', overrides: {} },
      pages: [{ path: '/', title: 'Home', blocks: [{ id: 'a', widget: 'hero', props: { heading: 'Hi' } }] }],
    };
    const v2 = migrateSpec(v1);
    expect(v2.version).toBe(2);
    expect(v2.pages[0]!.root.kind).toBe('section');
    expect(validateSpec(v2, { catalog }).ok).toBe(true);
  });

  it('passes current specs through and rejects newer versions', () => {
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

  it('ignores every local env file and ships security headers', () => {
    const vite = generateProject(ready('wallet-dashboard'), { framework: 'vite', catalog, versions });
    expect(vite.files['.gitignore']).toContain('.env*.local');
    expect(vite.files['README.md']).toContain("script-src 'self' 'wasm-unsafe-eval'");
    const next = generateProject(ready('wallet-dashboard'), { framework: 'next', catalog, versions });
    expect(next.files['.gitignore']).toContain('.env*.local');
    expect(next.files['next.config.mjs']).toContain('async headers()');
    expect(next.files['next.config.mjs']).toContain("frame-ancestors 'none'");
    expect(next.files['next.config.mjs']).toContain('poweredByHeader: false');
  });

  it('escapes prop strings so they cannot break out of JSX', () => {
    const spec = emptySpec('X');
    spec.pages[0]!.blocks[0]!.props.heading = '"} />{alert(1)}<b x={"';
    const page = generateProject(spec, { framework: 'vite', catalog, versions }).files['src/pages/HomePage.tsx']!;
    expect(page).not.toContain('{alert(1)}');
    expect(page).toContain('\\u007balert(1)\\u007d');
  });

  it('paints the page with the chosen theme, not a hardcoded dark body', () => {
    const spec = ready('wallet-dashboard');
    spec.theme = { base: 'light', overrides: { accent: '#ff0000', bg: '#ffffff' } };
    for (const framework of ['vite', 'next'] as const) {
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const css = files[framework === 'vite' ? 'src/app.css' : 'app/app.css']!;
      expect(css).toContain(":root:has([data-sqc-theme='light'])");
      expect(css).toMatch(/\.dapp-root \{[^}]*background: var\(--sqc-bg/);
      expect(css).not.toMatch(/body \{[^}]*background/);
      const shell = files[framework === 'vite' ? 'src/App.tsx' : 'app/providers.tsx']!;
      expect(shell).toContain('const THEME = "light" as const;');
      expect(shell).toContain('<div className="dapp-root" style={themeOverridesToStyle(THEME_OVERRIDES)}>');
      expect(shell).toContain('accent: "#ff0000"');
    }
  });

  it('exports the contract-call block with its props', () => {
    const spec = emptySpec('Counter');
    addBlock(spec, {
      id: 'counter',
      widget: 'contract-call',
      props: { contract: RECIPIENT, functionSig: 'get()', mode: 'read', decode: 'uint256' },
    });
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    const files = generateProject(spec, { framework: 'vite', catalog, versions }).files;
    expect(files['src/blocks/ContractCall.tsx']).toContain('export function ContractCall');
    expect(files['src/pages/HomePage.tsx']).toContain('<ContractCall');
    expect(files['src/pages/HomePage.tsx']).toContain('functionSig={"get()"}');
  });

  it('contract-call needs an address and a function', () => {
    const spec = emptySpec('Counter');
    addBlock(spec, { id: 'c', widget: 'contract-call', props: { mode: 'write' } });
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.issues).toContainEqual({ path: 'pages[0].blocks[1].props.contract', message: 'required' });
      expect(r.issues).toContainEqual({ path: 'pages[0].blocks[1].props.functionSig', message: 'required' });
    }
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

describe('v1 → v2 migration (extended)', () => {
  const v1 = {
    version: 1,
    name: 'Flat',
    network: 'testnet',
    theme: { base: 'splendor', overrides: { accent: '#123456' } },
    pages: [
      { path: '/', title: 'Home', blocks: [{ id: 'a', widget: 'hero', props: { heading: 'Hi' } }, { id: 'b', widget: 'account-card', props: {} }] },
      { path: '/more', title: 'More', blocks: [{ id: 'c', widget: 'hero', props: {} }] },
    ],
  };

  it('keeps block order, ids and theme overrides, and adds contracts + nav', () => {
    const v2 = migrateSpec(v1);
    expect(v2.version).toBe(2);
    expect(v2.theme.overrides.accent).toBe('#123456');
    expect(v2.pages.map((p) => p.blocks!.map((b) => b.id))).toEqual([['a', 'b'], ['c']]);
    expect(v2.contracts).toEqual([]);
    expect(v2.nav).toEqual({ showConnect: true });
    const stack = (v2.pages[0]!.root as ContainerNode).children[0] as ContainerNode;
    expect(stack.children.map((n) => (n.kind === 'block' ? n.block.id : n.id))).toEqual(['a', 'b']);
  });

  it('does not mutate its input and is idempotent', () => {
    const copy = JSON.parse(JSON.stringify(v1));
    const once = migrateSpec(v1);
    expect(v1).toEqual(copy);
    expect(migrateSpec(once)).toEqual(once);
    expect(validateSpec(once, { catalog }).ok).toBe(true);
  });
});

describe('theme presets', () => {
  it('has unique ids and every preset validates as a theme', () => {
    expect(new Set(THEME_PRESETS.map((p) => p.id)).size).toBe(THEME_PRESETS.length);
    for (const preset of THEME_PRESETS) {
      const spec = ready('wallet-dashboard');
      const applied = applyThemePreset(preset.id)!;
      expect(applied.preset).toBe(preset.id);
      spec.theme = applied;
      const r = validateSpec(spec, { catalog });
      expect(r.ok, `${preset.id}: ${JSON.stringify(!r.ok && r.issues)}`).toBe(true);
    }
  });

  it('merges onto current overrides (preset wins) and returns null for unknown ids', () => {
    const merged = applyThemePreset('midnight', { base: 'splendor', overrides: { accent: '#000000', danger: '#ff0000' } })!;
    expect(merged.base).toBe('dark');
    expect(merged.overrides.danger).toBe('#ff0000');
    expect(merged.overrides.accent).toBe(THEME_PRESETS.find((p) => p.id === 'midnight')!.overrides.accent);
    expect(applyThemePreset('does-not-exist')).toBeNull();
  });
});

describe('layout tree limits', () => {
  function withRoot(root: ContainerNode): DappSpec {
    const spec = ready('wallet-dashboard');
    spec.pages = [{ path: '/', title: 'Home', root, blocks: [{ id: 'leaf', widget: 'hero', props: { heading: 'x' } }] }];
    return spec;
  }

  it('accepts a tree at the depth limit and rejects one level deeper', () => {
    const ok = nested(5); // 5 containers + 1 leaf block = depth 6
    expect(treeDepth(ok)).toBe(6);
    expect(validateSpec(withRoot(ok), { catalog }).ok).toBe(true);
    const deep = nested(6);
    expect(treeDepth(deep)).toBe(7);
    const r = validateSpec(withRoot(deep), { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.issues.some((i) => /max tree depth/.test(i.message))).toBe(true);
  });

  it('rejects pages with too many nodes', () => {
    const root: ContainerNode = {
      kind: 'section',
      id: 'root',
      children: Array.from({ length: 210 }, (_, i) => ({ kind: 'stack' as const, id: `s${i}`, children: [] })),
    };
    const r = validateSpec(withRoot(root), { catalog });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.issues.some((i) => /nodes per page/.test(i.message))).toBe(true);
  });
});

describe('generateProject: contracts.ts, actions.ts and vendor mode', () => {
  const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };

  function withContractsAndActions(): DappSpec {
    const spec = emptySpec('Actions');
    spec.pages.push({ path: '/app', title: 'App', root: { kind: 'section', id: 'app-root', children: [] }, blocks: [] });
    spec.contracts = [
      {
        id: 'counter',
        name: 'Counter',
        address: RECIPIENT,
        functions: [
          { name: 'get', signature: 'get()', params: [], returns: 'uint256', kind: 'read' },
          { name: 'inc', signature: 'inc()', params: [], kind: 'write' },
        ],
      },
    ];
    addBlock(spec, { id: 'go', widget: 'action-button', props: { label: 'Open app', href: '/app' } });
    addBlock(spec, { id: 'docs', widget: 'action-button', props: { label: 'Docs', href: 'https://docs.splendor.org' } });
    addBlock(spec, { id: 'cta', widget: 'hero-centered', props: { heading: 'Hi', ctaLabel: 'Start', ctaPath: '/app' } });
    addBlock(spec, {
      id: 'bump',
      widget: 'action-button',
      props: { label: 'Bump', href: 'contract:counter.inc' },
    });
    return spec;
  }

  for (const framework of ['vite', 'next'] as const) {
    it(`${framework}: emits contracts.ts with typed helpers and actions.ts`, () => {
      const spec = withContractsAndActions();
      const v = validateSpec(spec, { catalog });
      expect(v.ok, JSON.stringify(!v.ok && v.issues)).toBe(true);
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const dir = framework === 'vite' ? 'src' : 'app';
      const contracts = files[`${dir}/contracts.ts`]!;
      const actions = files[`${dir}/actions.ts`]!;
      expect(contracts).toContain('export const CONTRACTS');
      expect(contracts).toContain('export function readCall');
      expect(contracts).toContain('export function writeCall');
      expect(contracts).toContain('useReadContract');
      expect(contracts).toContain('useContract');
      expect(contracts).toContain(JSON.stringify(RECIPIENT));
      expect(actions).toContain("from './contracts'");
      expect(actions).toContain('export const ACTIONS');
      expect(actions).toContain('export async function runAction');
      expect(actions).toContain('{"type":"page","path":"/app"}');
      expect(actions).toContain('{"type":"url","href":"https://docs.splendor.org"}');
      expect(actions).toContain('{"type":"contract","contractId":"counter","fn":"inc"}');
      expect(actions).toContain('id: "cta"');
      expect(actions).toContain(framework === 'vite' ? 'window.location.hash' : 'navigate?: (path: string) => void');
      expect(actions).toContain('safeUrl');
    });
  }

  it('emits empty-but-valid helpers when nothing is bound', () => {
    for (const framework of ['vite', 'next'] as const) {
      const files = generateProject(ready('wallet-dashboard'), { framework, catalog, versions }).files;
      const dir = framework === 'vite' ? 'src' : 'app';
      expect(files[`${dir}/contracts.ts`]).toContain('export const CONTRACTS = {} as const');
      expect(files[`${dir}/actions.ts`]).toContain('export const ACTIONS: readonly ActionEntry[] = [];');
    }
  });

  it('actionsSource is exported and works without a catalog', () => {
    const spec = withContractsAndActions();
    // A BlockAction stored as JSON in an ction prop is understood too (custom blocks).
    addBlock(spec, { id: 'custom', widget: 'hero', props: { action: JSON.stringify({ type: 'url', href: 'https://example.com' }) } });
    const src = actionsSource(spec);
    expect(src).toContain('id: "custom"');
    expect(src).toContain('{"type":"url","href":"https://example.com"}');
    expect(src).toContain('"id": "go"'.replace('"id"', 'id'));
    expect(src).toContain('export function actionHref');
  });

  it('vendor mode pins file: dependencies and adds the decode script', () => {
    for (const framework of ['vite', 'next'] as const) {
      const project = generateProject(ready('wallet-dashboard'), {
        framework,
        catalog,
        versions,
        sdk: 'vendor',
        vendorFiles: { 'vendor/@splendor/sqc-sdk/package.json': '{}', 'vendor/@splendor/connect-react/package.json': '{}' },
        vendorWasmBase64: 'AAEC',
      });
      const pkg = JSON.parse(project.files['package.json']!);
      expect(pkg.dependencies['@splendor/connect-react']).toBe('file:vendor/@splendor/connect-react');
      expect(pkg.dependencies['@splendor/sqc-sdk']).toBe('file:vendor/@splendor/sqc-sdk');
      expect(pkg.scripts.postinstall).toBe('node scripts/decode-wasm.mjs');
      expect(project.files['scripts/decode-wasm.mjs']).toContain('sqc_signer.wasm.b64');
      expect(project.files['vendor/@splendor/sqc-sdk/package.json']).toBe('{}');
      expect(project.files['vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64']).toBe('AAEC');
    }
  });

  it('npm mode never emits file: dependencies', () => {
    const pkg = JSON.parse(generateProject(ready('wallet-dashboard'), { framework: 'vite', catalog, versions }).files['package.json']!);
    expect(JSON.stringify(pkg.dependencies)).not.toContain('file:');
  });
});