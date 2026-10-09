import { DAPP_SPEC_VERSION, type DappSpec } from './types.js';

export type DappTemplate = {
  id: string;
  title: string;
  description: string;
  /** Short list of what the user must fill in before the spec validates (e.g. a recipient). */
  setup: { blockId: string; prop: string; label: string }[];
  spec: DappSpec;
};

const base = (name: string, description: string, pages: DappSpec['pages']): DappSpec => ({
  version: DAPP_SPEC_VERSION,
  name,
  description,
  network: 'testnet',
  theme: { base: 'splendor', overrides: {} },
  pages,
});

export const TEMPLATES: readonly DappTemplate[] = [
  {
    id: 'wallet-dashboard',
    title: 'Wallet dashboard',
    description: 'Account, balance, SPLD transfers and pending transactions — the smallest useful dApp.',
    setup: [],
    spec: base('Wallet Dashboard', 'Check your SPLD balance and send transfers on Splendor.', [
      {
        path: '/',
        title: 'Dashboard',
        blocks: [
          { id: 'hero', widget: 'hero', props: { heading: 'Wallet dashboard', text: 'Your Splendor account at a glance.' } },
          { id: 'account', widget: 'account-card', props: { showBalance: true, showNetwork: true, showExplorer: true, showDisconnect: true } },
          { id: 'send', widget: 'transfer', props: { defaultAmount: '0.01', showFee: true, toast: true } },
          { id: 'pending', widget: 'pending-tx', props: { emptyText: 'Nothing pending', pollMs: 5000 } },
        ],
      },
    ]),
  },
  {
    id: 'token-launchpad',
    title: 'Token launchpad',
    description: 'Launch a QRC-20 token, then manage balances and transfers on a second page.',
    setup: [],
    spec: base('Token Launchpad', 'Create and manage QRC-20 tokens on Splendor.', [
      {
        path: '/',
        title: 'Launch',
        blocks: [
          { id: 'hero', widget: 'hero', props: { heading: 'Launch your token', text: 'Deploy a QRC-20 token in one transaction.' } },
          { id: 'deploy', widget: 'deploy-token', props: { defaultName: 'My Token', defaultSymbol: 'MYT', defaultSupply: '1000000', decimals: 18, review: true, showFee: true, toast: true } },
          { id: 'account', widget: 'account-card', props: { showBalance: true } },
        ],
      },
      {
        path: '/manage',
        title: 'Manage',
        blocks: [
          { id: 'token', widget: 'token-panel', props: { defaultAmount: '1', showFee: true, toast: true } },
          { id: 'pending', widget: 'pending-tx', props: { pollMs: 5000 } },
        ],
      },
    ]),
  },
  {
    id: 'tip-jar',
    title: 'Pay / tip jar',
    description: 'Accept SPLD tips to a fixed address, with the real network fee shown before signing.',
    setup: [{ blockId: 'tips', prop: 'recipient', label: 'Address that receives tips' }],
    spec: base('Tip Jar', 'Support this project with SPLD.', [
      {
        path: '/',
        title: 'Tip',
        blocks: [
          { id: 'hero', widget: 'hero', props: { heading: 'Buy me a coffee', text: 'Tips go straight to my Splendor address.' } },
          { id: 'tips', widget: 'tip-jar', props: { title: 'Send a tip', recipient: '', amount: '0.1' } },
          { id: 'summary', widget: 'balance-summary', props: { title: 'Your wallet' } },
        ],
      },
    ]),
  },
  {
    id: 'members-area',
    title: 'Members area',
    description: 'Wallet sign-in that unlocks members-only content; verify on your server with verifySignIn.',
    setup: [],
    spec: base('Members Club', 'Sign in with your Splendor wallet to see members-only content.', [
      {
        path: '/',
        title: 'Home',
        blocks: [
          { id: 'hero', widget: 'hero', props: { heading: 'Members club', text: 'Sign in with your wallet — no passwords, no email.' } },
          {
            id: 'members',
            widget: 'members-area',
            props: {
              title: 'Members area',
              statement: 'Sign in to the members club',
              content: 'Welcome back! Here is this week\'s members-only update.',
            },
          },
          { id: 'account', widget: 'account-card', props: { showBalance: false } },
        ],
      },
    ]),
  },
  {
    id: 'nft-drop',
    title: 'NFT drop',
    description: 'Deploy a QRC-1155 collection (or use one) and let visitors mint and see what they own.',
    setup: [],
    spec: base('NFT Drop', 'Mint a Splendor collectible.', [
      {
        path: '/',
        title: 'Drop',
        blocks: [
          { id: 'hero', widget: 'hero', props: { heading: 'Genesis drop', text: 'Mint your collectible on Splendor Quantum Chain.' } },
          { id: 'drop', widget: 'nft-drop', props: { title: 'Mint', collectionName: 'Genesis', tokenId: '1', amount: '1' } },
          { id: 'pending', widget: 'pending-tx', props: { pollMs: 5000 } },
        ],
      },
    ]),
  },
];

export function getTemplate(id: string): DappTemplate | undefined {
  return TEMPLATES.find((t) => t.id === id);
}

/** Blank spec for "start from scratch". */
export function emptySpec(name = 'My Splendor dApp'): DappSpec {
  return base(name, '', [{ path: '/', title: 'Home', blocks: [{ id: 'hero', widget: 'hero', props: { heading: name, text: '' } }] }]);
}

/** Deep copy of a template spec so editors never mutate the shared constant. */
export function instantiateTemplate(t: DappTemplate): DappSpec {
  return JSON.parse(JSON.stringify(t.spec)) as DappSpec;
}
