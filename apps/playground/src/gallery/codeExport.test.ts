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
    const embed = embedSnippet(e, props, 'testnet')!;
    expect(embed).toContain('data-sqc-widget="transfer"');
    expect(embed).toContain('embed.js');
  });

  it('only offers the embed script for widgets it can mount', () => {
    expect(embedSnippet(entry('hero'), {}, 'testnet')).toBeNull();
  });

  it('escapes quotes in embed props', () => {
    const out = embedSnippet(entry('transfer'), { defaultAmount: "it's" }, 'testnet')!;
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
