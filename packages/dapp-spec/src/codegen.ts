import { BLOCK_MANIFESTS, blockSource } from './blocks.js';
import type { CatalogEntry, DappBlock, DappPage, DappSpec, PropValue, ThemeOverrides } from './types.js';

export type Framework = 'vite' | 'next';

/** Published package versions to pin (`3.0.0` becomes `^3.0.0`; ranges are kept as given). */
export type PackageVersions = { sqcSdk: string; connectReact: string };

export type GenerateOptions = {
  framework: Framework;
  /** Placeable items (widgets from the connect-react manifest + `BLOCK_MANIFESTS`). */
  catalog: readonly CatalogEntry[];
  versions: PackageVersions;
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

function renderBlock(block: DappBlock, entry: CatalogEntry, indent: string): string {
  const props = Object.entries(block.props).filter(([, v]) => v !== '' && v != null);
  const attrs = props.map(([k, v]) => jsxProp(k, v));
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

function resolvePages(spec: DappSpec, catalog: readonly CatalogEntry[]): Resolved[] {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  return spec.pages.map((page) => {
    const entries = page.blocks.map((b) => {
      const e = byId.get(b.widget);
      if (!e) throw new Error(`dapp-spec codegen: unknown widget "${b.widget}" (validate the spec first)`);
      return e;
    });
    const uniq = (list: CatalogEntry[]) => [...new Map(list.map((e) => [e.id, e])).values()];
    return {
      page,
      component: pageComponent(page),
      usedWidgets: uniq(entries.filter((e) => e.kind !== 'block')),
      usedBlocks: uniq(entries.filter((e) => e.kind === 'block')),
    };
  });
}

function pageSource(r: Resolved, opts: { blocksDir: string; client: boolean }, catalog: readonly CatalogEntry[]): string {
  const byId = new Map(catalog.map((c) => [c.id, c]));
  const lines: string[] = [];
  if (opts.client) lines.push(`'use client';`, '');
  const pkgs = new Map<string, string[]>();
  for (const w of r.usedWidgets) {
    const pkg = w.package ?? '@splendor/connect-react';
    pkgs.set(pkg, [...(pkgs.get(pkg) ?? []), w.importName]);
  }
  for (const [pkg, names] of pkgs) lines.push(`import { ${[...new Set(names)].sort().join(', ')} } from '${pkg}';`);
  for (const b of r.usedBlocks) lines.push(`import { ${b.importName} } from '${opts.blocksDir}/${b.importName}';`);
  if (lines.length) lines.push('');
  const body = r.page.blocks.length
    ? r.page.blocks.map((b) => renderBlock(b, byId.get(b.widget)!, '      ')).join('\n')
    : '      <p className="dapp-empty">This page has no blocks yet.</p>';
  lines.push(
    `export function ${r.component}() {`,
    '  return (',
    '    <div className="dapp-grid">',
    body,
    '    </div>',
    '  );',
    '}',
    '',
  );
  return lines.join('\n');
}

const APP_CSS = `:root {
  color-scheme: dark;
}
body {
  margin: 0;
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
}
.dapp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 1rem;
  align-items: start;
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

## Learn more

- Widgets and hooks: \`node_modules/@splendor/connect-react/docs/GUIDE.md\`
- SDK: \`node_modules/@splendor/sqc-sdk/README.md\`
`;
}

function viteFiles(spec: DappSpec, pages: Resolved[], catalog: readonly CatalogEntry[], v: PackageVersions): Record<string, string> {
  const name = slugify(spec.name);
  const files: Record<string, string> = {};
  files['package.json'] =
    JSON.stringify(
      {
        name,
        private: true,
        version: '0.1.0',
        type: 'module',
        scripts: { dev: 'vite', build: 'tsc --noEmit && vite build', preview: 'vite preview' },
        dependencies: {
          '@splendor/connect-react': range(v.connectReact),
          '@splendor/sqc-sdk': range(v.sqcSdk),
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
  files['src/App.tsx'] = `import { useEffect, useState } from 'react';
import {
  ConnectKit,
  SplendorConnectProvider,
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
        <div className="dapp-shell" style={themeOverridesToStyle(THEME_OVERRIDES)}>
          <header className="dapp-header">
            <p className="dapp-brand">${spec.name.replace(/[<>{}&]/g, '')}</p>
            <ConnectKit />
          </header>
          <WrongNetworkBanner expectedNetworkId={NETWORK} />
          {ROUTES.length > 1 ? (
            <nav className="dapp-nav">
              {ROUTES.map((r) => (
                <a key={r.path} href={\`#\${r.path}\`} aria-current={r === route ? 'page' : undefined}>
                  {r.title}
                </a>
              ))}
            </nav>
          ) : null}
          <main>
            <route.Page />
          </main>
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}
`;
  for (const r of pages) files[`src/pages/${r.component}.tsx`] = pageSource(r, { blocksDir: '../blocks', client: false }, catalog);
  addBlocks(files, pages, 'src/blocks');
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

function addBlocks(files: Record<string, string>, pages: Resolved[], dir: string): void {
  const blocks = new Map(pages.flatMap((r) => r.usedBlocks).map((b) => [b.id, b]));
  for (const b of blocks.values()) {
    const manifest = BLOCK_MANIFESTS.find((m) => m.id === b.id);
    if (!manifest) throw new Error(`dapp-spec codegen: block "${b.id}" has no source`);
    files[`${dir}/${manifest.importName}.tsx`] = blockSource(manifest.file);
  }
}

function nextFiles(spec: DappSpec, pages: Resolved[], catalog: readonly CatalogEntry[], v: PackageVersions): Record<string, string> {
  const name = slugify(spec.name);
  const files: Record<string, string> = {};
  files['package.json'] =
    JSON.stringify(
      {
        name,
        private: true,
        version: '0.1.0',
        scripts: {
          predev: 'node scripts/copy-sqc-wasm.mjs',
          dev: 'next dev',
          prebuild: 'node scripts/copy-sqc-wasm.mjs',
          build: 'next build',
          start: 'next start',
        },
        dependencies: {
          '@splendor/connect-react': range(v.connectReact),
          '@splendor/sqc-sdk': range(v.sqcSdk),
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

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    return rpcRewrites;
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
  const routes = pages.map((r) => ({ path: r.page.path, title: r.page.title }));
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
  ConnectKit,
  SplendorConnectProvider,
  TxToastHost,
  WrongNetworkBanner,
  themeOverridesToStyle,
  type ThemeOverrides,
} from '@splendor/connect-react';
import { appNetworks } from './networks';

const NETWORK = ${JSON.stringify(spec.network)} as const;
const THEME = ${JSON.stringify(spec.theme.base)} as const;
const THEME_OVERRIDES: ThemeOverrides = ${themeConst(spec.theme.overrides)};
const ROUTES = ${JSON.stringify(routes)};

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
        <div className="dapp-shell" style={themeOverridesToStyle(THEME_OVERRIDES)}>
          <header className="dapp-header">
            <p className="dapp-brand">${spec.name.replace(/[<>{}&]/g, '')}</p>
            <ConnectKit />
          </header>
          <WrongNetworkBanner expectedNetworkId={NETWORK} />
          {ROUTES.length > 1 ? (
            <nav className="dapp-nav">
              {ROUTES.map((r) => (
                <Link key={r.path} href={r.path} aria-current={r.path === pathname ? 'page' : undefined}>
                  {r.title}
                </Link>
              ))}
            </nav>
          ) : null}
          <main>{children}</main>
        </div>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}
`;
  for (const r of pages) {
    const dir = r.page.path === '/' ? 'app' : `app${r.page.path}`;
    const depth = r.page.path === '/' ? '..' : '../..';
    files[`${dir}/${r.component}.tsx`] = pageSource(r, { blocksDir: `${depth}/blocks`, client: true }, catalog);
    files[`${dir}/page.tsx`] = `import { ${r.component} } from './${r.component}';

export const metadata = { title: ${JSON.stringify(r.page.title)} };

export default function Page() {
  return <${r.component} />;
}
`;
  }
  addBlocks(files, pages, 'blocks');
  for (const [path, src] of Object.entries(files)) {
    if (path.startsWith('blocks/')) files[path] = `'use client';\n\n${src}`;
  }
  files['.env.example'] = `# Call public RPCs directly instead of through the /rpc/<network> rewrite.
# NEXT_PUBLIC_SQC_DIRECT_RPC=1
`;
  return files;
}

/** Generate a ready-to-run project. Validate the spec first; this throws on unknown widgets. */
export function generateProject(spec: DappSpec, opts: GenerateOptions): GeneratedProject {
  const pages = resolvePages(spec, opts.catalog);
  const files = opts.framework === 'vite'
    ? viteFiles(spec, pages, opts.catalog, opts.versions)
    : nextFiles(spec, pages, opts.catalog, opts.versions);
  files['README.md'] = readme(spec, opts.framework);
  files['.gitignore'] = opts.framework === 'vite'
    ? 'node_modules\ndist\n.env.local\n'
    : 'node_modules\n.next\nout\n.env*.local\npublic/sqc_signer.wasm\npublic/wasm_exec.js\n';
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
