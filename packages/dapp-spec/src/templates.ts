import {
  DAPP_SPEC_VERSION,
  type ContainerNode,
  type ContractFunction,
  type ContractRef,
  type DappBlock,
  type DappPage,
  type DappSpec,
  type LayoutNode,
  type PageAccess,
} from './types.js';
import { mapTree, wrapBlocks } from './tree.js';

/**
 * One question a kit asks before opening ("Your token address"). The answer fills the contract
 * `contractId` in `spec.contracts` (every block bound to it follows) and any raw `targets` props
 * (widgets that take an address directly).
 */
export type TemplateSlot = {
  id: string;
  kind: 'token' | 'contract' | 'address' | 'text';
  label: string;
  help?: string;
  placeholder?: string;
  required?: boolean;
  contractId?: string;
  targets?: { blockId: string; prop: string }[];
  /**
   * Optional slots: blocks removed (with the slot's demo contract) when the wizard ran and this slot
   * was left empty, so an export never ships placeholders the user did not choose.
   */
  removeWhenSkipped?: string[];
};

/** A slot answer: an address or text, or a contract with its ABI functions. */
export type SlotAnswer = string | { address: string; name?: string; functions?: ContractFunction[] };
export type SlotAnswers = Readonly<Record<string, SlotAnswer | undefined>>;

export type DappTemplate = {
  id: string;
  title: string;
  description: string;
  /** Short list of what the user must fill in before the spec validates (e.g. a recipient). */
  setup: { blockId: string; prop: string; label: string }[];
  spec: DappSpec;
  /** Ready-made multi-page app kit (landing + dashboard): offered first, with a setup step. */
  kit?: boolean;
  /** Questions the kit wizard asks; answers are applied by `instantiateTemplate`. */
  slots?: TemplateSlot[];
  /** Kits whose pages depend on the answers (e.g. one form per ABI write function). */
  build?: (answers: SlotAnswers) => DappSpec;
  /** Page outline shown on the kit card ("Landing → Dashboard"). */
  outline?: string[];
};

/**
 * Placeholder addresses the kits use until the user pastes their own. The playground's test wallet
 * answers reads for them with sample data, so a fresh kit previews with real-looking numbers.
 */
export const DEMO_TOKEN_ADDRESS = `SQ:${'de'.repeat(32)}`;
export const DEMO_CONTRACT_ADDRESS = `SQ:${'c0'.repeat(32)}`;
export const DEMO_NFT_ADDRESS = `SQ:${'a7'.repeat(32)}`;

/** Id-safe segment for a page path (`/` → `home`, `/my-page` → `my-page`). Ids allow only [A-Za-z0-9_-]. */
function pathSeg(path: string): string {
  if (path === '/') return 'home';
  return path.replace(/^\//, '').replace(/[^A-Za-z0-9_-]+/g, '-').slice(0, 40) || 'page';
}

function page(path: string, title: string, blocks: DappBlock[], rootId?: string): DappPage {
  const root = wrapBlocks(blocks, rootId ?? `${pathSeg(path)}-root`);
  return { path, title, root, blocks };
}

// --- Layout helpers for kits (real sections and rows, not one flat stack) --------------------
const leaf = (id: string, widget: string, props: DappBlock['props']): LayoutNode => ({ kind: 'block', block: { id, widget, props } });
const section = (id: string, children: LayoutNode[], style: ContainerNode['style'] = {}): ContainerNode => ({
  kind: 'section',
  id,
  style: { maxWidth: '1120px', padding: '1.25rem', gap: '1.25rem', ...style },
  children,
});
const row = (id: string, children: LayoutNode[]): ContainerNode => ({ kind: 'row', id, style: { gap: '1rem', align: 'stretch' }, children });
const column = (id: string, children: LayoutNode[], width?: string): ContainerNode => ({
  kind: 'column',
  id,
  style: { gap: '1rem', ...(width ? { width } : {}) },
  children,
});

function treePage(path: string, title: string, root: ContainerNode, access?: PageAccess): DappPage {
  const blocks: DappBlock[] = [];
  const walk = (n: LayoutNode) => (n.kind === 'block' ? blocks.push(n.block) : n.children.forEach(walk));
  walk(root);
  return { path, title, root, blocks, ...(access && access !== 'public' ? { access } : {}) };
}

const TOKEN_CONTRACT = (address: string, name = 'Token'): ContractRef => ({
  id: 'token',
  name,
  address,
  kind: 'token',
  decimals: 18,
  functions: [],
});

const DEMO_APP_FUNCTIONS: ContractFunction[] = [
  { name: 'get', signature: 'get()', params: [], returns: 'uint256', kind: 'read' },
  { name: 'set', signature: 'set(uint256)', params: [{ name: 'value', type: 'uint256' }], kind: 'write' },
  { name: 'increment', signature: 'increment()', params: [], kind: 'write' },
];

const APP_CONTRACT = (address: string, functions: ContractFunction[] = DEMO_APP_FUNCTIONS, name = 'App contract'): ContractRef => ({
  id: 'app',
  name,
  address,
  kind: 'contract',
  functions,
});

/** Token project: public landing with live token data → wallet-only dashboard. */
function tokenProjectSpec(): DappSpec {
  const landing = section('landing-root', [
    leaf('hero', 'hero-gradient', {
      heading: 'Meet the Demo Token',
      text: 'A post-quantum token on Splendor. Live supply, clear tokenomics, one click to the app.',
      ctaLabel: 'Launch app',
      ctaPath: '/app',
    }),
    leaf('stats', 'token-stats', { title: 'Token at a glance', contractId: 'token', showSupply: true, showBalance: false, layout: 'row' }),
    row('landing-info', [
      column('landing-info-left', [
        leaf('tokenomics', 'tokenomics', {
          title: 'Tokenomics',
          contractId: 'token',
          chart: 'donut',
          items: 'Community|40|Airdrops and rewards\nTreasury|25|Grants and partnerships\nTeam|20|4-year vesting\nLiquidity|15|Exchange pools',
        }),
      ]),
      column('landing-info-right', [
        leaf('steps', 'steps', { title: 'How it works', items: 'Connect|Link your Splendor wallet\nHold|Keep DEMO in your wallet\nUse|Send tokens and use the app' }),
      ]),
    ]),
    leaf('features', 'feature-grid', {
      title: 'Why hold DEMO',
      items: 'Post-quantum|ML-DSA-65 signatures by default\nFast|Seconds to finality\nOpen|Every number on this page is read on-chain',
    }),
    leaf('faq', 'faq', {
      title: 'FAQ',
      items: 'Where does the supply come from?|It is read live from the token contract.\nDo I need a wallet?|Yes, to open the app. The landing page is public.',
    }),
    leaf('cta', 'cta-banner', { heading: 'Ready?', text: 'Connect your wallet and open the dashboard.', buttonLabel: 'Launch app', buttonPath: '/app' }),
    leaf('footer', 'footer-bar', { text: 'Built with the Splendor dApp Studio' }),
  ]);
  const dashboard = section('app-root', [
    leaf('app-hero', 'hero', { heading: 'Dashboard', text: 'Your balance, your token and your contract in one place.' }),
    row('app-top', [
      column('app-top-left', [leaf('account', 'account-card', { showBalance: true, showNetwork: true, showExplorer: true })]),
      column('app-top-right', [leaf('my-token', 'token-stats', { title: 'Your DEMO', contractId: 'token', showSupply: false, showBalance: true, layout: 'card' })]),
    ]),
    row('app-actions', [
      column('app-actions-left', [leaf('send-token', 'token-panel', { tokenAddress: DEMO_TOKEN_ADDRESS, defaultAmount: '1', showFee: true, toast: true })]),
      column('app-actions-right', [
        leaf('app-value', 'live-stat', { label: 'Current value', contractId: 'app', functionSig: 'get()', format: 'number', refreshMs: 0 }),
        leaf('app-set', 'contract-form', { title: 'Update the value', contractId: 'app', functionSig: 'set(uint256)', buttonLabel: 'Save on-chain', successMessage: 'Saved.' }),
      ]),
    ]),
    leaf('history', 'tx-history', { title: 'Recent transactions', limit: 10 }),
  ]);
  return {
    ...base('Token Project', 'A public landing page with live token data and a wallet-only dashboard.', [
      treePage('/', 'Home', landing),
      treePage('/app', 'Dashboard', dashboard, 'wallet'),
    ]),
    contracts: [TOKEN_CONTRACT(DEMO_TOKEN_ADDRESS, 'Demo Token'), APP_CONTRACT(DEMO_CONTRACT_ADDRESS)],
    nav: { brand: 'Demo Token', showConnect: true, links: ['/', '/app'], footer: '' },
  };
}

const MAX_KIT_READS = 4;
const MAX_KIT_FORMS = 6;
const label = (name: string) => name.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').replace(/^./, (c) => c.toUpperCase());

/** Contract app: landing with live reads → signed-in dashboard with one form per write function. */
function contractAppSpec(answers: SlotAnswers = {}): DappSpec {
  const a = answers.contract;
  const given = typeof a === 'object' && a ? a : typeof a === 'string' ? { address: a } : undefined;
  const functions = given?.functions?.length ? given.functions : DEMO_APP_FUNCTIONS;
  const contract = APP_CONTRACT(given?.address ?? DEMO_CONTRACT_ADDRESS, functions, given?.name?.trim() || 'My contract');
  const reads = functions.filter((f) => f.kind === 'read' && f.params.length === 0).slice(0, MAX_KIT_READS);
  const writes = functions.filter((f) => f.kind === 'write').slice(0, MAX_KIT_FORMS);
  const stat = (f: ContractFunction, i: number, prefix: string) =>
    leaf(`${prefix}-read-${i + 1}`, 'live-stat', { label: label(f.name), contractId: 'app', functionSig: f.signature, format: 'number', refreshMs: 0 });

  const landing = section('landing-root', [
    leaf('hero', 'hero-centered', {
      heading: contract.name,
      text: 'Live numbers straight from the contract. Sign in to take part.',
      ctaLabel: 'Open the app',
      ctaPath: '/app',
    }),
    ...(reads.length ? [row('landing-stats', reads.map((f, i) => column(`landing-stat-${i + 1}`, [stat(f, i, 'landing')])))] : []),
    leaf('features', 'feature-grid', {
      title: 'How it works',
      items: 'Read|Anyone can see the live numbers\nSign in|Prove your wallet with a free signature\nAct|Send transactions from the dashboard',
    }),
    leaf('cta', 'cta-banner', { heading: 'Take part', text: 'Sign in with your Splendor wallet.', buttonLabel: 'Open the app', buttonPath: '/app' }),
    leaf('footer', 'footer-bar', { text: 'Built with the Splendor dApp Studio' }),
  ]);
  const dashboard = section('app-root', [
    leaf('app-hero', 'hero', { heading: 'Dashboard', text: `Read and write ${contract.name}.` }),
    row('app-top', [column('app-top-left', [leaf('account', 'account-card', { showBalance: true, showNetwork: true })]), ...reads.map((f, i) => column(`app-stat-${i + 1}`, [stat(f, i, 'app')]))]),
    ...writes.map((f, i) =>
      leaf(`app-form-${i + 1}`, 'contract-form', {
        title: label(f.name),
        contractId: 'app',
        functionSig: f.signature,
        buttonLabel: label(f.name),
        successMessage: 'Transaction sent.',
      }),
    ),
    leaf('history', 'tx-history', { title: 'Recent transactions', limit: 10 }),
  ]);
  return {
    ...base(contract.name, 'A landing page with live contract data and a signed-in dashboard.', [
      treePage('/', 'Home', landing),
      treePage('/app', 'Dashboard', dashboard, 'signin'),
    ]),
    contracts: [contract],
    nav: { brand: contract.name, showConnect: true, links: ['/', '/app'], footer: '' },
  };
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

const TOKEN_SLOTS: TemplateSlot[] = [
  {
    id: 'token',
    kind: 'token',
    label: 'Your token address',
    help: 'The QRC-20 token your landing page shows. Leave the demo token to try it first.',
    placeholder: 'SQ:…',
    required: true,
    contractId: 'token',
    targets: [{ blockId: 'send-token', prop: 'tokenAddress' }],
  },
  {
    id: 'contract',
    kind: 'contract',
    label: 'An app contract (optional)',
    help: 'A contract the dashboard reads and writes, e.g. staking or a sale. Paste its address and ABI.',
    contractId: 'app',
    removeWhenSkipped: ['app-value', 'app-set'],
  },
];

export const TEMPLATES: readonly DappTemplate[] = [
  {
    id: 'token-project',
    title: 'Token project',
    description: 'Landing page with live supply and tokenomics, plus a wallet-only dashboard to hold, send and use your token.',
    setup: [],
    kit: true,
    outline: ['Landing (public)', 'Dashboard (wallet)'],
    slots: TOKEN_SLOTS,
    spec: tokenProjectSpec(),
  },
  {
    id: 'contract-app',
    title: 'Contract app',
    description: 'Landing page with live numbers from your contract, plus a signed-in dashboard with a form for every write function.',
    setup: [],
    kit: true,
    outline: ['Landing (public)', 'Dashboard (signed in)'],
    slots: [
      {
        id: 'contract',
        kind: 'contract',
        label: 'Your contract',
        help: 'Paste the address and the ABI (sqc-solc JSON or name(type) signatures). Leave the demo contract to try it first.',
        required: true,
        contractId: 'app',
      },
    ],
    build: contractAppSpec,
    spec: contractAppSpec(),
  },
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
    id: 'nft-membership',
    title: 'NFT membership',
    description: 'Public QRC-721 gallery, plus a wallet-only members page to send your passes, receive with QR and see your activity.',
    setup: [],
    outline: ['Gallery (public)', 'Members (wallet)'],
    spec: {
      ...base('NFT Membership', 'Hold a pass from the collection to join the members area.', [
        treePage(
          '/',
          'Gallery',
          section('home-root', [
            leaf('hero', 'hero-gradient', {
              heading: 'Members pass',
              text: 'Each QRC-721 pass in this collection opens the members area. Signed with post-quantum keys.',
              ctaLabel: 'Open members area',
              ctaPath: '/members',
            }),
            leaf('gallery', 'nft721-gallery', { title: 'The collection', collection: DEMO_NFT_ADDRESS, tokenIds: '1-12', showOwner: true }),
          ]),
        ),
        treePage(
          '/members',
          'Members',
          section('members-root', [
            leaf('members-hero', 'hero', { heading: 'Members area', text: 'Your passes, your address and your recent activity.' }),
            row('members-top', [
              column('members-left', [
                leaf('passes', 'nft-panel', { title: 'Your passes', collectionAddress: DEMO_NFT_ADDRESS, tokenIds: '1-12', showTransfer: true, showMint: true, toast: true }),
              ]),
              column('members-right', [leaf('receive', 'receive', { showAmount: true, qrSize: 176 }), leaf('contacts', 'address-book', { allowEdit: true })]),
            ]),
            leaf('activity', 'activity-feed', { blocks: 2000, limit: 25, showPending: true }),
          ]),
          'wallet',
        ),
      ]),
      nav: { brand: 'NFT Membership', showConnect: true, links: ['/', '/members'], footer: '' },
    },
  },
  {
    // Kept for old links (`/build?template=landing`): the Token project kit with the demo token.
    id: 'landing',
    title: 'Landing page',
    description: 'Landing page with live token data and a wallet-only dashboard (the Token project kit with a demo token).',
    setup: [],
    outline: ['Landing (public)', 'Dashboard (wallet)'],
    slots: TOKEN_SLOTS,
    spec: tokenProjectSpec(),
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
        { id: `${pathSeg(path)}-hero`, widget: 'hero-centered', props: { heading: title, text: 'Tell visitors what this page is for.', ctaLabel: 'Get started' } },
        { id: `${pathSeg(path)}-features`, widget: 'feature-grid', props: { title: 'Highlights', items: 'Fast|Ship in minutes,Secure|Post-quantum signatures,Open|Export real code' } },
        { id: `${pathSeg(path)}-cta`, widget: 'cta-banner', props: { heading: 'Next step', text: 'Connect a wallet and try it.', buttonLabel: 'Connect' } },
      ]),
  },
  {
    id: 'landing-linked',
    title: 'Landing → app',
    description: 'Public landing whose buttons open /app.',
    build: (path, title) => {
      const s = pathSeg(path);
      return treePage(
        path,
        title,
        section(`${s}-root`, [
          leaf(`${s}-hero`, 'hero-gradient', { heading: title, text: 'Say what your app does in one line.', ctaLabel: 'Launch app', ctaPath: '/app' }),
          leaf(`${s}-features`, 'feature-grid', { title: 'Highlights', items: 'Fast|Seconds to finality\nSecure|Post-quantum signatures\nOpen|Every number read on-chain' }),
          leaf(`${s}-cta`, 'cta-banner', { heading: 'Ready?', text: 'Connect your wallet and open the app.', buttonLabel: 'Launch app', buttonPath: '/app' }),
        ]),
      );
    },
  },
  {
    id: 'dashboard-wallet',
    title: 'Dashboard (wallet only)',
    description: 'Account, balance and activity; visitors connect first.',
    build: (path, title) => {
      const s = pathSeg(path);
      return treePage(
        path,
        title,
        section(`${s}-root`, [
          leaf(`${s}-hero`, 'hero', { heading: title, text: 'Your account at a glance.' }),
          row(`${s}-top`, [
            column(`${s}-left`, [leaf(`${s}-account`, 'account-card', { showBalance: true, showNetwork: true })]),
            column(`${s}-right`, [leaf(`${s}-history`, 'tx-history', { title: 'Recent transactions', limit: 10 })]),
          ]),
        ]),
        'wallet',
      );
    },
  },
  {
    id: 'dashboard',
    title: 'Dashboard',
    description: 'Account, balance and activity.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'Your account at a glance.' } },
        { id: `${pathSeg(path)}-account`, widget: 'account-card', props: { showBalance: true, showNetwork: true } },
        { id: `${pathSeg(path)}-pending`, widget: 'pending-tx', props: { emptyText: 'Nothing pending' } },
      ]),
  },
  {
    id: 'transactions',
    title: 'Transactions',
    description: 'Transfer form and pending list.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'Send SPLD and track pending txs.' } },
        { id: `${pathSeg(path)}-transfer`, widget: 'transfer', props: { defaultAmount: '0.01', showFee: true, toast: true } },
        { id: `${pathSeg(path)}-pending`, widget: 'pending-tx', props: { pollMs: 5000 } },
      ]),
  },
  {
    id: 'token',
    title: 'Token',
    description: 'Deploy or manage a QRC-20.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'Token tools.' } },
        { id: `${pathSeg(path)}-panel`, widget: 'token-panel', props: { defaultAmount: '1', showFee: true } },
      ]),
  },
  {
    id: 'nft',
    title: 'NFT',
    description: 'Mint and show collectibles.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'Your collection.' } },
        { id: `${pathSeg(path)}-drop`, widget: 'nft-drop', props: { title: 'Mint', collectionName: 'Collection', tokenId: '1', amount: '1' } },
      ]),
  },
  {
    id: 'members',
    title: 'Members',
    description: 'Sign-in gate and account card.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'Members only.' } },
        { id: `${pathSeg(path)}-members`, widget: 'members-area', props: { title: 'Members', statement: 'Sign in', content: 'Welcome.' } },
      ]),
  },
  {
    id: 'about',
    title: 'About',
    description: 'Heading, text and footer.',
    build: (path, title) =>
      page(path, title, [
        { id: `${pathSeg(path)}-hero`, widget: 'hero', props: { heading: title, text: 'About this project.' } },
        { id: `${pathSeg(path)}-text`, widget: 'rich-text', props: { text: 'Add your story here.' } },
        { id: `${pathSeg(path)}-footer`, widget: 'footer-bar', props: { text: 'Built on Splendor' } },
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

/**
 * A fresh spec from a template (never the shared constant). With `answers`, each slot fills its
 * contract in `spec.contracts` (so every block bound to it follows) and its raw `targets` props.
 * Kits with `build` regenerate their pages from the answers (e.g. one form per ABI write function).
 */
export function instantiateTemplate(t: DappTemplate, answers: SlotAnswers = {}): DappSpec {
  const answered = Object.keys(answers).length > 0;
  let spec = JSON.parse(JSON.stringify(t.build && answered ? t.build(answers) : t.spec)) as DappSpec;
  for (const slot of t.slots ?? []) {
    const answer = answers[slot.id];
    if (answer == null || answer === '' || (typeof answer === 'object' && !answer.address.trim())) {
      if (answered && slot.removeWhenSkipped?.length) spec = withoutSlot(spec, slot);
      continue;
    }
    const address = typeof answer === 'string' ? answer.trim() : answer.address.trim();
    if (slot.contractId) {
      const contracts = spec.contracts ?? (spec.contracts = []);
      let c = contracts.find((x) => x.id === slot.contractId);
      if (!c) {
        c = { id: slot.contractId, name: slot.label, address: '', functions: [], kind: slot.kind === 'token' ? 'token' : 'contract' };
        contracts.push(c);
      }
      c.address = address;
      if (typeof answer === 'object') {
        if (answer.name?.trim()) c.name = answer.name.trim().slice(0, 80);
        if (answer.functions?.length) c.functions = answer.functions;
      }
    }
    for (const target of slot.targets ?? []) {
      spec.pages = spec.pages.map((p) => {
        const root = mapTree(p.root, (n) =>
          n.kind === 'block' && n.block.id === target.blockId ? { ...n, block: { ...n.block, props: { ...n.block.props, [target.prop]: address } } } : n,
        );
        const blocks: DappBlock[] = [];
        const walk = (n: LayoutNode) => (n.kind === 'block' ? blocks.push(n.block) : n.children.forEach(walk));
        walk(root);
        return { ...p, root, blocks };
      });
    }
  }
  return spec;
}

/** Drop a skipped slot's blocks, any container they leave empty, and its contract. */
function withoutSlot(spec: DappSpec, slot: TemplateSlot): DappSpec {
  const drop = new Set(slot.removeWhenSkipped);
  const prune = (n: LayoutNode): LayoutNode | null => {
    if (n.kind === 'block') return drop.has(n.block.id) ? null : n;
    const children = n.children.map(prune).filter((c): c is LayoutNode => c !== null);
    return children.length || !n.children.length ? { ...n, children } : null;
  };
  return {
    ...spec,
    contracts: (spec.contracts ?? []).filter((c) => c.id !== slot.contractId),
    pages: spec.pages.map((p) => {
      const root = (prune(p.root) ?? { ...(p.root as ContainerNode), children: [] }) as ContainerNode;
      const blocks: DappBlock[] = [];
      const walk = (n: LayoutNode) => (n.kind === 'block' ? blocks.push(n.block) : n.children.forEach(walk));
      walk(root);
      return { ...p, root, blocks };
    }),
  };
}

/** Templates offered first as ready-made app kits (landing + dashboard). */
export function kitTemplates(): DappTemplate[] {
  return TEMPLATES.filter((t) => t.kit);
}
