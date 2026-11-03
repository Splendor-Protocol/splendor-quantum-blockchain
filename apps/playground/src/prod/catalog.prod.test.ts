import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { DAPP_SPEC_VERSION, createCatalog, validateSpec, wrapBlocks, type DappSpec } from '@splendor/dapp-spec';
import { galleryProps, snippetProps } from '../gallery/GalleryPage';
import { EMBED_WIDGETS, componentExport, embedSnippet, fullPageSource, reactUsage } from '../gallery/codeExport';
import { DEMO_RECIPIENT } from '../preview/mock';

const catalog = createCatalog(WIDGET_MANIFESTS);
const placeable = catalog.filter((e) => e.placeable);
const theme = { base: 'splendor', overrides: {} };
const PINS = { wasmSha256: 'a'.repeat(64), execSha256: 'b'.repeat(64) };

function previewSpec(entry: (typeof catalog)[number]): DappSpec {
  const block = { id: 'item', widget: entry.id, props: galleryProps(entry) };
  return {
    version: DAPP_SPEC_VERSION,
    name: entry.title,
    network: 'testnet',
    theme: { base: 'splendor', overrides: {} },
    pages: [{ path: '/', title: entry.title, root: wrapBlocks([block]), blocks: [block] }],
  };
}

const unescapeAttr = (s: string) => s.replace(/&#39;/g, "'").replace(/&amp;/g, '&');

describe('gallery catalog prod', () => {
  it('[L1] the catalog is not empty and ids are unique', () => {
    expect(placeable.length).toBeGreaterThan(10);
    expect(new Set(catalog.map((e) => e.id)).size).toBe(catalog.length);
  });

  it.each(placeable.map((e) => [e.id, e] as const))('[L1] %s has a valid preview spec and every code export', (_id, entry) => {
    const v = validateSpec(previewSpec(entry), { catalog: [entry] });
    expect(v.ok, JSON.stringify(!v.ok && v.issues)).toBe(true);
    const props = snippetProps(galleryProps(entry));
    expect(reactUsage(entry, props, theme, 'testnet')).toContain(`<${entry.importName}`);
    expect(fullPageSource(entry, props, theme, 'mainnet', PINS, '3.0.0')).toContain(`wasmSha256="${PINS.wasmSha256}"`);
    const comp = componentExport(entry, props, 'testnet');
    expect(comp.fileName).toMatch(/\.tsx$/);
    expect(comp.source.length).toBeGreaterThan(0);
    for (const text of [reactUsage(entry, props, theme, 'testnet'), comp.source, comp.readme]) {
      expect(text).not.toContain(DEMO_RECIPIENT);
    }
  });

  it('[L2] every embed-offered widget is a real catalog widget', () => {
    const ids = new Set(catalog.filter((e) => e.kind !== 'block').map((e) => e.id));
    for (const id of EMBED_WIDGETS) expect(ids.has(id), id).toBe(true);
  });

  it.each([...EMBED_WIDGETS])('[L2] %s embed snippet round-trips hostile prop text through data-props', (id) => {
    const entry = catalog.find((e) => e.id === id)!;
    const props = { ...snippetProps(galleryProps(entry)), title: `it's <b>"bold"</b> & more` };
    const html = embedSnippet(entry, props, 'mainnet', '3.0.0', PINS)!;
    expect(html).toContain(`data-sqc-widget="${id}"`);
    expect(html).toContain(`data-wasm-sha256="${PINS.wasmSha256}"`);
    expect(html).toMatch(/@splendor\/connect-react@3\.0\.0\//);
    const attr = html.match(/data-props='([^']*)'/);
    expect(attr).toBeTruthy();
    expect(JSON.parse(unescapeAttr(attr![1]!))).toEqual(props);
  });

  it('[L2] blocks and unsupported widgets get no embed snippet', () => {
    for (const e of catalog.filter((x) => !EMBED_WIDGETS.has(x.id))) {
      expect(embedSnippet(e, {}, 'testnet', '3.0.0')).toBeNull();
    }
  });

  it('[L4] building every preview spec and export for the whole catalog stays under 5 s', () => {
    const t0 = performance.now();
    for (let round = 0; round < 3; round++) {
      for (const entry of placeable) {
        validateSpec(previewSpec(entry), { catalog });
        const props = snippetProps(galleryProps(entry));
        reactUsage(entry, props, theme, 'testnet');
        fullPageSource(entry, props, theme, 'testnet');
        componentExport(entry, props, 'testnet');
        embedSnippet(entry, props, 'testnet', '3.0.0');
      }
    }
    expect(performance.now() - t0).toBeLessThan(5_000);
  });
});
