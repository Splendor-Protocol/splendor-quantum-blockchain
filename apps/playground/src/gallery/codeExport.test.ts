import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, defaultProps } from '@splendor/dapp-spec';
import { componentExport, embedSnippet, fullPageSource, reactUsage } from './codeExport';

const catalog = createCatalog(WIDGET_MANIFESTS);
const entry = (id: string) => catalog.find((e) => e.id === id)!;
const theme = { base: 'splendor', overrides: {} };

describe('code export', () => {
  it('builds React, embed and full-page code for a widget', () => {
    const e = entry('transfer');
    const props = defaultProps(e);
    expect(reactUsage(e, props, theme, 'testnet')).toContain('<TransferWidget');
    expect(fullPageSource(e, props, theme, 'testnet')).toContain('export default function App');
    const embed = embedSnippet(e, props, 'testnet', '3.0.0')!;
    expect(embed).toContain('data-sqc-widget="transfer"');
    expect(embed).toContain('embed.js');
  });

  it('pins the signer in full-page code and never allows an unpinned one', () => {
    const e = entry('transfer');
    const unpinned = fullPageSource(e, defaultProps(e), theme, 'mainnet');
    expect(unpinned).not.toContain('allowUnpinnedWasm');
    expect(unpinned).toContain('wasmSha256={import.meta.env.VITE_SQC_WASM_SHA256}');
    const pins = { wasmSha256: 'a'.repeat(64), execSha256: 'b'.repeat(64) };
    const pinned = fullPageSource(e, defaultProps(e), theme, 'mainnet', pins, '3.0.0');
    expect(pinned).not.toContain('allowUnpinnedWasm');
    expect(pinned).toContain(`wasmSha256="${pins.wasmSha256}" execSha256="${pins.execSha256}"`);
    // The digests belong to one version, so the install line names it.
    expect(pinned).toContain('npm i @splendor/connect-react@3.0.0');
    expect(pinned).toContain('@splendor/connect-react@3.0.0 ships');
  });

  it('loads the embed script and styles at an exact version, with pins when known', () => {
    const e = entry('transfer');
    const embed = embedSnippet(e, defaultProps(e), 'testnet', '3.0.0')!;
    expect(embed).toContain('https://unpkg.com/@splendor/connect-react@3.0.0/dist/embed.js');
    expect(embed).toContain('https://unpkg.com/@splendor/connect-react@3.0.0/dist/theme/tokens.css');
    expect(embed).not.toMatch(/connect-react\/dist/);
    const pinned = embedSnippet(e, defaultProps(e), 'mainnet', '3.0.0', { wasmSha256: 'a'.repeat(64), execSha256: 'b'.repeat(64) })!;
    expect(pinned).toContain(`data-wasm-sha256="${'a'.repeat(64)}" data-exec-sha256="${'b'.repeat(64)}"`);
  });

  it('only offers the embed script for widgets it can mount', () => {
    expect(embedSnippet(entry('hero'), {}, 'testnet', '3.0.0')).toBeNull();
  });

  it('escapes quotes in embed props', () => {
    const out = embedSnippet(entry('transfer'), { defaultAmount: "it's" }, 'testnet', '3.0.0')!;
    expect(out).toContain('&#39;');
    expect(out).not.toContain("it's");
  });

  it('exports every placeable entry as a single file with a README', () => {
    for (const e of catalog.filter((x) => x.placeable)) {
      const out = componentExport(e, defaultProps(e), 'testnet');
      expect(out.fileName.endsWith('.tsx'), e.id).toBe(true);
      expect(out.source, e.id).toContain(e.kind === 'block' ? `export function ${e.importName}` : e.importName);
      expect(out.readme, e.id).toContain(out.fileName);
    }
  });
});
