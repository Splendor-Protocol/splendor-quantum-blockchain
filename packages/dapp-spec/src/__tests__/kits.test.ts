import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import {
  DEMO_CONTRACT_ADDRESS,
  DEMO_TOKEN_ADDRESS,
  PAGE_TEMPLATES,
  bindBlock,
  bindBlockProps,
  blocksFromTree,
  createCatalog,
  emptySpec,
  generateProject,
  getTemplate,
  instantiateTemplate,
  kitTemplates,
  validateSpec,
  type CatalogEntry,
  type DappSpec,
} from '../index.js';

const catalog = createCatalog(WIDGET_MANIFESTS);
const versions = { sqcSdk: '3.0.0', connectReact: '3.0.0' };
const MY_TOKEN = `SQ:${'ab'.repeat(32)}`;
const MY_CONTRACT = `SQ:${'cd'.repeat(32)}`;
const entry = (id: string) => catalog.find((c) => c.id === id)!;
const allBlocks = (spec: DappSpec) => spec.pages.flatMap((p) => blocksFromTree(p.root));

describe('contract bindings', () => {
  const liveStat: CatalogEntry = {
    id: 'x-stat',
    title: 'x',
    description: '',
    category: 'modules',
    kind: 'block',
    importName: 'X',
    status: 'stable',
    placeable: true,
    defaults: {},
    props: [
      { name: 'contractId', label: 'Contract', type: 'string', format: 'contract', contractKind: 'any' },
      { name: 'contract', label: 'Address', type: 'string', format: 'address' },
      { name: 'functionSig', label: 'Function', type: 'string', format: 'function', functionKind: 'read' },
      { name: 'decimals', label: 'Decimals', type: 'number' },
    ],
  };
  const contracts = [
    { id: 'app', name: 'App', address: MY_CONTRACT, functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' as const }] },
    { id: 'tok', name: 'Tok', address: MY_TOKEN, kind: 'token' as const, decimals: 6, functions: [] },
  ];

  it('replaces the contract id with its address, adds ABI params and token decimals', () => {
    const r = bindBlockProps({ id: 'a', widget: 'x-stat', props: { contractId: 'app', functionSig: 'get()' } }, liveStat, contracts);
    expect(r.props).toEqual({ contract: MY_CONTRACT, functionSig: 'get()', fnParams: '[]' });
    expect(r.addressRefs).toEqual({ contract: 'app' });
    const t = bindBlockProps({ id: 'b', widget: 'x-stat', props: { contractId: 'tok' } }, liveStat, contracts);
    expect(t.props).toMatchObject({ contract: MY_TOKEN, decimals: 6 });
  });

  it('leaves raw props alone for unknown ids or unbound blocks', () => {
    expect(bindBlockProps({ id: 'a', widget: 'x-stat', props: { contract: MY_CONTRACT } }, liveStat, contracts).props).toEqual({ contract: MY_CONTRACT });
    expect(bindBlockProps({ id: 'a', widget: 'x-stat', props: { contractId: 'nope' } }, liveStat, contracts).addressRefs).toEqual({});
  });

  it('bindBlock (used by the preview) binds against spec.contracts, and a spec without contracts leaves props raw', () => {
    const block = { id: 'a', widget: 'x-stat', props: { contractId: 'app', functionSig: 'get()' } };
    expect(bindBlock({ contracts }, block, liveStat)).toEqual(bindBlockProps(block, liveStat, contracts));
    expect(bindBlock({}, block, liveStat)).toEqual({ props: { functionSig: 'get()' }, addressRefs: {} });
    expect(bindBlock({ contracts }, block, undefined)).toEqual({ props: block.props, addressRefs: {} });
  });
});

describe('page access', () => {
  it('accepts wallet / signin on sub-pages and keeps it through normalization', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!);
    const r = validateSpec(spec, { catalog });
    expect(r.ok ? [] : r.issues).toEqual([]);
    if (r.ok) expect(r.spec.pages.find((p) => p.path === '/app')!.access).toBe('wallet');
  });

  it('rejects unknown values and a protected home page', () => {
    const spec = emptySpec('x');
    (spec.pages[0] as { access?: string }).access = 'signin';
    spec.pages.push({ ...PAGE_TEMPLATES.find((t) => t.id === 'blank')!.build('/a', 'A'), access: 'vip' as never });
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    const paths = r.ok ? [] : r.issues.map((i) => i.path);
    expect(paths).toEqual(expect.arrayContaining(['pages[0].access', 'pages[1].access']));
  });

  it('codegen wraps protected pages in AccessGate and ships the gate', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!);
    for (const framework of ['vite', 'next'] as const) {
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const dash = Object.entries(files).find(([k]) => /Dashboard|App/.test(k) && k.endsWith('Page.tsx'))!;
      expect(dash[1]).toContain('<AccessGate access="wallet"');
      const home = framework === 'vite' ? files['src/pages/HomePage.tsx']! : files['app/HomePage.tsx']!;
      expect(home).not.toContain('AccessGate');
      expect(Object.keys(files).some((k) => k.endsWith('blocks/AccessGate.tsx'))).toBe(true);
    }
  });
});

describe('validation of bound props', () => {
  it('flags a contract id that is not in spec.contracts and a function that is not on the contract', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!);
    spec.pages[1]!.root = JSON.parse(JSON.stringify(spec.pages[1]!.root).replace('"contractId":"app","functionSig":"get()"', '"contractId":"ghost","functionSig":"get()"'));
    spec.pages[1]!.root = JSON.parse(JSON.stringify(spec.pages[1]!.root).replace('"functionSig":"set(uint256)"', '"functionSig":"burn(uint256)"'));
    const r = validateSpec(spec, { catalog });
    expect(r.ok).toBe(false);
    const messages = r.ok ? [] : r.issues.map((i) => i.message);
    expect(messages).toContain('pick a contract from the Contracts panel');
    expect(messages).toContain('not a function of the chosen contract');
  });
});

describe('kits', () => {
  it('offers the Token project and Contract app kits first', () => {
    expect(kitTemplates().map((t) => t.id)).toEqual(['token-project', 'contract-app']);
  });

  it.each(['token-project', 'contract-app', 'landing'])('%s validates against the catalog as shipped', (id) => {
    const r = validateSpec(instantiateTemplate(getTemplate(id)!), { catalog });
    expect(r.ok ? [] : r.issues).toEqual([]);
  });

  it('Token project: one answer binds the token everywhere (contract + raw widget props)', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!, { token: MY_TOKEN });
    expect(spec.contracts!.find((c) => c.id === 'token')!.address).toBe(MY_TOKEN);
    expect(allBlocks(spec).find((b) => b.id === 'send-token')!.props.tokenAddress).toBe(MY_TOKEN);
    expect(JSON.stringify(spec)).not.toContain(DEMO_TOKEN_ADDRESS);
    const bound = bindBlockProps(allBlocks(spec).find((b) => b.id === 'stats')!, entry('token-stats'), spec.contracts);
    expect(bound.props.tokenAddress).toBe(MY_TOKEN);
  });

  it('Contract app: builds one form per write function and a stat per no-arg read from the ABI', () => {
    const functions = [
      { name: 'totalStaked', signature: 'totalStaked()', params: [], kind: 'read' as const },
      { name: 'stakeOf', signature: 'stakeOf(address)', params: [{ name: 'who', type: 'address' }], kind: 'read' as const },
      { name: 'stake', signature: 'stake(uint256)', params: [{ name: 'amount', type: 'uint256' }], kind: 'write' as const },
      { name: 'unstake', signature: 'unstake(uint256)', params: [{ name: 'amount', type: 'uint256' }], kind: 'write' as const },
    ];
    const spec = instantiateTemplate(getTemplate('contract-app')!, { contract: { address: MY_CONTRACT, name: 'Staking', functions } });
    expect(spec.name).toBe('Staking');
    expect(spec.pages.find((p) => p.path === '/app')!.access).toBe('signin');
    const blocks = allBlocks(spec);
    expect(blocks.filter((b) => b.widget === 'contract-form').map((b) => b.props.functionSig)).toEqual(['stake(uint256)', 'unstake(uint256)']);
    expect(new Set(blocks.filter((b) => b.widget === 'live-stat').map((b) => b.props.functionSig))).toEqual(new Set(['totalStaked()']));
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
  });

  it('Token project: skipping the optional contract removes its demo contract and blocks', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!, { token: MY_TOKEN });
    expect(spec.contracts!.map((c) => c.id)).toEqual(['token']);
    expect(allBlocks(spec).some((b) => b.id === 'app-value' || b.id === 'app-set')).toBe(false);
    expect(JSON.stringify(spec)).not.toContain('"app-actions-right"');
    expect(validateSpec(spec, { catalog }).ok).toBe(true);
    // Keeping it (or opening the kit without the wizard) keeps the contract.
    expect(instantiateTemplate(getTemplate('token-project')!, { token: MY_TOKEN, contract: DEMO_CONTRACT_ADDRESS }).contracts!.map((c) => c.id)).toEqual(['token', 'app']);
    expect(instantiateTemplate(getTemplate('token-project')!).contracts!.map((c) => c.id)).toEqual(['token', 'app']);
  });

  it('an explicit nav.links list shows only those pages in the exported menu', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!);
    spec.nav = { ...spec.nav, links: ['/'] };
    const app = generateProject(spec, { framework: 'vite', catalog, versions }).files['src/App.tsx']!;
    expect(app).toMatch(/NAV_LINKS = \[\{"path":"\/","title":"Home"\}\]/);
    expect(app).toContain('"/app"');
  });

  it('Contract app without answers previews against the demo contract', () => {
    const spec = instantiateTemplate(getTemplate('contract-app')!);
    expect(spec.contracts![0]!.address).toBe(DEMO_CONTRACT_ADDRESS);
  });

  it('exports reference CONTRACTS instead of inlining bound addresses (Vite and Next build inputs)', () => {
    const spec = instantiateTemplate(getTemplate('token-project')!, { token: MY_TOKEN });
    for (const framework of ['vite', 'next'] as const) {
      const files = generateProject(spec, { framework, catalog, versions }).files;
      const home = framework === 'vite' ? files['src/pages/HomePage.tsx']! : files['app/HomePage.tsx']!;
      expect(home).toContain('CONTRACTS["token"].address');
      expect(home).toMatch(/import \{ CONTRACTS \} from '\.\.?\/contracts';/);
      expect(home).not.toContain('contractId=');
      const contracts = framework === 'vite' ? files['src/contracts.ts']! : files['app/contracts.ts']!;
      expect(contracts).toContain(MY_TOKEN);
    }
  });

  it('linked page templates: landing links to /app, dashboard is wallet-only', () => {
    const landing = PAGE_TEMPLATES.find((t) => t.id === 'landing-linked')!.build('/promo', 'Promo');
    expect(JSON.stringify(landing.root)).toContain('"/app"');
    expect(PAGE_TEMPLATES.find((t) => t.id === 'dashboard-wallet')!.build('/me', 'Me').access).toBe('wallet');
  });
});
