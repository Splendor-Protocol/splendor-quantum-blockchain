import { bindBlockProps } from './bind.js';
import { BLOCK_MANIFESTS, blockSource } from './blocks.js';
import { blocksFromTree, styleToCss } from './tree.js';
import type { BlockAction, CatalogEntry, ContractRef, DappBlock, DappPage, DappSpec, LayoutNode, PropValue, ThemeOverrides } from './types.js';

export type Framework = 'vite' | 'next';
/** `npm` pins registry versions; `vendor` uses file:vendor/@splendor/* (for StackBlitz / unpublished SDKs). */
export type SdkSource = 'npm' | 'vendor';

/** Published package versions to pin (`3.0.0` becomes `^3.0.0`; ranges are kept as given). */
export type PackageVersions = { sqcSdk: string; connectReact: string };

export type GenerateOptions = {
  framework: Framework;
  /** Placeable items (widgets from the connect-react manifest + `BLOCK_MANIFESTS`). */
  catalog: readonly CatalogEntry[];
  versions: PackageVersions;
  /** How to depend on the Splendor packages. Default `npm`. */
  sdk?: SdkSource;
  /**
   * Extra files to merge into the project (e.g. vendored package trees under `vendor/`).
   * Keys are relative paths; values are file contents (text). Binary wasm should be provided
   * as base64 via `vendorWasmBase64` instead.
   */
  vendorFiles?: Record<string, string>;
  /** Base64 of `sqc_signer.wasm` written as `vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64`. */
  vendorWasmBase64?: string;
};

/** Relative path → file contents. Pure data: the generator never writes to disk or runs code. */
export type GeneratedProject = { name: string; framework: Framework; files: Record<string, string> };

const TOOLING = {
  react: '^19.0.0',
  typesReact: '^19.0.0',
  vite: '^6.0.0',
  pluginReact: '^4.3.4',
  typescript: '^5.7.2',
  next: '^15.3.0',
  typesNode: '^22.0.0',
};

export function slugify(name: string): string {
  const s = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60);
  return s || 'splendor-dapp';
}

function range(v: string): string {
  return /^\d+\.\d+\.\d+/.test(v) ? `^${v}` : v;
}

function pageComponent(page: DappPage): string {
  if (page.path === '/') return 'HomePage';
  const base = page.path.slice(1).split('-').map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join('');
  return `${/^[A-Za-z]/.test(base) ? base : `P${base}`}Page`;
}

function jsxProp(name: string, value: PropValue): string {
  if (value === true) return name;
  if (typeof value === 'string') return `${name}=${JSON.stringify(value).replace(/[{}<>]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`).replace(/^"/, '{"').replace(/"$/, '"}')}`;
  return `${name}={${JSON.stringify(value)}}`;
}

/** Per-page codegen state: the spec's contracts, and whether the page references `CONTRACTS`. */
type RenderCtx = { contracts: readonly ContractRef[]; usesContracts: boolean };

function renderBlock(block: DappBlock, entry: CatalogEntry, indent: string, ctx?: RenderCtx): string {
  const bound = ctx ? bindBlockProps(block, entry, ctx.contracts) : { props: block.props, addressRefs: {} as Record<string, string> };
  const refs = Object.entries(bound.addressRefs);
  if (refs.length && ctx) ctx.usesContracts = true;
  const props = Object.entries(bound.props).filter(([k, v]) => v !== '' && v != null && !(k in bound.addressRefs));
  const attrs = [
    ...props.map(([k, v]) => jsxProp(k, v)),
    ...refs.map(([k, id]) => `${k}={CONTRACTS[${JSON.stringify(id).replace(/[{}<>]/g, '')}].address}`),
  ];
  const one = `<${entry.importName}${attrs.length ? ' ' + attrs.join(' ') : ''} />`;
  if (one.length + indent.length <= 100) return indent + one;
  return `${indent}<${entry.importName}\n${attrs.map((a) => `${indent}  ${a}`).join('\n')}\n${indent}/>`;
}

function themeConst(overrides: ThemeOverrides): string {
  const entries = Object.entries(overrides).filter(([, v]) => typeof v === 'string' && v);
  if (!entries.length) return '{}';
  return `{\n${entries.map(([k, v]) => `  ${k}: ${JSON.stringify(v)},`).join('\n')}\n}`;
}

type Resolved = { page: DappPage; component: string; usedWidgets: CatalogEntry[]; usedBlocks: CatalogEntry[] };

function pageBlocks(page: DappPage): DappBlock[] {
  if (page.root) return blocksFromTree(page.root);
  return page.blocks ?? [];
}

/**
 * One component name per page, unique even on case-insensitive file systems. `/` always gets
 * `HomePage`; a page whose name is taken (`/home`, `/a-b` vs `/ab`) gets a numeric suffix
 * (`HomePage2`), so imports and generated files never collide.
 */
function componentNames(pages: readonly DappPage[]): Map<DappPage, string> {
  const taken = new Set<string>();
  const out = new Map<DappPage, string>();
  const ordered = [...pages].sort((a, b) => Number(b.path === '/') - Number(a.path === '/'));
  for (const page of ordered) {
    const base = pageComponent(page);
    let name = base;
    for (let n = 2; taken.has(name.toLowerCase()); n += 1) name = `${base.replace(/Page$/, '')}Page${n}`;
    taken.add(name.toLowerCase());
    out.set(page, name);
  }
  return out;
}

function resolvePages(spec: DappSpec, catalog: readonly CatalogEntry[]): Resolved[] {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const names = componentNames(spec.pages);
  return spec.pages.map((page) => {
    const entries = pageBlocks(page).map((b) => {
      const e = byId.get(b.widget);
      if (!e) throw new Error(`dapp-spec codegen: unknown widget "${b.widget}" (validate the spec first)`);
      return e;
    });
    const uniq = (list: CatalogEntry[]) => [...new Map(list.map((e) => [e.id, e])).values()];
    return {
      page,
      component: names.get(page)!,
      usedWidgets: uniq(entries.filter((e) => e.kind !== 'block')),
      usedBlocks: uniq(entries.filter((e) => e.kind === 'block')),
    };
  });
}

function styleAttr(style: Record<string, string> | undefined, indent: string): string {
  if (!style || !Object.keys(style).length) return '';
  const inner = Object.entries(style)
    .map(([k, v]) => `${k}: ${JSON.stringify(v)}`)
    .join(', ');
  return `\n${indent}  style={{ ${inner} }}`;
}

function renderTree(node: LayoutNode, byId: Map<string, CatalogEntry>, indent: string, ctx?: RenderCtx): string {
  if (node.kind === 'block') {
    const entry = byId.get(node.block.widget);
    if (!entry) throw new Error(`dapp-spec codegen: unknown widget "${node.block.widget}"`);
    return renderBlock(node.block, entry, indent, ctx);
  }
  const cls = `dapp-${node.kind}`;
  const hide = node.style?.hideOn ? ` dapp-hide-${node.style.hideOn}` : '';
  const style = styleToCss(node.style, node.kind);
  const open = `${indent}<div className="${cls}${hide}" data-node-id=${JSON.stringify(node.id)}${styleAttr(style, indent)}>`;
  if (!node.children.length) return `${open}\n${indent}</div>`;
  const kids = node.children.map((c) => renderTree(c, byId, indent + '  ', ctx)).join('\n');
  return `${open}\n${kids}\n${indent}</div>`;
}

const isGated = (page: DappPage) => page.access === 'wallet' || page.access === 'signin';

function pageSource(
  r: Resolved,
  opts: { blocksDir: string; client: boolean; contractsPath: string },
  catalog: readonly CatalogEntry[],
  contracts: readonly ContractRef[],
): string {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const ctx: RenderCtx = { contracts, usesContracts: false };
  const gated = isGated(r.page);
  const root = r.page.root;
  const indent = gated ? '        ' : '      ';
  const body = root
    ? renderTree(root, byId, indent, ctx)
    : pageBlocks(r.page).length
      ? pageBlocks(r.page).map((b) => renderBlock(b, byId.get(b.widget)!, indent, ctx)).join('\n')
      : `${indent}<p className="dapp-empty">This page has no blocks yet.</p>`;

  const lines: string[] = [];
  if (opts.client) lines.push(`'use client';`, '');
  const pkgs = new Map<string, string[]>();
  for (const w of r.usedWidgets) {
    const pkg = w.package ?? '@splendor/connect-react';
    pkgs.set(pkg, [...(pkgs.get(pkg) ?? []), w.importName]);
  }
  for (const [pkg, names] of pkgs) lines.push(`import { ${[...new Set(names)].sort().join(', ')} } from '${pkg}';`);
  for (const b of r.usedBlocks) lines.push(`import { ${b.importName} } from '${opts.blocksDir}/${b.importName}';`);
  if (gated) lines.push(`import { AccessGate } from '${opts.blocksDir}/AccessGate';`);
  if (ctx.usesContracts) lines.push(`import { CONTRACTS } from '${opts.contractsPath}';`);
  if (lines.length) lines.push('');
  lines.push(
    `export function ${r.component}() {`,
    '  return (',
    '    <div className="dapp-page">',
    ...(gated ? [`      <AccessGate access=${JSON.stringify(r.page.access)} title=${jsxProp('title', r.page.title).slice('title='.length)}>`] : []),
    body,
    ...(gated ? ['      </AccessGate>'] : []),
    '    </div>',
    '  );',
    '}',
    '',
  );
  return lines.join('\n');
}

function depVersions(v: PackageVersions, sdk: SdkSource): { connectReact: string; sqcSdk: string } {
  if (sdk === 'vendor') {
    return {
      connectReact: 'file:vendor/@splendor/connect-react',
      sqcSdk: 'file:vendor/@splendor/sqc-sdk',
    };
  }
  return { connectReact: range(v.connectReact), sqcSdk: range(v.sqcSdk) };
}

/** Layout CSS of generated apps. The playground preview mirrors its `.dapp-*` rules. */
export const APP_CSS = `:root {
  color-scheme: dark;
}
:root:has([data-sqc-theme='light']) {
  color-scheme: light;
}
body {
  margin: 0;
}
/* Theme variables live on the provider's .sqc-root, so the page background is painted here, not on body. */
.dapp-root {
  min-height: 100vh;
  background: var(--sqc-bg, #0b0d12);
  color: var(--sqc-text, #e8ecf3);
  font-family: var(--sqc-font-body, system-ui, sans-serif);
}
.dapp-shell {
  max-width: 1120px;
  margin: 0 auto;
  padding: 1.25rem;
}
.dapp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1rem;
}
.dapp-brand {
  font-weight: 700;
  font-size: 1.15rem;
  margin: 0;
}
.dapp-nav {
  display: flex;
  gap: 0.75rem;
  margin-bottom: 1rem;
  flex-wrap: wrap;
}
.dapp-nav a {
  color: var(--sqc-text-muted, #9aa4b2);
  text-decoration: none;
}
.dapp-nav a[aria-current='page'] {
  color: var(--sqc-accent, #7c5cff);
  font-weight: 650;
  text-decoration: underline;
  text-underline-offset: 0.3em;
}
.dapp-gate {
  display: grid;
  justify-items: center;
  gap: 0.75rem;
  max-width: 28rem;
  margin: 3rem auto;
  padding: 2rem 1.5rem;
  text-align: center;
}
.dapp-gate__title {
  margin: 0;
  font-size: 1.35rem;
}
.dapp-footer {
  margin-top: 2rem;
  padding-top: 1rem;
  border-top: 1px solid var(--sqc-border, #262b38);
  color: var(--sqc-text-muted, #9aa4b2);
  font-size: 0.875rem;
  text-align: center;
}
.dapp-page {
  display: flex;
  flex-direction: column;
  gap: 0;
}
.dapp-section {
  width: 100%;
  max-width: var(--dapp-section-max, 1120px);
  margin: 0 auto;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
  box-sizing: border-box;
}
.dapp-stack {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.dapp-row {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: stretch;
}
.dapp-column {
  flex: 1 1 240px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.dapp-card {
  border: 1px solid var(--sqc-border, #262b38);
  background: var(--sqc-surface, #12151d);
  border-radius: var(--sqc-radius, 12px);
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.dapp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 1rem;
  align-items: start;
}
@media (max-width: 720px) {
  .dapp-hide-mobile { display: none !important; }
}
@media (min-width: 721px) {
  .dapp-hide-desktop { display: none !important; }
}
.dapp-stats {
  display: grid;
  gap: 0.5rem;
  margin: 0 0 0.75rem;
}
.dapp-stats div {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
}
.dapp-stats dt {
  color: var(--sqc-text-muted, #9aa4b2);
}
.dapp-stats dd {
  margin: 0;
}
.dapp-mono {
  font-family: var(--sqc-font-mono, ui-monospace, monospace);
}
.dapp-note,
.dapp-empty {
  color: var(--sqc-text-muted, #9aa4b2);
}
`;

function readme(spec: DappSpec, framework: Framework): string {
  const dev = framework === 'vite' ? 'http://localhost:5173' : 'http://localhost:3000';
  const pins =
    framework === 'vite'
      ? '`sqcWasmPlugin` serves the signer and defines `VITE_SQC_WASM_SHA256` / `VITE_SQC_EXEC_SHA256` from the files it serves, so production builds are pinned automatically.'
      : '`npm run dev` / `npm run build` first run `scripts/copy-sqc-wasm.mjs`, which copies the signer into `public/` and writes its digests to `.env.production.local` (`NEXT_PUBLIC_SQC_WASM_SHA256` / `NEXT_PUBLIC_SQC_EXEC_SHA256`), so production builds are pinned.';
  return `# ${spec.name}

${spec.description ?? 'A dApp on Splendor Quantum Chain, generated by the Splendor dApp Playground.'}

Built with [\`@splendor/connect-react\`](https://www.npmjs.com/package/@splendor/connect-react) and
[\`@splendor/sqc-sdk\`](https://www.npmjs.com/package/@splendor/sqc-sdk) (${framework === 'vite' ? 'Vite' : 'Next.js'} + React).

## Run it

\`\`\`bash
npm install
npm run dev
\`\`\`

Open ${dev}. Connect with the Splendor Quantum Wallet extension or an in-page seed wallet.

## Network

The app runs on **${spec.network}**. Change \`NETWORK\` in ${framework === 'vite' ? '`src/App.tsx`' : '`app/providers.tsx`'}
to \`'mainnet'\`, \`'testnet'\`, \`'devnet'\` or \`'local'\`. Mainnet spends real SPLD.

On localhost the public RPCs are reached through a dev proxy at \`/rpc/<network>\` because they do
not accept cross-origin calls from localhost. Set \`${framework === 'vite' ? 'VITE' : 'NEXT_PUBLIC'}_SQC_DIRECT_RPC=1\` to call the RPC
directly (for example when deployed on an origin the RPC allows).

## The signer (WASM)

Keys are handled by the ML-DSA-65 signer bundle shipped in \`@splendor/sqc-sdk\`.
${pins} Never deploy with \`allowUnpinnedWasm\`.

## Deploy

\`\`\`bash
npm run build
\`\`\`

${framework === 'vite' ? 'Upload `dist/` to any static host (Netlify, Vercel, Cloudflare Pages, S3).' : 'Deploy to Vercel (`npx vercel`) or any Node host that runs `next start`.'}

## Security headers

${
  framework === 'vite'
    ? `A static host does not add these for you. Set them in its config (Netlify \`public/_headers\`,
Vercel \`vercel.json\`, nginx \`add_header\`). The build has no inline scripts, so a strict CSP works:

\`\`\`
Content-Security-Policy: default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' https://*.splendor.org; frame-ancestors 'none'; object-src 'none'; base-uri 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
Strict-Transport-Security: max-age=31536000; includeSubDomains
\`\`\`

\`'wasm-unsafe-eval'\` is required for the signer. Add your RPC host to \`connect-src\` if it is not under \`splendor.org\`.`
    : `\`next.config.mjs\` sends framing, MIME-sniffing, referrer and permissions headers on every route.
A script CSP with Next.js needs per-request nonces (see the Next.js "Content Security Policy" guide);
when you add one, include \`'wasm-unsafe-eval'\` in \`script-src\` for the signer. Serve over HTTPS with HSTS.`
}

## Learn more

- Widgets and hooks: \`node_modules/@splendor/connect-react/docs/GUIDE.md\`
- SDK: \`node_modules/@splendor/sqc-sdk/README.md\`
`;
}

/** Escape text for use between JSX tags (entities keep every character, nothing can open a tag or expression). */
function jsxText(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\{/g, '&#123;')
    .replace(/\}/g, '&#125;')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * What the app shell shows, from `spec.nav`: brand (falls back to the app name), the connect
 * button (default on), nav links (listed pages first, in order; the rest follow in page order)
 * and an optional footer line.
 */
function navModel(spec: DappSpec, pages: Resolved[]) {
  const nav = spec.nav ?? {};
  const byPath = new Map(pages.map((r) => [r.page.path, r.page]));
  const order: DappPage[] = [];
  const seen = new Set<string>();
  for (const p of nav.links ?? []) {
    const page = byPath.get(p);
    if (page && !seen.has(p)) {
      seen.add(p);
      order.push(page);
    }
  }
  // No explicit list: every page, in page order. An explicit list shows only those pages.
  if (!nav.links) for (const r of pages) order.push(r.page);
  return {
    brand: jsxText(nav.brand?.trim() || spec.name) || jsxText(spec.name),
    showConnect: nav.showConnect !== false,
    links: order.map((p) => ({ path: p.path, title: p.title })),
    footer: jsxText(nav.footer ?? ''),
  };
}

function viteFiles(
  spec: DappSpec,
  pages: Resolved[],
  catalog: readonly CatalogEntry[],
  v: PackageVersions,
  sdk: SdkSource,
): Record<string, string> {
  const name = slugify(spec.name);
  const deps = depVersions(v, sdk);
  const files: Record<string, string> = {};
  files['package.json'] =
    JSON.stringify(
      {
        name,
        private: true,
        version: '0.1.0',
        type: 'module',
        scripts: {
          ...(sdk === 'vendor' ? { postinstall: 'node scripts/decode-wasm.mjs' } : {}),
          dev: 'vite',
          build: 'tsc --noEmit && vite build',
          preview: 'vite preview',
        },
        dependencies: {
          '@splendor/connect-react': deps.connectReact,
          '@splendor/sqc-sdk': deps.sqcSdk,
          react: TOOLING.react,
          'react-dom': TOOLING.react,
        },
        devDependencies: {
          '@types/react': TOOLING.typesReact,
          '@types/react-dom': TOOLING.typesReact,
          '@vitejs/plugin-react': TOOLING.pluginReact,
          typescript: TOOLING.typescript,
          vite: TOOLING.vite,
        },
      },
      null,
      2,
    ) + '\n';
  files['vite.config.ts'] = `import { defineConfig, type ProxyOptions } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
import { listNetworks } from '@splendor/sqc-sdk/networks';

// Public Splendor RPCs do not accept cross-origin calls from localhost, so dev and preview
// proxy each https network under /rpc/<id> (see src/networks.ts).
const rpcProxy: Record<string, ProxyOptions> = {};
for (const net of listNetworks()) {
  const target = net.rpcUrls.find((u) => u.startsWith('https://'));
  if (!target) continue;
  rpcProxy[\`/rpc/\${net.id}\`] = {
    target,
    changeOrigin: true,
    secure: true,
    rewrite: (p) => p.replace(new RegExp(\`^/rpc/\${net.id}\`), ''),
  };
}

export default defineConfig({
  plugins: [react(), sqcWasmPlugin()],
  server: { proxy: rpcProxy },
  preview: { proxy: rpcProxy },
  resolve: { dedupe: ['react', 'react-dom'] },
});
`;
  files['tsconfig.json'] =
    JSON.stringify(
      {
        compilerOptions: {
          target: 'ES2022',
          lib: ['ES2022', 'DOM', 'DOM.Iterable'],
          module: 'ESNext',
          moduleResolution: 'Bundler',
          jsx: 'react-jsx',
          strict: true,
          skipLibCheck: true,
          noEmit: true,
          types: ['vite/client'],
        },
        include: ['src', 'vite.config.ts'],
      },
      null,
      2,
    ) + '\n';
  files['index.html'] = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>${spec.name.replace(/[<>&"]/g, '')}</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
`;
  files['src/main.tsx'] = `import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@splendor/connect-react/style.css';
import { App } from './App';
import './app.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
`;
  files['src/networks.ts'] = networksSource();
  files['src/app.css'] = APP_CSS;
  const routes = pages.map((r) => ({ path: r.page.path, title: r.page.title, component: r.component }));
  const shell = navModel(spec, pages);
  files['src/App.tsx'] = `import { useEffect, useState } from 'react';
import {
${shell.showConnect ? '  ConnectKit,\n' : ''}  SplendorConnectProvider,
  TxToastHost,
  WrongNetworkBanner,
  themeOverridesToStyle,
  type ThemeOverrides,
} from '@splendor/connect-react';
import { appNetworks } from './networks';
${routes.map((r) => `import { ${r.component} } from './pages/${r.component}';`).join('\n')}

const NETWORK = ${JSON.stringify(spec.network)} as const;
const THEME = ${JSON.stringify(spec.theme.base)} as const;
const THEME_OVERRIDES: ThemeOverrides = ${themeConst(spec.theme.overrides)};
const CUSTOM_NETWORKS = appNetworks(import.meta.env.VITE_SQC_DIRECT_RPC === '1');

const ROUTES = [
${routes.map((r) => `  { path: ${JSON.stringify(r.path)}, title: ${JSON.stringify(r.title)}, Page: ${r.component} },`).join('\n')}
];

/** Nav links in display order (from the spec's nav). */
const NAV_LINKS = ${JSON.stringify(shell.links)};

function useHashPath(): string {
  const read = () => window.location.hash.replace(/^#/, '') || '/';
  const [path, setPath] = useState(read);
  useEffect(() => {
    const on = () => setPath(read());
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return path;
}

export function App() {
  // sqcWasmPlugin defines both digests from the files it serves; .env values take precedence.
  const wasmSha256 = (import.meta.env.VITE_SQC_WASM_SHA256 as string | undefined)?.trim();
  const execSha256 = (import.meta.env.VITE_SQC_EXEC_SHA256 as string | undefined)?.trim();
  if (!import.meta.env.DEV && (!wasmSha256 || !execSha256)) {
    throw new Error('VITE_SQC_WASM_SHA256 and VITE_SQC_EXEC_SHA256 are required for production builds.');
  }
  const path = useHashPath();
  const route = ROUTES.find((r) => r.path === path) ?? ROUTES[0]!;
  return (
    <SplendorConnectProvider
      network={NETWORK}
      connectors="auto"
      theme={THEME}
      autoReconnect
      customNetworks={CUSTOM_NETWORKS}
      {...(wasmSha256 && execSha256 ? { wasmSha256, execSha256 } : { allowUnpinnedWasm: true })}
    >
      <TxToastHost>
        <div className="dapp-root" style={themeOverridesToStyle(THEME_OVERRIDES)}>
          <div className="dapp-shell">
            <header className="dapp-header">
              <p className="dapp-brand">${shell.brand}</p>${shell.showConnect ? '\n              <ConnectKit />' : ''}
            </header>
            <WrongNetworkBanner expectedNetworkId={NETWORK} />
            {NAV_LINKS.length > 1 ? (
              <nav className="dapp-nav" aria-label="Pages">
                {NAV_LINKS.map((r) => (
                  <a key={r.path} href={\`#\${r.path}\`} aria-current={r.path === route.path ? 'page' : undefined}>
                    {r.title}
                  </a>
                ))}
              </nav>
            ) : null}
            <main>
              <route.Page />
            </main>${shell.footer ? `\n            <footer className="dapp-footer">${shell.footer}</footer>` : ''}
          </div>
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}
`;
  for (const r of pages) {
    files[`src/pages/${r.component}.tsx`] = pageSource(r, { blocksDir: '../blocks', client: false, contractsPath: '../contracts' }, catalog, spec.contracts ?? []);
  }
  addBlocks(files, pages, 'src/blocks', 'vite');
  files['.env.example'] = `# Call public RPCs directly instead of through the dev proxy (/rpc/<network>).
# VITE_SQC_DIRECT_RPC=1
`;
  return files;
}

function networksSource(): string {
  return `import { listNetworks, type NetworkConfig } from '@splendor/sqc-sdk/networks';

/**
 * On localhost every https network is pointed at the dev-server proxy (/rpc/<id>), because the
 * public RPCs do not accept cross-origin calls from localhost. Elsewhere, or with \`direct\`, the
 * SDK's built-in RPC URLs are used.
 */
export function appNetworks(direct: boolean): NetworkConfig[] | undefined {
  if (typeof window === 'undefined' || direct) return undefined;
  const host = window.location.hostname;
  if (host !== 'localhost' && host !== '127.0.0.1') return undefined;
  return listNetworks()
    .filter((n) => n.rpcUrls.some((u) => u.startsWith('https://')))
    .map((n) => ({ ...n, rpcUrls: [\`\${window.location.origin}/rpc/\${n.id}\`] }));
}
`;
}

/** Helper modules shared by blocks (`blocks/*.ts`). Copied next to the blocks whenever any block is used. */
const SHARED_BLOCK_FILES = ['pageHref.ts', 'parseList.ts'] as const;

/**
 * `pageHref` for Next.js exports: pages are real routes there (`/about`), so links are plain
 * paths instead of the `#/about` hash links the Vite export and the preview use.
 */
const NEXT_PAGE_HREF = `/**
 * Link target of a page path or external URL. Pages are routes in this app, so the path is used as is.
 */
export function pageHref(path: string): string {
  const v = path.trim();
  if (!v) return '';
  if (/^https?:\\/\\//i.test(v)) return v;
  const p = v.replace(/^#/, '');
  return p.startsWith('/') ? p : \`/\${p}\`;
}
`;

function addBlocks(files: Record<string, string>, pages: Resolved[], dir: string, framework: Framework): void {
  const blocks = new Map(pages.flatMap((r) => r.usedBlocks).map((b) => [b.id, b]));
  for (const b of blocks.values()) {
    const manifest = BLOCK_MANIFESTS.find((m) => m.id === b.id);
    if (!manifest) throw new Error(`dapp-spec codegen: block "${b.id}" has no source`);
    files[`${dir}/${manifest.importName}.tsx`] = blockSource(manifest.file);
  }
  if (pages.some((r) => isGated(r.page))) files[`${dir}/AccessGate.tsx`] = blockSource('AccessGate.tsx');
  if (!blocks.size) return;
  for (const file of SHARED_BLOCK_FILES) {
    files[`${dir}/${file}`] = framework === 'next' && file === 'pageHref.ts' ? NEXT_PAGE_HREF : blockSource(file);
  }
}

function nextFiles(
  spec: DappSpec,
  pages: Resolved[],
  catalog: readonly CatalogEntry[],
  v: PackageVersions,
  sdk: SdkSource,
): Record<string, string> {
  const name = slugify(spec.name);
  const deps = depVersions(v, sdk);
  const files: Record<string, string> = {};
  files['package.json'] =
    JSON.stringify(
      {
        name,
        private: true,
        version: '0.1.0',
        scripts: {
          ...(sdk === 'vendor' ? { postinstall: 'node scripts/decode-wasm.mjs' } : {}),
          predev: 'node scripts/copy-sqc-wasm.mjs',
          dev: 'next dev',
          prebuild: 'node scripts/copy-sqc-wasm.mjs',
          build: 'next build',
          start: 'next start',
        },
        dependencies: {
          '@splendor/connect-react': deps.connectReact,
          '@splendor/sqc-sdk': deps.sqcSdk,
          next: TOOLING.next,
          react: TOOLING.react,
          'react-dom': TOOLING.react,
        },
        devDependencies: {
          '@types/node': TOOLING.typesNode,
          '@types/react': TOOLING.typesReact,
          '@types/react-dom': TOOLING.typesReact,
          typescript: TOOLING.typescript,
        },
      },
      null,
      2,
    ) + '\n';
  files['next.config.mjs'] = `import { listNetworks } from '@splendor/sqc-sdk/networks';

/** Public Splendor RPCs do not accept cross-origin calls from localhost: proxy them under /rpc/<id>. */
const rpcRewrites = listNetworks().flatMap((net) => {
  const target = net.rpcUrls.find((u) => u.startsWith('https://'));
  return target ? [{ source: \`/rpc/\${net.id}/:path*\`, destination: \`\${target}/:path*\` }] : [];
});

/**
 * Baseline security headers. A script CSP needs per-request nonces with Next.js, so only framing
 * is restricted here; see the README before adding one.
 */
const securityHeaders = [
  { key: 'Content-Security-Policy', value: "frame-ancestors 'none'; object-src 'none'; base-uri 'self'" },
  { key: 'X-Frame-Options', value: 'DENY' },
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
  { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=(), payment=()' },
];

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    return rpcRewrites;
  },
  async headers() {
    return [{ source: '/:path*', headers: securityHeaders }];
  },
};

export default nextConfig;
`;
  files['tsconfig.json'] =
    JSON.stringify(
      {
        compilerOptions: {
          target: 'ES2022',
          lib: ['dom', 'dom.iterable', 'esnext'],
          allowJs: false,
          skipLibCheck: true,
          strict: true,
          noEmit: true,
          esModuleInterop: true,
          module: 'esnext',
          moduleResolution: 'bundler',
          resolveJsonModule: true,
          isolatedModules: true,
          jsx: 'preserve',
          incremental: true,
          plugins: [{ name: 'next' }],
        },
        include: ['next-env.d.ts', '**/*.ts', '**/*.tsx', '.next/types/**/*.ts'],
        exclude: ['node_modules'],
      },
      null,
      2,
    ) + '\n';
  files['next-env.d.ts'] = `/// <reference types="next" />
/// <reference types="next/image-types/global" />
`;
  files['scripts/copy-sqc-wasm.mjs'] = `import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { copySqcWasmToPublic } from '@splendor/connect-react/next';

// Copy the signer into public/ and pin what was copied for production builds.
const dir = copySqcWasmToPublic('public');
const sha = (f) => createHash('sha256').update(readFileSync(join(dir, f))).digest('hex');
writeFileSync(
  '.env.production.local',
  \`NEXT_PUBLIC_SQC_WASM_SHA256=\${sha('sqc_signer.wasm')}\\nNEXT_PUBLIC_SQC_EXEC_SHA256=\${sha('wasm_exec.js')}\\n\`,
);
console.log('[sqc-wasm] copied signer to public/ and pinned it in .env.production.local');
`;
  files['app/networks.ts'] = networksSource();
  files['app/app.css'] = APP_CSS;
  const shell = navModel(spec, pages);
  files['app/layout.tsx'] = `import type { ReactNode } from 'react';
import '@splendor/connect-react/style.css';
import './app.css';
import { ClientRoot } from './client-root';

export const metadata = { title: ${JSON.stringify(spec.name)} };

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <ClientRoot>{children}</ClientRoot>
      </body>
    </html>
  );
}
`;
  files['app/client-root.tsx'] = `'use client';

import dynamic from 'next/dynamic';
import type { ReactNode } from 'react';

// The wallet provider loads the WASM signer and reads browser storage: render it on the client only.
const Providers = dynamic(() => import('./providers').then((m) => m.Providers), { ssr: false });

export function ClientRoot({ children }: { children: ReactNode }) {
  return <Providers>{children}</Providers>;
}
`;
  files['app/providers.tsx'] = `'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { ReactNode } from 'react';
import {
${shell.showConnect ? '  ConnectKit,\n' : ''}  SplendorConnectProvider,
  TxToastHost,
  WrongNetworkBanner,
  themeOverridesToStyle,
  type ThemeOverrides,
} from '@splendor/connect-react';
import { appNetworks } from './networks';

const NETWORK = ${JSON.stringify(spec.network)} as const;
const THEME = ${JSON.stringify(spec.theme.base)} as const;
const THEME_OVERRIDES: ThemeOverrides = ${themeConst(spec.theme.overrides)};
/** Nav links in display order (from the spec's nav). */
const ROUTES = ${JSON.stringify(shell.links)};

export function Providers({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const wasmSha256 = process.env.NEXT_PUBLIC_SQC_WASM_SHA256?.trim();
  const execSha256 = process.env.NEXT_PUBLIC_SQC_EXEC_SHA256?.trim();
  if (process.env.NODE_ENV === 'production' && (!wasmSha256 || !execSha256)) {
    throw new Error('NEXT_PUBLIC_SQC_WASM_SHA256 and NEXT_PUBLIC_SQC_EXEC_SHA256 are required for production builds.');
  }
  return (
    <SplendorConnectProvider
      network={NETWORK}
      connectors="auto"
      theme={THEME}
      autoReconnect
      customNetworks={appNetworks(process.env.NEXT_PUBLIC_SQC_DIRECT_RPC === '1')}
      {...(wasmSha256 && execSha256 ? { wasmSha256, execSha256 } : { allowUnpinnedWasm: true })}
    >
      <TxToastHost>
        <div className="dapp-root" style={themeOverridesToStyle(THEME_OVERRIDES)}>
          <div className="dapp-shell">
            <header className="dapp-header">
              <p className="dapp-brand">${shell.brand}</p>${shell.showConnect ? '\n              <ConnectKit />' : ''}
            </header>
            <WrongNetworkBanner expectedNetworkId={NETWORK} />
            {ROUTES.length > 1 ? (
              <nav className="dapp-nav" aria-label="Pages">
                {ROUTES.map((r) => (
                  <Link key={r.path} href={r.path} aria-current={r.path === pathname ? 'page' : undefined}>
                    {r.title}
                  </Link>
                ))}
              </nav>
            ) : null}
            <main>{children}</main>${shell.footer ? `\n            <footer className="dapp-footer">${shell.footer}</footer>` : ''}
          </div>
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}
`;
  for (const r of pages) {
    const dir = r.page.path === '/' ? 'app' : `app${r.page.path}`;
    const depth = r.page.path === '/' ? '..' : '../..';
    files[`${dir}/${r.component}.tsx`] = pageSource(
      r,
      { blocksDir: `${depth}/blocks`, client: true, contractsPath: r.page.path === '/' ? './contracts' : '../contracts' },
      catalog,
      spec.contracts ?? [],
    );
    files[`${dir}/page.tsx`] = `import { ${r.component} } from './${r.component}';

export const metadata = { title: ${JSON.stringify(r.page.title)} };

export default function Page() {
  return <${r.component} />;
}
`;
  }
  addBlocks(files, pages, 'blocks', 'next');
  // Block components are client components; the shared .ts helpers stay plain modules.
  for (const [path, src] of Object.entries(files)) {
    if (path.startsWith('blocks/') && path.endsWith('.tsx')) files[path] = `'use client';\n\n${src}`;
  }
  files['.env.example'] = `# Call public RPCs directly instead of through the /rpc/<network> rewrite.
# NEXT_PUBLIC_SQC_DIRECT_RPC=1
`;
  return files;
}

function contractsSource(spec: DappSpec): string {
  const list = spec.contracts ?? [];
  const entries = list
    .map((c) => {
      const fns = c.functions
        .map(
          (f) =>
            `    { name: ${JSON.stringify(f.name)}, signature: ${JSON.stringify(f.signature)}, kind: ${JSON.stringify(f.kind)}, params: ${JSON.stringify(f.params)}${f.returns ? `, returns: ${JSON.stringify(f.returns)}` : ''} }`,
        )
        .join(',\n');
      return `  ${JSON.stringify(c.id)}: {\n    id: ${JSON.stringify(c.id)},\n    name: ${JSON.stringify(c.name)},\n    address: ${JSON.stringify(c.address)},\n    functions: [\n${fns}\n    ],\n  }`;
    })
    .join(',\n');
  const firstRead = list.flatMap((c) => c.functions.filter((f) => f.kind === 'read').map((f) => ({ c, f })))[0];
  const firstWrite = list.flatMap((c) => c.functions.filter((f) => f.kind === 'write').map((f) => ({ c, f })))[0];
  const readId = JSON.stringify(firstRead?.c.id ?? 'token');
  const readSig = JSON.stringify(firstRead?.f.signature ?? 'balanceOf(address)');
  const writeId = JSON.stringify(firstWrite?.c.id ?? 'token');
  const writeSig = JSON.stringify(firstWrite?.f.signature ?? 'transfer(address,uint256)');
  return `/**
 * Contracts bound in the Splendor dApp Studio.
 *
 * \`CONTRACTS\` is plain metadata (no wallet, no network). The helpers below turn it into the
 * arguments that \`useReadContract\` / \`useContract\` from \`@splendor/connect-react\` expect.
 */
export type ContractParam = { readonly name: string; readonly type: string };
export type ContractFunctionMeta = {
  readonly name: string;
  readonly signature: string;
  readonly kind: 'read' | 'write';
  readonly params: readonly ContractParam[];
  readonly returns?: string;
};
export type ContractMeta = {
  readonly id: string;
  readonly name: string;
  readonly address: string;
  readonly functions: readonly ContractFunctionMeta[];
};

export const CONTRACTS = ${entries ? `{\n${entries}\n}` : '{}'} as const satisfies Record<string, ContractMeta>;

export type ContractId = keyof typeof CONTRACTS;

/** Look up a bound contract. Throws on an unknown id so typos fail loudly. */
export function getContract(id: ContractId): ContractMeta {
  const c = (CONTRACTS as Record<string, ContractMeta>)[id];
  if (!c) throw new Error(\`Unknown contract "\${String(id)}"\`);
  return c;
}

/** Find a function by name or full signature, e.g. \`balanceOf\` or \`balanceOf(address)\`. */
export function getContractFunction(id: ContractId, nameOrSignature: string): ContractFunctionMeta {
  const fn = getContract(id).functions.find((f) => f.name === nameOrSignature || f.signature === nameOrSignature);
  if (!fn) throw new Error(\`Contract "\${String(id)}" has no function "\${nameOrSignature}"\`);
  return fn;
}

export type ContractCallTarget = {
  address: string;
  signature: string;
  kind: 'read' | 'write';
  args: unknown[];
};

/** Everything a hook needs for one call: \`address\`, \`signature\` and \`args\`. */
export function contractCall(id: ContractId, nameOrSignature: string, args: unknown[] = []): ContractCallTarget {
  const fn = getContractFunction(id, nameOrSignature);
  return { address: getContract(id).address, signature: fn.signature, kind: fn.kind, args };
}

/** Shorthand for read-only functions. Throws if the function writes state. */
export function readCall(id: ContractId, nameOrSignature: string, args: unknown[] = []): ContractCallTarget {
  const t = contractCall(id, nameOrSignature, args);
  if (t.kind !== 'read') throw new Error(\`\${t.signature} is a write function: use writeCall\`);
  return t;
}

/** Shorthand for state-changing functions. Throws if the function is read-only. */
export function writeCall(id: ContractId, nameOrSignature: string, args: unknown[] = []): ContractCallTarget {
  const t = contractCall(id, nameOrSignature, args);
  if (t.kind !== 'write') throw new Error(\`\${t.signature} is a read function: use readCall\`);
  return t;
}

/*
 * Patterns (copy into a component; hooks only work inside <SplendorConnectProvider>):
 *
 * // 1. Read without a wallet, re-run every 10s
 * import { useReadContract } from '@splendor/connect-react';
 * import { readCall } from './contracts';
 *
 * function Value() {
 *   const t = readCall(${readId}, ${readSig});
 *   const { data, loading, error } = useReadContract(t.address, t.signature, t.args, { pollMs: 10_000 });
 *   if (loading) return <p>Loading…</p>;
 *   if (error) return <p role="alert">{String(error)}</p>;
 *   return <p>{data?.return_data}</p>;
 * }
 *
 * // 2. Write (signs a transaction with the connected wallet)
 * import { useContract } from '@splendor/connect-react';
 * import { writeCall } from './contracts';
 *
 * function Send() {
 *   const t = writeCall(${writeId}, ${writeSig}, []);
 *   const contract = useContract(t.address);
 *   return (
 *     <button disabled={contract.status === 'pending'} onClick={() => void contract.call(t.signature, t.args)}>
 *       Send
 *     </button>
 *   );
 * }
 */
`;
}

type ActionEntry = {
  id: string;
  page: string;
  widget: string;
  label?: string;
  action: BlockAction;
};

/** Props that may carry a navigation / contract action. `format: 'action'` props are added from the catalog. */
const ACTION_PROP_KEYS = ['action', 'href', 'ctaPath', 'buttonPath', 'link'] as const;
const LABEL_PROP_KEYS = ['label', 'ctaLabel', 'buttonLabel', 'heading', 'title'] as const;

/** Parse one prop value into a `BlockAction`: JSON, `contract:<id>.<fn>`, `https://…` or a page path. */
function parseActionValue(value: PropValue | undefined): BlockAction | null {
  if (typeof value !== 'string') return null;
  const v = value.trim();
  if (!v) return null;
  if (v.startsWith('{')) {
    try {
      const a = JSON.parse(v) as Record<string, unknown>;
      if (a.type === 'page' && typeof a.path === 'string') return { type: 'page', path: normalizePagePath(a.path) };
      if (a.type === 'url' && typeof a.href === 'string') return { type: 'url', href: a.href };
      if (a.type === 'contract' && typeof a.contractId === 'string' && typeof a.fn === 'string') {
        return { type: 'contract', contractId: a.contractId, fn: a.fn, ...(typeof a.argsFrom === 'string' ? { argsFrom: a.argsFrom } : {}) };
      }
    } catch {
      /* fall through: not JSON */
    }
    return null;
  }
  const c = /^contract:([^.\s]+)\.(.+)$/.exec(v);
  if (c) return { type: 'contract', contractId: c[1]!, fn: c[2]! };
  if (/^https?:\/\//i.test(v)) return { type: 'url', href: v };
  if (v.startsWith('/') || v.startsWith('#/')) return { type: 'page', path: normalizePagePath(v) };
  return null;
}

function normalizePagePath(p: string): string {
  const s = p.replace(/^#/, '');
  return s.startsWith('/') ? s : `/${s}`;
}

/** Collect every button-like action from the spec (action-button, hero/CTA paths, `action` props). */
function collectActions(spec: DappSpec, catalog?: readonly CatalogEntry[]): ActionEntry[] {
  const byId = new Map((catalog ?? []).map((c) => [c.id, c]));
  const out: ActionEntry[] = [];
  const seen = new Set<string>();
  for (const page of spec.pages) {
    for (const block of pageBlocks(page)) {
      const keys = new Set<string>(ACTION_PROP_KEYS);
      for (const p of byId.get(block.widget)?.props ?? []) if (p.format === 'action') keys.add(p.name);
      let action: BlockAction | null = null;
      for (const key of keys) {
        action = parseActionValue(block.props[key]);
        if (action) break;
      }
      if (!action && typeof block.props.contractId === 'string' && typeof block.props.fn === 'string' && block.props.contractId && block.props.fn) {
        action = {
          type: 'contract',
          contractId: block.props.contractId,
          fn: block.props.fn,
          ...(typeof block.props.argsFrom === 'string' && block.props.argsFrom ? { argsFrom: block.props.argsFrom } : {}),
        };
      }
      if (!action) continue;
      const key = `${page.path}:${block.id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const label = LABEL_PROP_KEYS.map((k) => block.props[k]).find((v): v is string => typeof v === 'string' && v.trim() !== '');
      out.push({ id: block.id, page: page.path, widget: block.widget, ...(label ? { label } : {}), action });
    }
  }
  return out;
}

/**
 * Source of `src/actions.ts` (Vite) / `app/actions.ts` (Next): the actions found on buttons
 * and CTAs, plus helpers to resolve and run them. Pure data and functions; no hooks.
 */
export function actionsSource(spec: DappSpec, opts: { framework?: Framework; catalog?: readonly CatalogEntry[] } = {}): string {
  const framework = opts.framework ?? 'vite';
  const entries = collectActions(spec, opts.catalog);
  const pagePaths = spec.pages.map((p) => p.path);
  const list = entries.length
    ? `[\n${entries
        .map((e) => `  { id: ${JSON.stringify(e.id)}, page: ${JSON.stringify(e.page)}, widget: ${JSON.stringify(e.widget)}${e.label ? `, label: ${JSON.stringify(e.label)}` : ''}, action: ${JSON.stringify(e.action)} }`)
        .join(',\n')}\n]`
    : '[]';
  const hash = framework === 'vite';
  return `import { CONTRACTS, contractCall, type ContractCallTarget, type ContractId } from './contracts';

/** Declarative button / CTA action, as saved in the Splendor dApp Studio. */
export type BlockAction =
  | { type: 'page'; path: string }
  | { type: 'url'; href: string }
  | { type: 'contract'; contractId: string; fn: string; argsFrom?: string };

export type ActionEntry = {
  /** Block id on its page. */
  id: string;
  page: string;
  widget: string;
  label?: string;
  action: BlockAction;
};

/** Routes of this app. */
export const PAGE_PATHS = ${JSON.stringify(pagePaths)} as const;
export type PagePath = (typeof PAGE_PATHS)[number];

/** Actions found on buttons and CTAs when the project was exported. */
export const ACTIONS: readonly ActionEntry[] = ${list};

export function actionsForPage(path: string): ActionEntry[] {
  return ACTIONS.filter((a) => a.page === path);
}

export function getAction(pagePath: string, blockId: string): BlockAction | undefined {
  return ACTIONS.find((a) => a.page === pagePath && a.id === blockId)?.action;
}

/** Link target of a page: ${hash ? 'hash routing (`#/path`)' : 'the route path'}. */
export function pageHref(path: string): string {
  const p = path.replace(/^#/, '');
  return ${hash ? "`#${p.startsWith('/') ? p : `/${p}`}`" : "p.startsWith('/') ? p : `/${p}`"};
}

/** Only http(s) links are allowed; anything else (javascript:, data:) returns null. */
export function safeUrl(href: string): string | null {
  try {
    const u = new URL(href);
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.toString() : null;
  } catch {
    return null;
  }
}

/** \`href\` for page and url actions; null for contract actions (they need a handler). */
export function actionHref(action: BlockAction): string | null {
  if (action.type === 'page') return pageHref(action.path);
  if (action.type === 'url') return safeUrl(action.href);
  return null;
}

/** Contract metadata + call target for a contract action (feed it to \`useReadContract\` / \`useContract\`). */
export function resolveContractAction(action: Extract<BlockAction, { type: 'contract' }>, args: unknown[] = []): ContractCallTarget {
  if (!(action.contractId in CONTRACTS)) throw new Error(\`Unknown contract "\${action.contractId}"\`);
  return contractCall(action.contractId as ContractId, action.fn, args);
}

export type RunActionOptions = {
  /** Called for contract actions. Run the call with \`useReadContract\` / \`useContract\` there. */
  onContract?: (target: ContractCallTarget, action: Extract<BlockAction, { type: 'contract' }>) => void | Promise<void>;
${hash ? '' : "  /** Client-side navigation, e.g. `router.push` from `next/navigation`. Defaults to a full page load. */\n  navigate?: (path: string) => void;\n"}  /** Arguments for contract actions. */
  args?: unknown[];
};

/** Run an action in the browser. Pages navigate, urls open in a new tab, contracts go to \`onContract\`. */
export async function runAction(action: BlockAction, opts: RunActionOptions = {}): Promise<void> {
  if (action.type === 'page') {
${hash ? "    window.location.hash = pageHref(action.path).slice(1);" : "    if (opts.navigate) opts.navigate(pageHref(action.path));\n    else window.location.assign(pageHref(action.path));"}
    return;
  }
  if (action.type === 'url') {
    const href = safeUrl(action.href);
    if (href) window.open(href, '_blank', 'noopener,noreferrer');
    return;
  }
  await opts.onContract?.(resolveContractAction(action, opts.args ?? []), action);
}
`;
}

function decodeWasmScript(): string {
  return `import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const b64 = join(root, 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64');
const out = join(root, 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm');
if (existsSync(b64) && !existsSync(out)) {
  writeFileSync(out, Buffer.from(readFileSync(b64, 'utf8'), 'base64'));
  console.log('[decode-wasm] wrote sqc_signer.wasm');
}
`;
}

/**
 * Vendored package files land in the generated project as-is, and callers write them to disk or a
 * zip: keep them inside `vendor/@splendor/`, with no traversal, drive letters or backslashes.
 */
function isSafeVendorPath(path: string): boolean {
  if (!path.startsWith('vendor/@splendor/') || path.length > 300 || /[\\:\0]/.test(path)) return false;
  return path.split('/').every((seg) => seg !== '' && seg !== '.' && seg !== '..');
}

/** Generate a ready-to-run project. Validate the spec first; this throws on unknown widgets. */
export function generateProject(spec: DappSpec, opts: GenerateOptions): GeneratedProject {
  const sdk = opts.sdk ?? 'npm';
  const pages = resolvePages(spec, opts.catalog);
  const files = opts.framework === 'vite'
    ? viteFiles(spec, pages, opts.catalog, opts.versions, sdk)
    : nextFiles(spec, pages, opts.catalog, opts.versions, sdk);
  files['README.md'] = readme(spec, opts.framework);
  files['.gitignore'] = opts.framework === 'vite'
    ? 'node_modules\ndist\n.env*.local\n'
    : 'node_modules\n.next\nout\n.env*.local\npublic/sqc_signer.wasm\npublic/wasm_exec.js\n';
  const appDir = opts.framework === 'vite' ? 'src' : 'app';
  files[`${appDir}/contracts.ts`] = contractsSource(spec);
  files[`${appDir}/actions.ts`] = actionsSource(spec, { framework: opts.framework, catalog: opts.catalog });
  if (sdk === 'vendor') {
    files['scripts/decode-wasm.mjs'] = decodeWasmScript();
    if (opts.vendorFiles) {
      for (const [path, content] of Object.entries(opts.vendorFiles)) {
        if (!isSafeVendorPath(path)) throw new Error(`vendorFiles: refusing unsafe path "${path}" (must be a relative path under vendor/@splendor/)`);
        files[path] = content;
      }
    }
    if (opts.vendorWasmBase64) {
      files['vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64'] = opts.vendorWasmBase64;
    }
  }
  const sorted: Record<string, string> = {};
  for (const k of Object.keys(files).sort()) sorted[k] = files[k]!;
  return { name: slugify(spec.name), framework: opts.framework, files: sorted };
}

/** JSX snippet for one block (for "copy code" in the playground). */
export function blockSnippet(block: DappBlock, entry: CatalogEntry): string {
  const imp = entry.kind === 'block'
    ? `import { ${entry.importName} } from './blocks/${entry.importName}';`
    : `import { ${entry.importName} } from '${entry.package ?? '@splendor/connect-react'}';`;
  return `${imp}\n\n${renderBlock(block, entry, '')}\n`;
}
