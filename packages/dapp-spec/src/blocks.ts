import { BLOCK_SOURCES } from './blocks.generated.js';
import type { CatalogEntry } from './types.js';

/** A block: a small component shipped as source and copied into generated projects. */
export type BlockManifest = CatalogEntry & {
  kind: 'block';
  /** File under `blocks/` (and `src/blocks/` in generated projects). */
  file: string;
};

export const BLOCK_MANIFESTS: readonly BlockManifest[] = [
  {
    id: 'hero',
    title: 'Hero text',
    description: 'Page heading with a short description.',
    category: 'layout',
    kind: 'block',
    importName: 'Hero',
    file: 'Hero.tsx',
    status: 'stable',
    placeable: true,
    props: [
      { name: 'heading', label: 'Heading', type: 'string', format: 'text' },
      { name: 'text', label: 'Text', type: 'string', format: 'text' },
    ],
    defaults: { heading: 'My Splendor dApp', text: '' },
    requires: { wallet: false },
  },
  {
    id: 'balance-summary',
    title: 'Balance summary',
    description: 'Network, address and SPLD balance with a refresh button.',
    category: 'wallet',
    kind: 'block',
    importName: 'BalanceSummary',
    file: 'BalanceSummary.tsx',
    status: 'stable',
    placeable: true,
    props: [{ name: 'title', label: 'Title', type: 'string', format: 'text' }],
    defaults: { title: 'Overview' },
    requires: { wallet: true },
  },
  {
    id: 'tip-jar',
    title: 'Tip jar',
    description: 'Fixed-recipient SPLD tips built from widget parts, with the real fee.',
    category: 'wallet',
    kind: 'block',
    importName: 'TipJar',
    file: 'TipJar.tsx',
    status: 'stable',
    placeable: true,
    props: [
      { name: 'title', label: 'Title', type: 'string', format: 'text' },
      { name: 'recipient', label: 'Recipient', type: 'string', format: 'address', placeholder: 'SQ:…', required: true },
      { name: 'amount', label: 'Suggested tip (SPLD)', type: 'string', format: 'amount' },
    ],
    defaults: { title: 'Tip jar', amount: '0.1' },
    requires: { wallet: true },
  },
  {
    id: 'members-area',
    title: 'Members area',
    description: 'Content that unlocks after a wallet sign-in, verified by your server when apiUrl is set.',
    category: 'auth',
    kind: 'block',
    importName: 'MembersArea',
    file: 'MembersArea.tsx',
    status: 'stable',
    placeable: true,
    props: [
      { name: 'title', label: 'Title', type: 'string', format: 'text' },
      { name: 'statement', label: 'Sign-in statement', type: 'string', format: 'text' },
      { name: 'content', label: 'Members-only text', type: 'string', format: 'text' },
      { name: 'apiUrl', label: 'Verify server URL', type: 'string', format: 'url', placeholder: 'https://api.example.com' },
    ],
    defaults: { title: 'Members area', statement: 'Sign in to the members area' },
    requires: { wallet: true },
  },
  {
    id: 'nft-drop',
    title: 'NFT drop (QRC-1155)',
    description: 'Deploy or use a QRC-1155 collection, mint to yourself and show your balance.',
    category: 'nft',
    kind: 'block',
    importName: 'NftDrop',
    file: 'NftDrop.tsx',
    status: 'beta',
    placeable: true,
    props: [
      { name: 'title', label: 'Title', type: 'string', format: 'text' },
      { name: 'collection', label: 'Collection address', type: 'string', format: 'address', placeholder: 'SQ:… (empty: deploy)' },
      { name: 'collectionName', label: 'New collection name', type: 'string', format: 'text' },
      { name: 'tokenId', label: 'Token id', type: 'string', format: 'amount' },
      { name: 'amount', label: 'Amount per mint', type: 'string', format: 'amount' },
    ],
    defaults: { title: 'NFT drop', tokenId: '1', amount: '1', collectionName: 'My Collection' },
    requires: { wallet: true, modules: ['qrc1155'] },
  },
];

/** Source text of a block file, for code generation. */
export function blockSource(file: string): string {
  const src = BLOCK_SOURCES[file];
  if (src == null) throw new Error(`dapp-spec: no source for block ${file}`);
  return src;
}
