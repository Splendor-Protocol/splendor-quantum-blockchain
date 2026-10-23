import { BLOCK_MANIFESTS, blockSnippet, blockSource, type CatalogEntry, type NetworkId, type PropValue } from '@splendor/dapp-spec';

/** Same shape the Theme tab edits (`ThemeValue` in ThemeEditor). */
export type CodeTheme = { base: string; overrides: Record<string, string | undefined> };

/** Widgets the `@splendor/connect-react/embed` script can mount (see `src/embed.tsx`). */
export const EMBED_WIDGETS: ReadonlySet<string> = new Set(['transfer', 'account-card', 'token-panel', 'deploy-token', 'sign-in', 'pending-tx']);

export const EMBED_SCRIPT_URL = 'https://unpkg.com/@splendor/connect-react/dist/embed.js';
export const EMBED_STYLE_URL = 'https://unpkg.com/@splendor/connect-react/dist/theme/tokens.css';

export function themeProp(theme: CodeTheme): string {
  const overrides = Object.fromEntries(Object.entries(theme.overrides).filter(([, v]) => v));
  return Object.keys(overrides).length
    ? `theme={${JSON.stringify({ ...(theme.base !== 'splendor' ? { base: theme.base } : {}), ...overrides })}}`
    : `theme="${theme.base}"`;
}

/** The JSX element for the entry (no import line). */
function elementJsx(entry: CatalogEntry, props: Record<string, PropValue>): string {
  return blockSnippet({ id: 'item', widget: entry.id, props }, entry).split('\n\n').slice(1).join('\n\n').trim();
}

function indent(text: string, spaces: number): string {
  const pad = ' '.repeat(spaces);
  return text
    .split('\n')
    .map((l) => (l ? pad + l : l))
    .join('\n');
}

function importLine(entry: CatalogEntry, from: string): string {
  return `import { ${entry.importName} } from '${from}';`;
}

/** Where the component is imported from in a user's project. */
function importSource(entry: CatalogEntry): string {
  return entry.kind === 'block' ? `./${entry.importName}` : (entry.package ?? '@splendor/connect-react');
}

/** Code-tab "React" section: the element plus the provider it needs. */
export function reactUsage(entry: CatalogEntry, props: Record<string, PropValue>, theme: CodeTheme, network: NetworkId): string {
  const imp = entry.kind === 'block' ? importLine(entry, `./blocks/${entry.importName}`) : importLine(entry, importSource(entry));
  return `import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';
${imp}

export function Example() {
  return (
    <SplendorConnectProvider network="${network}" connectors="auto" ${themeProp(theme)}>
      <ConnectKit />
${indent(elementJsx(entry, props), 6)}
    </SplendorConnectProvider>
  );
}
`;
}

/** Plain-HTML embed snippet, or null when the embed script does not support this entry. */
export function embedSnippet(entry: CatalogEntry, props: Record<string, PropValue>, network: NetworkId): string | null {
  if (!EMBED_WIDGETS.has(entry.id)) return null;
  const json = JSON.stringify(props).replace(/&/g, '&amp;').replace(/'/g, '&#39;');
  return `<link rel="stylesheet" href="${EMBED_STYLE_URL}" />

<div data-sqc-widget="${entry.id}" data-network="${network}" data-props='${json}'></div>

<script src="${EMBED_SCRIPT_URL}"></script>
<!-- Serve sqc_signer.wasm and wasm_exec.js from your site root (npx sqc-copy-wasm).
     Add data-wasm-sha256 and data-exec-sha256 to pin the signer; mainnet widgets refuse to load without them. -->`;
}

/** A complete `App.tsx` that renders the entry inside the provider. */
export function fullPageSource(entry: CatalogEntry, props: Record<string, PropValue>, theme: CodeTheme, network: NetworkId): string {
  const note = entry.kind === 'block' ? `// Save ${entry.importName}.tsx (Download as component) next to this file.\n` : '';
  const widgetImport = entry.kind === 'block' ? '' : `, ${entry.importName}`;
  const blockImport = entry.kind === 'block' ? `\n${importLine(entry, importSource(entry))}` : '';
  return `${note}import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit, TxToastHost${widgetImport} } from '@splendor/connect-react';${blockImport}

export default function App() {
  return (
    // For production, pass wasmSha256 and execSha256 instead of allowUnpinnedWasm.
    <SplendorConnectProvider network="${network}" connectors="auto" ${themeProp(theme)} allowUnpinnedWasm>
      <TxToastHost>
        <main style={{ maxWidth: 720, margin: '0 auto', padding: '2rem 1rem', display: 'grid', gap: '1rem' }}>
          <ConnectKit />
${indent(elementJsx(entry, props), 10)}
        </main>
      </TxToastHost>
    </SplendorConnectProvider>
  );
}
`;
}

export type ComponentExport = { fileName: string; source: string; readmeName: string; readme: string };

/**
 * Single-file component for "Download as component". Blocks ship their own source verbatim;
 * widgets get a one-component wrapper with the current settings baked in.
 */
export function componentExport(entry: CatalogEntry, props: Record<string, PropValue>, network: NetworkId): ComponentExport {
  let fileName: string;
  let source: string;
  let usage: string;
  if (entry.kind === 'block') {
    const manifest = BLOCK_MANIFESTS.find((m) => m.id === entry.id);
    fileName = `${entry.importName}.tsx`;
    source = manifest ? blockSource(manifest.file) : `// Source for ${entry.importName} is not available.\n`;
    usage = `import { ${entry.importName} } from './${entry.importName}';\n\n${elementJsx(entry, props)}`;
  } else {
    const name = `${entry.importName}Example`;
    fileName = `${name}.tsx`;
    source = `import { ${entry.importName} } from '${entry.package ?? '@splendor/connect-react'}';

/** ${entry.title}: ${entry.description} */
export function ${name}() {
${indent(`return (\n${indent(elementJsx(entry, props), 2)}\n);`, 2)}
}
`;
    usage = `import { ${name} } from './${name}';\n\n<${name} />`;
  }
  const readme = `# ${entry.title}

${entry.description}

Generated by the Splendor dApp Studio gallery. Single file: \`${fileName}\`.

## Install

\`\`\`bash
npm install @splendor/connect-react @splendor/sqc-sdk react react-dom
\`\`\`

## Use

Render it inside \`SplendorConnectProvider\` (and import the stylesheet once):

\`\`\`tsx
import '@splendor/connect-react/style.css';
import { SplendorConnectProvider, ConnectKit } from '@splendor/connect-react';

<SplendorConnectProvider network="${network}" connectors="auto">
  <ConnectKit />
  {/* below */}
</SplendorConnectProvider>
\`\`\`

\`\`\`tsx
${usage}
\`\`\`

Serve \`sqc_signer.wasm\` and \`wasm_exec.js\` from your site root (\`npx sqc-copy-wasm\`).
${entry.requires?.wallet ? '\nThis component needs a connected wallet.\n' : ''}`;
  return { fileName, source, readmeName: 'README.md', readme };
}

/** Save text as a file through a temporary link. Browser only. */
export function downloadText(fileName: string, text: string, mime = 'text/plain'): void {
  const url = URL.createObjectURL(new Blob([text], { type: `${mime};charset=utf-8` }));
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
