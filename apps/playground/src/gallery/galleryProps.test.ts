import { describe, expect, it } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, validateSpec, type DappSpec } from '@splendor/dapp-spec';
import { DEMO_RECIPIENT } from '../preview/mock';
import { YOUR_ADDRESS, galleryProps, snippetProps } from './GalleryPage';

const catalog = createCatalog(WIDGET_MANIFESTS);

describe('gallery demo props', () => {
  it('every placeable entry previews as a valid one-block spec', () => {
    for (const entry of catalog.filter((e) => e.placeable)) {
      const spec: DappSpec = {
        version: 1,
        name: entry.title,
        network: 'testnet',
        theme: { base: 'splendor', overrides: {} },
        pages: [{ path: '/', title: entry.title, blocks: [{ id: 'item', widget: entry.id, props: galleryProps(entry) }] }],
      };
      const r = validateSpec(spec, { catalog: [entry] });
      expect(r.ok, `${entry.id}: ${JSON.stringify(!r.ok && r.issues)}`).toBe(true);
    }
  });

  it('copied code never contains the demo address', () => {
    const props = galleryProps(catalog.find((e) => e.id === 'tip-jar')!);
    expect(snippetProps(props)).toMatchObject({ recipient: YOUR_ADDRESS, amount: props.amount });
    expect(snippetProps({ recipient: 'SQ:' + 'ab'.repeat(32) }).recipient).toBe('SQ:' + 'ab'.repeat(32));
  });

  it('fills required fields with demo values', () => {
    expect(galleryProps(catalog.find((e) => e.id === 'tip-jar')!).recipient).toBe(DEMO_RECIPIENT);
    expect(galleryProps(catalog.find((e) => e.id === 'contract-call')!)).toMatchObject({ contract: DEMO_RECIPIENT, functionSig: 'get()' });
  });
});
