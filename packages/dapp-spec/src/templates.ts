import { DAPP_SPEC_VERSION, type DappBlock, type DappPage, type DappSpec } from './types.js';
import { wrapBlocks } from './tree.js';

export type DappTemplate = {
  id: string;
  title: string;
  description: string;
  /** Short list of what the user must fill in before the spec validates (e.g. a recipient). */
  setup: { blockId: string; prop: string; label: string }[];
  spec: DappSpec;
};

function page(path: string, title: string, blocks: DappBlock[], rootId?: string): DappPage {
  const root = wrapBlocks(blocks, rootId ?? `${path === '/' ? 'home' : path.slice(1)}-root`);
  return { path, title, root, blocks };
}

const base = (name: string, description: string, pages: DappPage[]): DappSpec => ({
  version: DAPP_SPEC_VERSION,
  name,
  description,
  network: 'testnet',
  theme: { base: 'splendor', overrides: {}, preset: 'splendor' },
  pages,
  contracts: [],
  nav: { brand: name, showConnect: true },
});

export const TEMPLATES: readonly DappTemplate[] = [
  {
    id: 'wallet-dashboard',
    title: 'Wallet dashboard',
    description: 'Account, balance, SPLD transfers and pending transactions — the smallest useful dApp.',
    setup: [],
    spec: base('Wallet Dashboard', 'Check your SPLD balance and send transfers on Splendor.', [
      page('/', 'Dashboard', [
        { id: 'hero', widget: 'hero', props: { heading: 'Wallet dashboard', text: 'Your Splendor account at a glance.' } },
        { id: 'account', widget: 'account-card', props: { showBalance: true, showNetwork: true, showExplorer: true, showDisconnect: true } },
        { id: 'send', widget: 'transfer', props: { defaultAmount: '0.01', showFee: true, toast: true } },
        { id: 'pending', widget: 'pending-tx', props: { emptyText: 'Nothing pending', pollMs: 5000 } },
      ]),
    ]),
  },
  {
    id: 'token-launchpad',
    title: 'Token launchpad',
    description: 'Launch a QRC-20 token, then manage balances and transfers on a second page.',
    setup: [],
    spec: base('Token Launchpad', 'Create and manage QRC-20 tokens on Splendor.', [
      page('/', 'Launch', [
        { id: 'hero', widget: 'hero', props: { heading: 'Launch your token', text: 'Deploy a QRC-20 token in one transaction.' } },
        { id: 'deploy', widget: 'deploy-token', props: { defaultName: 'My Token', defaultSymbol: 'MYT', defaultSupply: '1000000', decimals: 18, review: true, showFee: true, toast: true } },
        { id: 'account', widget: 'account-card', props: { showBalance: true } },
      ]),
      page('/manage', 'Manage', [
        { id: 'token', widget: 'token-panel', props: { defaultAmount: '1', showFee: true, toast: true } },
        { id: 'pending', widget: 'pending-tx', props: { pollMs: 5000 } },
      ]),
    ]),
  },
  {
    id: 'tip-jar',
    title: 'Pay / tip jar',
    description: 'Accept SPLD tips to a fixed address, with the real network fee shown before signing.',
    setup: [{ blockId: 'tips', prop: 'recipient', label: 'Address that receives tips' }],
    spec: base('Tip Jar', 'Support this project with SPLD.', [
      page('/', 'Tip', [
        { id: 'hero', widget: 'hero', props: { heading: 'Buy me a coffee', text: 'Tips go straight to my Splendor address.' } },
        { id: 'tips', widget: 'tip-jar', props: { title: 'Send a tip', recipient: '', amount: '0.1' } },
        { id: 'summary', widget: 'balance-summary', props: { title: 'Your wallet' } },
      ]),
    ]),
  },
  {
    id: 'members-area',
    title: 'Members area',
    description: 'Wallet sign-in that unlocks members-only content; verify on your server with verifySignIn.',
    setup: [],
    spec: base('Members Club', 'Sign in with your Splendor wallet to see members-only content.', [
      page('/', 'Home', [
        { id: 'hero', widget: 'hero', props: { heading: 'Members club', text: 'Sign in with your wallet — no passwords, no email.' } },
        {
          id: 'members',
          widget: 'members-area',
          props: {
            title: 'Members area',
            statement: 'Sign in to the members club',
            content: "Welcome back! Here is this week's members-only update.",
          },
        },
        { id: 'account', widget: 'account-card', props: { showBalance: false } },
      ]),
    ]),
  },
  {
    id: 'nft-drop',
    title: 'NFT drop',
    description: 'Deploy a QRC-1155 collection (or use one) and let visitors mint and see what they own.',
    setup: [],
    spec: base('NFT Drop', 'Mint a Splendor collectible.', [
      page('/', 'Drop', [
        { id: 'hero', widget: 'hero', props: { heading: 'Genesis drop', text: 'Mint your collectible on Splendor Quantum Chain.' } },
        { id: 'drop', widget: 'nft-drop', props: { title: 'Mint', collectionName: 'Genesis', tokenId: '1', amount: '1' } },
        { id: 'pending', widget: 'pending-tx', props: { pollMs: 5000 } },
      ]),
    ]),
  },
  {
    id: 'landing',
    title: 'Landing page',
    description: 'Marketing hero, features, FAQ and a wallet CTA — ready for a product site.',
    setup: [],
    spec: base('Splendor Landing', 'A polished landing page for your Splendor product.', [
      page('/', 'Home', [
        { id: 'hero', widget: 'hero-centered', props: { heading: 'Build on Splendor', text: 'Post-quantum dApps without the boilerplate.', ctaLabel: 'Open app', ctaPath: '/app' } },
        { id: 'features', widget: 'feature-grid', props: { title: 'Why Splendor', items: 'Wallet ready|Drop-in connect kit,Tokens|QRC-20 and NFT modules,Contracts|Bind any deployed ABI' } },
        { id: 'cta', widget: 'cta-banner', props: { heading: 'Ready to ship?', text: 'Export a Vite or Next.js project in one click.', buttonLabel: 'Start building', buttonPath: '/app' } },
        { id: 'faq', widget: 'faq', props: { title: 'FAQ', items: 'Is it post-quantum?|Yes — ML-DSA-65 signatures.,Do I need a wallet?|Use the extension or an in-page seed.' } },
        { id: 'footer', widget: 'footer-bar', props: { text: 'Built with the Splendor dApp Studio' } },
      ]),
      page('/app', 'App', [
        { id: 'app-hero', widget: 'hero', props: { heading: 'Your app', text: 'Connect and try it live.' } },
        { id: 'account', widget: 'account-card', props: { showBalance: true, showNetwork: true } },
        { id: 'send', widget: 'transfer', props: { defaultAmount: '0.01', showFee: true, toast: true } },
      ]),
    ]),
  },
];

/** Starter page layouts for the studio Pages panel. */
export type PageTemplate = {
  id: string;
  title: string;
  description: string;
  build: (path: string, title: string) => DappPage;
};

export const PAGE_TEMPLATES: readonly PageTemplate[] = [
  {
    id: 'blank',
    title: 'Blank',
    description: 'Empty section ready for pieces.',
    build: (path, title) => page(path, title, []),
  },
  {
    id: 'landing',
    title: 'Landing',
    description: 'Hero, features, CTA and FAQ.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero-centered', props: { heading: title, text: 'Tell visitors what this page is for.', ctaLabel: 'Get started' } },
        { id: `${path}-features`, widget: 'feature-grid', props: { title: 'Highlights', items: 'Fast|Ship in minutes,Secure|Post-quantum signatures,Open|Export real code' } },
        { id: `${path}-cta`, widget: 'cta-banner', props: { heading: 'Next step', text: 'Connect a wallet and try it.', buttonLabel: 'Connect' } },
      ]),
  },
  {
    id: 'dashboard',
    title: 'Dashboard',
    description: 'Account, balance and activity.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'Your account at a glance.' } },
        { id: `${path}-account`, widget: 'account-card', props: { showBalance: true, showNetwork: true } },
        { id: `${path}-pending`, widget: 'pending-tx', props: { emptyText: 'Nothing pending' } },
      ]),
  },
  {
    id: 'transactions',
    title: 'Transactions',
    description: 'Transfer form and pending list.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'Send SPLD and track pending txs.' } },
        { id: `${path}-transfer`, widget: 'transfer', props: { defaultAmount: '0.01', showFee: true, toast: true } },
        { id: `${path}-pending`, widget: 'pending-tx', props: { pollMs: 5000 } },
      ]),
  },
  {
    id: 'token',
    title: 'Token',
    description: 'Deploy or manage a QRC-20.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'Token tools.' } },
        { id: `${path}-panel`, widget: 'token-panel', props: { defaultAmount: '1', showFee: true } },
      ]),
  },
  {
    id: 'nft',
    title: 'NFT',
    description: 'Mint and show collectibles.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'Your collection.' } },
        { id: `${path}-drop`, widget: 'nft-drop', props: { title: 'Mint', collectionName: 'Collection', tokenId: '1', amount: '1' } },
      ]),
  },
  {
    id: 'members',
    title: 'Members',
    description: 'Sign-in gate and account card.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'Members only.' } },
        { id: `${path}-members`, widget: 'members-area', props: { title: 'Members', statement: 'Sign in', content: 'Welcome.' } },
      ]),
  },
  {
    id: 'about',
    title: 'About',
    description: 'Heading, text and footer.',
    build: (path, title) =>
      page(path, title, [
        { id: `${path}-hero`, widget: 'hero', props: { heading: title, text: 'About this project.' } },
        { id: `${path}-text`, widget: 'rich-text', props: { text: 'Add your story here.' } },
        { id: `${path}-footer`, widget: 'footer-bar', props: { text: 'Built on Splendor' } },
      ]),
  },
];

export function getTemplate(id: string): DappTemplate | undefined {
  return TEMPLATES.find((t) => t.id === id);
}

export function getPageTemplate(id: string): PageTemplate | undefined {
  return PAGE_TEMPLATES.find((t) => t.id === id);
}

/** Blank spec for "start from scratch". */
export function emptySpec(name = 'My Splendor dApp'): DappSpec {
  return base(name, '', [
    page('/', 'Home', [{ id: 'hero', widget: 'hero', props: { heading: name, text: '' } }]),
  ]);
}

/** Deep copy of a template spec so editors never mutate the shared constant. */
export function instantiateTemplate(t: DappTemplate): DappSpec {
  return JSON.parse(JSON.stringify(t.spec)) as DappSpec;
}
