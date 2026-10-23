import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  BLOCK_MANIFESTS,
  PAGE_TEMPLATES,
  TEMPLATES,
  THEME_PRESETS,
  actionsSource,
  applyThemePreset,
  blockSource,
  createCatalog,
  emptySpec,
  generateProject,
  instantiateTemplate,
  isSafeLink,
  migrateSpec,
  insertChild,
  styleToCss,
  treeDepth,
  updateBlockProps,
  validateSpec,
  type ContainerNode,
  type DappBlock,
  type DappSpec,
  type LayoutNode,
} from '../index.js';
import { pageHref } from '../../blocks/pageHref.js';
import { parseList } from '../../blocks/parseList.js';

const catalog = createCatalog(WIDGET_MANIFESTS);
const RECIPIENT = 'SQ:' + 'ab'.repeat(32);

/** Copy flat locks[].props edits into each page's layout tree (instantiateTemplate deep-clones, so they diverge). */
function sync(spec: DappSpec): DappSpec {
  for (const p of spec.pages) {
    for (const b of p.blocks ?? []) p.root = updateBlockProps(p.root, b.id, b.props) as typeof p.root;
  }
  return spec;
}

describe('link props', () => {
  it('accepts page paths, http(s) URLs, contract calls and JSON actions', () => {
    for (const ok of ['', '/about', 'about', '#/app', 'https://splendor.org/x?y=1', 'contract:counter.inc', '{"type":"page","path":"/a"}']) {
      expect(isSafeLink(ok), ok).toBe(true);
    }
  });

  it('rejects script, data and other schemes in button, hero and navbar links', () => {
    for (const bad of ['javascript:alert(1)', 'JAVASCRIPT:alert(1)', 'data:text/html,<b>', 'vbscript:x', '//evil.example', '{"type":"url","href":"javascript:x"}']) {
      expect(isSafeLink(bad), bad).toBe(false);
    }
    const spec = emptySpec('x');
    const blocks: DappBlock[] = [
      { id: 'btn', widget: 'action-button', props: { label: 'Go', href: 'javascript:alert(1)' } },
      { id: 'nav', widget: 'navbar', props: { links: 'Home|/,Evil|javascript:alert(1)' } },
    ];
    spec.pages[0]!.blocks = blocks;
    spec.pages[0]!.root = { kind: 'section', id: 'home-root', children: blocks.map((block) => ({ kind: 'block' as const, block })) };
    const res = validateSpec(spec, { catalog });
    expect(res.ok).toBe(false);
    const paths = res.ok ? [] : res.issues.map((i) => i.path);
    expect(paths.some((p) => p.endsWith('props.href'))).toBe(true);
    expect(paths.some((p) => p.endsWith('props.links'))).toBe(true);
  });
});

describe('page templates', () => {
  it.each(PAGE_TEMPLATES.map((t) => t.id))('%s builds a valid page at a sub-path', (id) => {
    const tpl = PAGE_TEMPLATES.find((t) => t.id === id)!;
    const spec = emptySpec('x');
    spec.pages.push(tpl.build('/my-page', 'My page'));
    const res = validateSpec(spec, { catalog });
    expect(res.ok ? [] : res.issues).toEqual([]);
  });
});

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

const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };

function extraPage(path: string, title = 'Page'): DappSpec['pages'][number] {
  return { path, title, root: { kind: 'section', id: `p${path.replace(/\W+/g, '-')}-root`, children: [] }, blocks: [] };
}

function issuesOf(spec: unknown): { path: string; message: string }[] {
  const r = validateSpec(spec, { catalog });
  return r.ok ? [] : r.issues;
}

describe('generateProject: unique page components', () => {
  it("gives '/' and '/home' different components and files", () => {
    const spec = emptySpec('Dup');
    spec.pages.push(extraPage('/home', 'Home 2'));
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    const vite = generateProject(spec, { framework: 'vite', catalog, versions }).files;
    const pageFiles = Object.keys(vite).filter((f) => f.startsWith('src/pages/'));
    expect(pageFiles.sort()).toEqual(['src/pages/HomePage.tsx', 'src/pages/HomePage2.tsx']);
    const app = vite['src/App.tsx']!;
    expect(app.match(/import \{ HomePage \} from/g)).toHaveLength(1);
    expect(app).toContain("import { HomePage2 } from './pages/HomePage2';");
    expect(vite['src/pages/HomePage2.tsx']).toContain('export function HomePage2()');

    const next = generateProject(spec, { framework: 'next', catalog, versions }).files;
    expect(next['app/HomePage.tsx']).toContain('export function HomePage()');
    expect(next['app/home/HomePage2.tsx']).toContain('export function HomePage2()');
    expect(next['app/home/page.tsx']).toContain("import { HomePage2 } from './HomePage2';");
  });

  it('keeps HomePage for "/" even when it is not the first page, and avoids case-only clashes', () => {
    const spec = emptySpec('Dup');
    spec.pages.unshift(extraPage('/home', 'Home 2'));
    spec.pages.push(extraPage('/a-b', 'AB'), extraPage('/ab', 'Ab'));
    const files = generateProject(spec, { framework: 'vite', catalog, versions }).files;
    const names = Object.keys(files).filter((f) => f.startsWith('src/pages/'));
    expect(names).toHaveLength(4);
    expect(new Set(names.map((n) => n.toLowerCase())).size).toBe(4);
    expect(files['src/pages/HomePage.tsx']).toContain('export function HomePage()');
    expect(files['src/pages/HomePage.tsx']).toContain('dapp-page');
  });
});

describe('page links in exports', () => {
  it('pageHref (preview/vite default) is a hash link; external URLs pass through', () => {
    expect(pageHref('/about')).toBe('#/about');
    expect(pageHref('about')).toBe('#/about');
    expect(pageHref('#/about')).toBe('#/about');
    expect(pageHref('')).toBe('');
    expect(pageHref('https://splendor.org/x')).toBe('https://splendor.org/x');
    expect(pageHref('HTTP://example.com')).toBe('HTTP://example.com');
  });

  function withButtons(): DappSpec {
    const spec = emptySpec('Links');
    addBlock(spec, { id: 'go', widget: 'action-button', props: { label: 'Go', href: '/app' } });
    addBlock(spec, { id: 'hero2', widget: 'hero-centered', props: { heading: 'x', ctaLabel: 'Start', ctaPath: '/app' } });
    return spec;
  }

  it('vite keeps hash links, next ships a plain-path pageHref', () => {
    const vite = generateProject(withButtons(), { framework: 'vite', catalog, versions }).files;
    expect(vite['src/blocks/pageHref.ts']).toBe(blockSource('pageHref.ts'));
    expect(vite['src/blocks/pageHref.ts']).toContain('`#${');
    expect(vite['src/blocks/ActionButton.tsx']).toContain("from './pageHref'");
    expect(vite['src/blocks/parseList.ts']).toBeDefined();

    const next = generateProject(withButtons(), { framework: 'next', catalog, versions }).files;
    const src = next['blocks/pageHref.ts']!;
    expect(src).toContain('export function pageHref');
    expect(src).not.toContain('#${');
    expect(src).not.toContain("'use client'");
    expect(src).toContain('/^https?:\\/\\//i');
    expect(next['blocks/ActionButton.tsx']).toMatch(/^'use client';/);
    expect(next['blocks/ActionButton.tsx']).toContain("from './pageHref'");
    expect(next['blocks/parseList.ts']).not.toContain("'use client'");
  });

  it('emits no shared helpers when no block is used', () => {
    const spec = ready('wallet-dashboard');
    const only = generateProject({ ...spec, pages: spec.pages.map((p) => ({ ...p, root: extraPage('/').root, blocks: [] })) }, { framework: 'vite', catalog, versions }).files;
    expect(only['src/blocks/pageHref.ts']).toBeUndefined();
  });

  it('every block that links imports the shared helper (no per-block hash code)', () => {
    for (const id of ['action-button', 'hero-centered', 'hero-gradient', 'hero-split', 'cta-banner', 'pricing-card', 'navbar']) {
      const m = BLOCK_MANIFESTS.find((b) => b.id === id)!;
      const src = blockSource(m.file);
      expect(src, id).toContain("from './pageHref'");
      expect(src, id).not.toContain('`#${');
    }
  });

  it('link blocks render no disabled placeholder button when there is no path', () => {
    for (const id of ['hero-centered', 'hero-gradient', 'hero-split', 'cta-banner', 'pricing-card', 'navbar']) {
      const src = blockSource(BLOCK_MANIFESTS.find((b) => b.id === id)!.file);
      expect(src, id).not.toMatch(/<button[^>]*disabled/);
    }
  });
});

describe('list props', () => {
  it('splits on commas, or on newlines when the value has one', () => {
    expect(parseList('a, b ,,c')).toEqual(['a', 'b', 'c']);
    expect(parseList('Fast|Ship, then test\nSecure|Post-quantum\r\n\n')).toEqual(['Fast|Ship, then test', 'Secure|Post-quantum']);
    expect(parseList('')).toEqual([]);
  });

  it('validates navbar links per newline item', () => {
    const spec = emptySpec('x');
    addBlock(spec, { id: 'nav', widget: 'navbar', props: { links: 'Home|/\nEvil, Inc|javascript:alert(1)' } });
    expect(issuesOf(spec).map((i) => i.path)).toContain('pages[0].blocks[1].props.links');
    spec.pages[0]!.blocks![1]!.props.links = 'Home|/\nAbout, us|/about';
    const root = spec.pages[0]!.root;
    spec.pages[0]!.root = updateBlockProps(root, 'nav', spec.pages[0]!.blocks![1]!.props) as typeof root;
    expect(issuesOf(spec)).toEqual([]);
  });
});

describe('validateSpec: ids, roots, nav, contracts', () => {
  it('rejects a block id that equals a container id (one id namespace)', () => {
    const spec = emptySpec('x');
    const rootId = (spec.pages[0]!.root as ContainerNode).id;
    addBlock(spec, { id: rootId, widget: 'hero', props: { heading: 'x' } });
    expect(issuesOf(spec)).toContainEqual({ path: 'pages[0].blocks[1].id', message: 'duplicate block id' });
  });

  it('rejects a container id that repeats on another page', () => {
    const spec = emptySpec('x');
    spec.pages.push({ ...extraPage('/two'), root: { kind: 'section', id: (spec.pages[0]!.root as ContainerNode).id, children: [] } });
    expect(issuesOf(spec).some((i) => i.message === 'duplicate node id')).toBe(true);
  });

  it('requires the page root to be a container', () => {
    const spec = emptySpec('x') as unknown as Record<string, any>;
    spec.pages[0].root = { kind: 'block', block: { id: 'solo', widget: 'hero', props: {} } };
    const issues = issuesOf(spec);
    expect(issues.some((i) => i.path === 'pages[0].root.kind' && /container/.test(i.message))).toBe(true);
  });

  it('validates nav fields', () => {
    const spec = emptySpec('x') as unknown as Record<string, any>;
    spec.nav = { brand: 'b'.repeat(81), footer: 'f'.repeat(201), showConnect: 'yes', links: ['/', '/missing', 5] };
    const paths = issuesOf(spec).map((i) => i.path);
    expect(paths).toEqual(expect.arrayContaining(['nav.brand', 'nav.footer', 'nav.showConnect', 'nav.links[1]', 'nav.links[2]']));

    spec.nav = { links: Array.from({ length: 21 }, () => '/') };
    expect(issuesOf(spec).map((i) => i.path)).toContain('nav.links');
    spec.nav = 'nope';
    expect(issuesOf(spec).map((i) => i.path)).toContain('nav');

    spec.nav = { brand: 'b'.repeat(80), footer: 'f'.repeat(200), showConnect: false, links: ['/'] };
    expect(issuesOf(spec)).toEqual([]);
  });

  function withFunctions(functions: unknown[], extra: Record<string, unknown> = {}): DappSpec {
    const spec = emptySpec('x') as unknown as Record<string, any>;
    spec.contracts = [{ id: 'c', name: 'C', address: RECIPIENT, functions, ...extra }];
    return spec as DappSpec;
  }
  const fn = (signature: string, kind: unknown = 'read', more: Record<string, unknown> = {}) => ({ name: 'f', signature, params: [], kind, ...more });

  it('accepts well-formed contracts', () => {
    const spec = withFunctions([fn('get()'), fn('set(uint256)', 'write', { params: [{ name: 'v', type: 'uint256' }] }), fn('$_x1(address,uint256[])')]);
    expect(issuesOf(spec)).toEqual([]);
  });

  it('rejects malformed signatures, kinds and duplicates', () => {
    const spec = withFunctions([fn('get'), fn('1bad()'), fn('f(a(b))'), fn('ok()', 'both'), fn('dup()'), fn('dup()')]);
    const issues = issuesOf(spec);
    const at = (p: string) => issues.filter((i) => i.path === p).map((i) => i.message);
    expect(at('contracts[0].functions[0].signature')).toHaveLength(1);
    expect(at('contracts[0].functions[1].signature')).toHaveLength(1);
    expect(at('contracts[0].functions[2].signature')).toHaveLength(1);
    expect(at('contracts[0].functions[3].kind')).toHaveLength(1);
    expect(at('contracts[0].functions[5].signature')).toEqual(['duplicate signature']);
    expect(at('contracts[0].functions[4].signature')).toEqual([]);
  });

  it('limits names, function count and params', () => {
    const tooMany = withFunctions(Array.from({ length: 65 }, (_, i) => fn(`f${i}()`)));
    expect(issuesOf(tooMany).map((i) => i.path)).toContain('contracts[0].functions');
    const max = withFunctions(Array.from({ length: 64 }, (_, i) => fn(`f${i}()`)));
    expect(issuesOf(max)).toEqual([]);

    const longName = withFunctions([fn('a()')], { name: 'n'.repeat(81) });
    expect(issuesOf(longName).map((i) => i.path)).toContain('contracts[0].name');

    const params = Array.from({ length: 17 }, () => ({ name: 'a', type: 'uint256' }));
    expect(issuesOf(withFunctions([fn('a()', 'read', { params })])).map((i) => i.path)).toContain('contracts[0].functions[0].params');
    expect(issuesOf(withFunctions([fn('a()', 'read', { params: [{ name: 'a' }, 'x', { name: 1, type: 't' }] })])).map((i) => i.path)).toEqual(
      expect.arrayContaining(['contracts[0].functions[0].params[0].type', 'contracts[0].functions[0].params[1]', 'contracts[0].functions[0].params[2].name']),
    );
  });

  it('keeps CSS values to plain tokens: no image(), image-set(), src(), url() or //', () => {
    for (const bad of ['image-set(x 1x)', 'IMAGE-SET(x)', 'image(x)', 'src(x)', 'url(x)', '//evil.example/x', 'expression(1)']) {
      const spec = emptySpec('x');
      spec.theme.overrides.heroGradient = bad;
      expect(issuesOf(spec).map((i) => i.path), bad).toContain('theme.overrides.heroGradient');
      const styled = emptySpec('x');
      (styled.pages[0]!.root as ContainerNode).style = { background: bad };
      expect(issuesOf(styled).map((i) => i.path), bad).toContain('pages[0].root.style.background');
    }
    for (const good of ['calc(100% - 2rem)', 'calc(1rem * 2 + 4px)', 'calc(2*3rem+1px)', 'linear-gradient(135deg, #7c5cff, #00d4ff)']) {
      const spec = emptySpec('x');
      spec.theme.overrides.heroGradient = good;
      (spec.pages[0]!.root as ContainerNode).style = { width: good };
      expect(issuesOf(spec), good).toEqual([]);
    }
  });

  it('reports a clear message for too many blocks', () => {
    const spec = emptySpec('x');
    for (let i = 0; i < 41; i += 1) addBlock(spec, { id: `b${i}`, widget: 'spacer', props: {} });
    const msgs = issuesOf(spec).map((i) => i.message);
    expect(msgs).toContain('max 40 blocks per page');
    expect(msgs.some((m) => m.startsWith('blocks must be an array'))).toBe(false);
  });
});

describe('validateSpec: https-only and integer props', () => {
  function one(widget: string, props: Record<string, string | number | boolean>): { path: string; message: string }[] {
    const spec = emptySpec('x');
    addBlock(spec, { id: 'blk', widget, props });
    return issuesOf(spec);
  }

  it('image sources and the members API must be https', () => {
    expect(one('image-block', { src: 'http://example.com/a.png' })).toContainEqual({ path: 'pages[0].blocks[1].props.src', message: 'must be an https URL' });
    expect(one('image-block', { src: 'https://example.com/a.png' })).toEqual([]);
    expect(one('hero-split', { heading: 'x', imageUrl: 'http://example.com/a.png' }).map((i) => i.path)).toContain('pages[0].blocks[1].props.imageUrl');
    expect(one('members-area', { apiUrl: 'http://api.example.com' })).toContainEqual({ path: 'pages[0].blocks[1].props.apiUrl', message: 'must be an https URL' });
    expect(one('members-area', { apiUrl: 'https://api.example.com' })).toEqual([]);
    expect(one('members-area', {})).toEqual([]);
  });

  it('plain url props (no httpsOnly) still accept http', () => {
    const custom = [{ id: 'link-card', title: 'x', description: '', category: 'x', kind: 'block' as const, importName: 'LinkCard', status: 'stable' as const, placeable: true, defaults: {}, props: [{ name: 'u', label: 'U', type: 'string' as const, format: 'url' as const }] }];
    const spec = emptySpec('x');
    addBlock(spec, { id: 'lc', widget: 'link-card', props: { u: 'http://example.com' } });
    expect(validateSpec(spec, { catalog: [...catalog, ...custom] }).ok).toBe(true);
  });

  it('token ids and mint amounts are whole numbers', () => {
    expect(one('nft-drop', { tokenId: '7', amount: '10' })).toEqual([]);
    expect(one('nft-drop', { tokenId: '0', amount: '1' })).toEqual([]);
    for (const bad of ['1.5', '-1', '01', '1e3', 'abc', ' 1']) {
      const paths = one('nft-drop', { tokenId: bad, amount: bad }).map((i) => i.path);
      expect(paths, bad).toEqual(['pages[0].blocks[1].props.tokenId', 'pages[0].blocks[1].props.amount']);
    }
    const drop = BLOCK_MANIFESTS.find((b) => b.id === 'nft-drop')!;
    expect(drop.props.filter((p) => ['tokenId', 'amount'].includes(p.name)).map((p) => p.format)).toEqual(['integer', 'integer']);
  });

  it('nft-gallery token ids must be whole numbers', () => {
    expect(one('nft-gallery', { collection: RECIPIENT, tokenIds: '1,2,3' })).toEqual([]);
    expect(one('nft-gallery', { collection: RECIPIENT, tokenIds: '1\n2\n3' })).toEqual([]);
    expect(one('nft-gallery', { collection: RECIPIENT, tokenIds: '1,x,2.5' }).map((i) => i.path)).toEqual(['pages[0].blocks[1].props.tokenIds']);
  });
});

describe('styleToCss', () => {
  it('uses flex-basis on columns so .dapp-column flex does not override the width', () => {
    expect(styleToCss({ width: '50%' }, 'column')).toEqual({ flex: '0 1 50%' });
    expect(styleToCss({ width: '50%' }, 'row')).toEqual({ width: '50%' });
    expect(styleToCss({ width: '50%' })).toEqual({ width: '50%' });
  });

  it('maps align per container kind', () => {
    expect(styleToCss({ align: 'center' }, 'stack')).toEqual({ alignItems: 'center' });
    expect(styleToCss({ align: 'end' }, 'column')).toEqual({ alignItems: 'flex-end' });
    expect(styleToCss({ align: 'start' })).toEqual({ alignItems: 'flex-start' });
    expect(styleToCss({ align: 'center' }, 'row')).toEqual({ alignItems: 'center', justifyContent: 'center' });
    expect(styleToCss({ align: 'end' }, 'row')).toEqual({ alignItems: 'flex-end', justifyContent: 'flex-end' });
    expect(styleToCss({ align: 'between' }, 'row')).toEqual({ justifyContent: 'space-between' });
    expect(styleToCss({ align: 'stretch' }, 'row')).toEqual({ alignItems: 'stretch' });
  });

  it('generated code passes the container kind through', () => {
    const spec = emptySpec('x');
    const root = spec.pages[0]!.root as ContainerNode;
    spec.pages[0]!.root = {
      ...root,
      children: [{ kind: 'row', id: 'r', style: { align: 'center' }, children: [{ kind: 'column', id: 'c', style: { width: '40%' }, children: [] }] }],
    };
    const page = generateProject(spec, { framework: 'vite', catalog, versions }).files['src/pages/HomePage.tsx']!;
    expect(page).toContain('alignItems: "center", justifyContent: "center"');
    expect(page).toContain('flex: "0 1 40%"');
    expect(page).not.toContain('width: "40%"');
  });
});

describe('generateProject: spec.nav', () => {
  function navSpec(nav: DappSpec['nav']): DappSpec {
    const spec = emptySpec('Shell App');
    spec.pages.push(extraPage('/about', 'About'), extraPage('/docs', 'Docs'));
    if (nav) spec.nav = nav;
    return spec;
  }
  const shellOf = (spec: DappSpec, framework: 'vite' | 'next') =>
    generateProject(spec, { framework, catalog, versions }).files[framework === 'vite' ? 'src/App.tsx' : 'app/providers.tsx']!;

  for (const framework of ['vite', 'next'] as const) {
    it(`${framework}: defaults to the app name, the connect button and page order`, () => {
      const shell = shellOf(navSpec(undefined), framework);
      expect(shell).toContain('<p className="dapp-brand">Shell App</p>');
      expect(shell).toContain('<ConnectKit />');
      expect(shell).toContain('  ConnectKit,');
      expect(shell).not.toContain('dapp-footer');
      const order = ['"/"', '"/about"', '"/docs"'].map((p) => shell.indexOf(`"path":${p}`));
      expect(order.every((i) => i >= 0)).toBe(true);
      expect([...order].sort((a, b) => a - b)).toEqual(order);
    });

    it(`${framework}: honours brand, showConnect, links order and footer`, () => {
      const shell = shellOf(navSpec({ brand: 'Acme <b>&</b> {x}', showConnect: false, links: ['/docs', '/', '/ghost', '/docs'], footer: 'Made by Acme & co {c}' }), framework);
      expect(shell).toContain('<p className="dapp-brand">Acme &lt;b&gt;&amp;&lt;/b&gt; &#123;x&#125;</p>');
      expect(shell).not.toContain('<ConnectKit />');
      expect(shell).not.toContain('  ConnectKit,');
      expect(shell).toContain('<footer className="dapp-footer">Made by Acme &amp; co &#123;c&#125;</footer>');
      // An explicit list is the menu: listed pages in that order, unlisted pages (/about) left out.
      const menu = shell.split('\n').find((l) => /^const (NAV_LINKS|ROUTES) = \[\{"path"/.test(l))!;
      expect(menu).toBe(`const ${framework === 'vite' ? 'NAV_LINKS' : 'ROUTES'} = [{"path":"/docs","title":"Docs"},{"path":"/","title":"Home"}];`);
      expect(shell).not.toContain('ghost');
    });

    it(`${framework}: blank brand falls back to the app name`, () => {
      expect(shellOf(navSpec({ brand: '   ' }), framework)).toContain('<p className="dapp-brand">Shell App</p>');
    });
  }

  it('ships CSS for the footer', () => {
    expect(generateProject(navSpec({ footer: 'x' }), { framework: 'vite', catalog, versions }).files['src/app.css']).toContain('.dapp-footer');
  });
});

import { bindBlockProps, signatureTypes } from '../bind.js';

describe('data blocks', () => {
  const entry = (id: string) => catalog.find((c) => c.id === id)!;
  const prop = (id: string, name: string) => entry(id).props.find((p) => p.name === name)!;
  const TOKEN = 'SQ:' + 'de'.repeat(32);
  const COUNTER = 'SQ:' + 'c0'.repeat(32);

  it('the catalog offers token-stats, tokenomics, live-stat and contract-form', () => {
    for (const id of ['token-stats', 'tokenomics', 'live-stat', 'contract-form']) {
      const e = entry(id);
      expect(e, id).toBeDefined();
      expect(e.kind).toBe('block');
      expect(e.placeable).toBe(true);
      expect(e.hidden).toBeUndefined();
      expect(blockSource(`${e.importName}.tsx`)).toContain(`export function ${e.importName}`);
    }
    expect(entry('token-stats').category).toBe('tokens');
    expect(entry('tokenomics').category).toBe('tokens');
    expect(entry('live-stat').category).toBe('modules');
    expect(entry('contract-form').category).toBe('modules');
    expect(entry('token-stats').thumb).toBe('token-stats');

    expect(prop('token-stats', 'contractId')).toMatchObject({ format: 'contract', contractKind: 'token', addressProp: 'tokenAddress' });
    expect(prop('token-stats', 'tokenAddress').format).toBe('address');
    expect(prop('token-stats', 'decimals')).toMatchObject({ type: 'number', min: 0, max: 36 });
    expect(prop('token-stats', 'layout').options?.map((o) => o.value)).toEqual(['row', 'card']);

    expect(prop('tokenomics', 'items')).toMatchObject({ format: 'list' });
    expect(prop('tokenomics', 'items').columns?.map((c) => c.key)).toEqual(['label', 'percent', 'note']);
    expect(prop('tokenomics', 'contractId')).toMatchObject({ format: 'contract', contractKind: 'token', addressProp: 'tokenAddress' });
    expect(prop('tokenomics', 'chart').options?.map((o) => o.value)).toEqual(['donut', 'bar']);
    expect(entry('tokenomics').defaults.items).toBe('Community|40|Airdrops and rewards\nTreasury|25|\nTeam|20|4-year vesting\nLiquidity|15|');

    expect(prop('live-stat', 'contractId')).toMatchObject({ format: 'contract', contractKind: 'any', addressProp: 'contract' });
    expect(prop('live-stat', 'functionSig')).toMatchObject({ format: 'function', functionKind: 'read', contractProp: 'contractId' });
    expect(prop('live-stat', 'format').options?.map((o) => o.value)).toEqual(['number', 'token', 'address', 'text', 'bool']);
    expect(prop('live-stat', 'refreshMs')).toMatchObject({ type: 'number', min: 0, max: 120_000 });

    expect(prop('contract-form', 'contractId')).toMatchObject({ format: 'contract', contractKind: 'any', addressProp: 'contract' });
    expect(prop('contract-form', 'functionSig')).toMatchObject({ format: 'function', functionKind: 'write', contractProp: 'contractId' });
    expect(prop('contract-form', 'successPath').format).toBe('action');
  });

  it('existing contract blocks gain a contract binding and function pickers', () => {
    for (const id of ['contract-read', 'contract-write', 'contract-call', 'contract-action']) {
      const props = entry(id).props;
      expect(props[1]!.name, id).toBe('contractId');
      expect(props[1]).toMatchObject({ format: 'contract', contractKind: 'any', addressProp: 'contract' });
      expect(props.find((p) => p.name === 'functionSig')).toMatchObject({ format: 'function', contractProp: 'contractId' });
    }
    // contract-call keeps its address `required`; a contract pick satisfies it (see validate).
    const spec = emptySpec('x');
    spec.contracts = [{ id: 'app', name: 'App', address: `SQ:${'cd'.repeat(32)}`, functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' }] }];
    addBlock(spec, { id: 'c', widget: 'contract-call', props: { contractId: 'app', functionSig: 'get()' } });
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    expect(prop('contract-read', 'functionSig').functionKind).toBe('read');
    expect(prop('contract-write', 'functionSig').functionKind).toBe('write');
    expect(prop('contract-action', 'functionSig').functionKind).toBe('write');
    expect(prop('contract-call', 'functionSig').functionKind).toBeUndefined();
    expect(prop('token-info', 'contractId')).toMatchObject({ format: 'contract', contractKind: 'token', addressProp: 'tokenAddress' });
  });

  it('the loose form-* blocks are hidden from the palette but still placeable', () => {
    for (const id of ['form-text', 'form-amount', 'form-address', 'form-select', 'form-toggle']) {
      expect(entry(id).hidden, id).toBe(true);
      expect(entry(id).placeable, id).toBe(true);
    }
  });

  it('bindBlockProps fills contract and fnParams for a live-stat', () => {
    const contracts = [
      {
        id: 'counter',
        name: 'Counter',
        address: COUNTER,
        functions: [{ name: 'get', signature: 'get()', params: [], returns: 'uint256', kind: 'read' as const }],
      },
      {
        id: 'vault',
        name: 'Vault',
        address: RECIPIENT,
        functions: [
          { name: 'balanceOf', signature: 'balanceOf(address)', params: [{ name: 'owner', type: 'address' }], returns: 'uint256', kind: 'read' as const },
        ],
      },
    ];
    const block: DappBlock = { id: 'ls', widget: 'live-stat', props: { label: 'Count', contractId: 'counter', functionSig: 'get()' } };
    const bound = bindBlockProps(block, entry('live-stat'), contracts);
    expect(bound.props.contract).toBe(COUNTER);
    expect(bound.props).not.toHaveProperty('contractId');
    expect(bound.props.functionSig).toBe('get()');
    expect(bound.props.fnParams).toBe('[]');
    expect(bound.addressRefs).toEqual({ contract: 'counter' });

    const withArg: DappBlock = { id: 'ls2', widget: 'live-stat', props: { contractId: 'vault', functionSig: 'balanceOf(address)' } };
    const b2 = bindBlockProps(withArg, entry('live-stat'), contracts);
    expect(JSON.parse(String(b2.props.fnParams))).toEqual([{ name: 'owner', type: 'address' }]);
    expect(signatureTypes('balanceOf(address)')).toEqual(['address']);
  });

  it('bindBlockProps hands a token contract its address and decimals', () => {
    const contracts = [{ id: 'gold', name: 'Gold', address: TOKEN, functions: [], kind: 'token' as const, decimals: 6 }];
    const block: DappBlock = { id: 'ts', widget: 'token-stats', props: { contractId: 'gold' } };
    const bound = bindBlockProps(block, entry('token-stats'), contracts);
    expect(bound.props.tokenAddress).toBe(TOKEN);
    expect(bound.props.decimals).toBe(6);
    expect(bound.addressRefs).toEqual({ tokenAddress: 'gold' });
  });

  for (const framework of ['vite', 'next'] as const) {
    it(`${framework}: a token-stats bound to a token contract references CONTRACTS and copies TokenStats.tsx`, () => {
      const spec = emptySpec('Token page');
      spec.contracts = [{ id: 'gold', name: 'Gold', address: TOKEN, functions: [], kind: 'token', decimals: 6 }];
      addBlock(spec, { id: 'stats', widget: 'token-stats', props: { title: 'Gold', contractId: 'gold', showSupply: true } });
      const v = validateSpec(spec, { catalog });
      expect(v.ok, JSON.stringify(!v.ok && v.issues)).toBe(true);
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const usage = Object.entries(files)
        .filter(([k]) => !/blocks\/|contracts\.ts$|actions\.ts$/.test(k))
        .map(([, src]) => src)
        .join('\n');
      expect(usage).toContain('<TokenStats');
      expect(usage).toContain('tokenAddress={CONTRACTS["gold"].address}');
      expect(usage).toContain("import { CONTRACTS } from");
      expect(usage).not.toContain('contractId');
      const copied = Object.keys(files).find((k) => k.endsWith('blocks/TokenStats.tsx'));
      expect(copied).toBeDefined();
      expect(files[copied!]).toContain('export function TokenStats');
    });
  }
});