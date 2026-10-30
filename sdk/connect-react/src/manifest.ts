/**
 * Widget manifest — machine-readable description of every drop-in widget and placeable part.
 *
 *   import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
 *
 * Data only: no React, no side effects. The dApp Playground builds its catalog, prop forms and
 * generated code from this list, so a new widget shows up there by adding an entry here.
 */

/** Bumped when the shape of `WidgetManifest` / `PropSchema` changes incompatibly. */
export const MANIFEST_SCHEMA_VERSION = 1 as const;

export type PropType = 'string' | 'number' | 'boolean' | 'select';

/** One configurable prop. Only serializable props are listed (no callbacks or nodes). */
export type PropSchema = {
  name: string;
  label: string;
  type: PropType;
  description?: string;
  /** Input hint for `string` props. */
  format?: 'address' | 'amount' | 'text' | 'url';
  placeholder?: string;
  required?: boolean;
  min?: number;
  max?: number;
  options?: { value: string; label: string }[];
};

export type WidgetCategory = 'wallet' | 'tokens' | 'nft' | 'auth' | 'modules' | 'activity' | (string & {});

export type WidgetManifest = {
  /** Stable id used in saved dApp specs. Never rename; add a new id instead. */
  id: string;
  title: string;
  description: string;
  category: WidgetCategory;
  /** `widget`: full drop-in panel. `part`: building block for custom widgets. */
  kind: 'widget' | 'part';
  /** Named export to import. */
  importName: string;
  package: string;
  /** Package version that first shipped this export. */
  since: string;
  status: 'stable' | 'beta' | 'experimental';
  /** False for parts that need runtime data (e.g. `FeeRow` needs an estimate) and cannot be placed alone. */
  placeable: boolean;
  props: PropSchema[];
  defaults: Record<string, unknown>;
  /** Callback props the widget fires (for generated code and docs). */
  events?: string[];
  requires?: { wallet?: boolean; modules?: string[]; networks?: string[] };
  /** Path in the package docs, e.g. `docs/GUIDE.md#9-drop-in-widgets`. */
  docs?: string;
};

const PACKAGE = '@splendor/connect-react';
const GUIDE_WIDGETS = 'docs/GUIDE.md#9-drop-in-widgets';

/** Base props shared by action widgets (`BaseWidgetProps`). */
const BASE_PROPS: PropSchema[] = [
  { name: 'title', label: 'Title', type: 'string', format: 'text', description: 'Replaces the default heading.' },
  { name: 'hideTitle', label: 'Hide title', type: 'boolean' },
];
const TOAST_PROP: PropSchema = {
  name: 'toast',
  label: 'Toasts',
  type: 'boolean',
  description: 'Push pending / success / error toasts when a <TxToastHost> is mounted.',
};
const SHOW_FEE_PROP: PropSchema = {
  name: 'showFee',
  label: 'Show network fee',
  type: 'boolean',
  description: 'Estimate the fee for the real call and block Confirm when it cannot be paid.',
};
const ACTION_EVENTS = ['onSuccess', 'onError', 'onStatusChange'];

export const WIDGET_MANIFESTS: readonly WidgetManifest[] = [
  {
    id: 'account-card',
    title: 'Account card',
    description: 'Connected address, SPLD balance, network and explorer link with copy and disconnect.',
    category: 'wallet',
    kind: 'widget',
    importName: 'AccountCard',
    package: PACKAGE,
    since: '2.0.0',
    status: 'stable',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'showBalance', label: 'Show balance', type: 'boolean' },
      { name: 'showNetwork', label: 'Show network', type: 'boolean' },
      { name: 'showExplorer', label: 'Show explorer link', type: 'boolean' },
      { name: 'showDisconnect', label: 'Show disconnect', type: 'boolean' },
    ],
    defaults: { showBalance: true, showNetwork: true, showExplorer: true, showDisconnect: true },
    events: ['onCopy', 'onDisconnect'],
    requires: { wallet: true },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'transfer',
    title: 'SPLD transfer',
    description: 'Send native SPLD with recipient checks, a review step and the real network fee.',
    category: 'wallet',
    kind: 'widget',
    importName: 'TransferWidget',
    package: PACKAGE,
    since: '2.0.0',
    status: 'stable',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'defaultTo', label: 'Default recipient', type: 'string', format: 'address', placeholder: 'SQ:…' },
      { name: 'defaultAmount', label: 'Default amount (SPLD)', type: 'string', format: 'amount', placeholder: '0.01' },
      SHOW_FEE_PROP,
      TOAST_PROP,
    ],
    defaults: { defaultAmount: '0.01', showFee: true, toast: true },
    events: ACTION_EVENTS,
    requires: { wallet: true },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'token-panel',
    title: 'QRC-20 token panel',
    description: 'Token balance and transfer for one QRC-20, or an address field when none is set.',
    category: 'tokens',
    kind: 'widget',
    importName: 'TokenPanel',
    package: PACKAGE,
    since: '2.0.0',
    status: 'stable',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'tokenAddress', label: 'Token address', type: 'string', format: 'address', placeholder: 'SQ:…' },
      { name: 'defaultAmount', label: 'Default amount', type: 'string', format: 'amount', placeholder: '1' },
      SHOW_FEE_PROP,
      TOAST_PROP,
    ],
    defaults: { defaultAmount: '1', showFee: true, toast: true },
    events: ACTION_EVENTS,
    requires: { wallet: true, modules: ['qrc20'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'deploy-token',
    title: 'Deploy QRC-20 token',
    description: 'Form to launch a QRC-20 token with name, symbol, supply and a fee-checked review.',
    category: 'tokens',
    kind: 'widget',
    importName: 'DeployTokenWidget',
    package: PACKAGE,
    since: '2.0.0',
    status: 'stable',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'defaultName', label: 'Default name', type: 'string', format: 'text', placeholder: 'My Token' },
      { name: 'defaultSymbol', label: 'Default symbol', type: 'string', format: 'text', placeholder: 'MYT' },
      { name: 'defaultSupply', label: 'Default supply (whole tokens)', type: 'string', format: 'amount' },
      { name: 'decimals', label: 'Decimals', type: 'number', min: 0, max: 18 },
      { name: 'showDecimals', label: 'Editable decimals', type: 'boolean' },
      { name: 'maxSupply', label: 'Max supply (optional)', type: 'string', format: 'amount' },
      { name: 'review', label: 'Review before signing', type: 'boolean' },
      SHOW_FEE_PROP,
      TOAST_PROP,
    ],
    defaults: { defaultSupply: '1000000', decimals: 18, review: true, showFee: true, toast: true },
    events: ACTION_EVENTS,
    requires: { wallet: true, modules: ['qrc20'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'sign-in',
    title: 'Sign in with wallet',
    description: 'ML-DSA-65 sign-in challenge bound to this site, for servers that verify with verifySignIn.',
    category: 'auth',
    kind: 'widget',
    importName: 'SignInButton',
    package: PACKAGE,
    since: '2.0.0',
    status: 'stable',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'statement', label: 'Statement', type: 'string', format: 'text', placeholder: 'Sign in to Example' },
      { name: 'label', label: 'Button text', type: 'string', format: 'text' },
      { name: 'expiresInSec', label: 'Challenge lifetime (s)', type: 'number', min: 30, max: 3600 },
    ],
    defaults: { expiresInSec: 300 },
    events: ['onSuccess', 'onError', 'onStatusChange'],
    requires: { wallet: true },
    docs: 'docs/GUIDE.md#8-sign-in-with-the-wallet',
  },
  {
    id: 'nft-panel',
    title: 'QRC-721 NFT panel',
    description: 'Collection summary, token grid with ownership, send for owned NFTs and a mint form for the collection owner.',
    category: 'nft',
    kind: 'widget',
    importName: 'NftPanel',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'collectionAddress', label: 'Collection address', type: 'string', format: 'address', placeholder: 'SQ:…' },
      {
        name: 'tokenIds',
        label: 'Token ids',
        type: 'string',
        format: 'text',
        placeholder: '1-12',
        description: 'Ids to show, e.g. "1-12" or "1,5,9" (at most 60). QRC-721 has no enumeration, so the panel checks these ids.',
      },
      { name: 'showTransfer', label: 'Allow sending', type: 'boolean' },
      { name: 'showMint', label: 'Mint form for the owner', type: 'boolean' },
      TOAST_PROP,
    ],
    defaults: { tokenIds: '1-12', showTransfer: true, showMint: true, toast: true },
    events: ACTION_EVENTS,
    requires: { wallet: true, modules: ['qrc721'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'bridge-claim',
    title: 'Bridge status and claims',
    description: 'Bridge pause state, threshold, limits and totals, a burn-id lookup, and an opt-in operator claim form.',
    category: 'modules',
    kind: 'widget',
    importName: 'BridgeClaimWidget',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'bridgeAddress', label: 'Bridge address', type: 'string', format: 'address', placeholder: 'SQ:…', required: true },
      { name: 'showCheck', label: 'Burn-id lookup', type: 'boolean' },
      {
        name: 'allowClaim',
        label: 'Operator claim form',
        type: 'boolean',
        description: 'Only bridge operators can submit claims with relayer attestations. Leave off for public pages.',
      },
      TOAST_PROP,
    ],
    defaults: { showCheck: true, allowClaim: false, toast: true },
    events: ACTION_EVENTS,
    requires: { wallet: false, modules: ['bridge'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'validator-list',
    title: 'Validator directory',
    description: 'Active validators with bond and join height, plus an admission-approval lookup. Read-only, no wallet needed.',
    category: 'modules',
    kind: 'widget',
    importName: 'ValidatorList',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'showAdmissionCheck', label: 'Admission lookup', type: 'boolean' },
      { name: 'maxRows', label: 'Rows', type: 'number', min: 1, max: 200 },
      { name: 'pollMs', label: 'Refresh (ms)', type: 'number', min: 5000, max: 600000 },
    ],
    defaults: { showAdmissionCheck: true, maxRows: 50, pollMs: 30000 },
    requires: { wallet: false, modules: ['validators'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'activity-feed',
    title: 'Activity feed',
    description: 'Recent QRC-20 and QRC-1155 transfers for the connected account from chain logs, with pending sends on top.',
    category: 'activity',
    kind: 'widget',
    importName: 'ActivityFeed',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'blocks', label: 'Blocks to scan', type: 'number', min: 10, max: 2000, description: 'The node serves at most 2000 blocks per query.' },
      { name: 'limit', label: 'Rows', type: 'number', min: 1, max: 100 },
      { name: 'showPending', label: 'Show pending sends', type: 'boolean' },
      {
        name: 'indexerUrl',
        label: 'Indexer URL (optional)',
        type: 'string',
        format: 'url',
        placeholder: 'https://indexer.example',
        description: 'Serves /idx/v1/logs for history beyond 2000 blocks. https only (http allowed for localhost).',
      },
    ],
    defaults: { blocks: 2000, limit: 25, showPending: true },
    requires: { wallet: true },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'receive',
    title: 'Receive with QR',
    description: 'Your SQ: address with copy and a QR code; an optional amount makes it a splendor: payment link.',
    category: 'wallet',
    kind: 'widget',
    importName: 'ReceiveWidget',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      { name: 'showAmount', label: 'Request an amount', type: 'boolean' },
      { name: 'defaultAmount', label: 'Default amount (SPLD)', type: 'string', format: 'amount', placeholder: '1' },
      { name: 'qrSize', label: 'QR size (px)', type: 'number', min: 96, max: 512 },
    ],
    defaults: { showAmount: true, qrSize: 176 },
    events: ['onCopy'],
    requires: { wallet: true },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'address-book',
    title: 'Address book',
    description: 'Labelled SQ: contacts per network, saved in this browser. Transfer widgets offer them as quick picks.',
    category: 'wallet',
    kind: 'widget',
    importName: 'AddressBook',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [...BASE_PROPS, { name: 'allowEdit', label: 'Allow add / remove', type: 'boolean' }],
    defaults: { allowEdit: true },
    events: ['onSelect'],
    requires: { wallet: false },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'token-balances',
    title: 'Token balances',
    description: 'SPLD and a list of QRC-20 balances for the connected account.',
    category: 'tokens',
    kind: 'widget',
    importName: 'TokenBalances',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      ...BASE_PROPS,
      {
        name: 'tokens',
        label: 'Token addresses',
        type: 'string',
        format: 'text',
        placeholder: 'SQ:…, SQ:…',
        description: 'Comma-separated QRC-20 addresses (at most 20).',
      },
      { name: 'showNative', label: 'Show SPLD', type: 'boolean' },
      { name: 'hideZero', label: 'Hide zero balances', type: 'boolean' },
    ],
    defaults: { showNative: true, hideZero: false },
    requires: { wallet: true, modules: ['qrc20'] },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'qr-code',
    title: 'QR code',
    description: 'Any text or link as a scannable QR code (inline SVG, no external service).',
    category: 'wallet',
    kind: 'part',
    importName: 'QrCode',
    package: PACKAGE,
    since: '3.1.0',
    status: 'beta',
    placeable: true,
    props: [
      { name: 'value', label: 'Text or link', type: 'string', format: 'text', required: true, placeholder: 'https://…' },
      { name: 'size', label: 'Size (px)', type: 'number', min: 64, max: 512 },
      {
        name: 'ecc',
        label: 'Error correction',
        type: 'select',
        options: [
          { value: 'L', label: 'Low (7%)' },
          { value: 'M', label: 'Medium (15%)' },
          { value: 'Q', label: 'Quartile (25%)' },
          { value: 'H', label: 'High (30%)' },
        ],
      },
    ],
    defaults: { value: 'https://splendor.org', size: 176, ecc: 'M' },
    requires: { wallet: false },
    docs: GUIDE_WIDGETS,
  },
  {
    id: 'pending-tx',
    title: 'Pending transactions',
    description: 'Unconfirmed transactions on the current network, kept across reloads.',
    category: 'activity',
    kind: 'part',
    importName: 'PendingTxList',
    package: PACKAGE,
    since: '3.0.0',
    status: 'stable',
    placeable: true,
    props: [
      { name: 'emptyText', label: 'Empty text', type: 'string', format: 'text', placeholder: 'Nothing pending' },
      { name: 'pollMs', label: 'Poll interval (ms)', type: 'number', min: 1000, max: 60000 },
    ],
    defaults: { emptyText: 'Nothing pending', pollMs: 5000 },
    requires: { wallet: false },
    docs: 'docs/GUIDE.md#13-pending-transactions-across-reloads',
  },
  {
    id: 'fee-row',
    title: 'Fee row',
    description: 'Network fee line for a useEstimateFee result; warns when the node predicts a revert.',
    category: 'activity',
    kind: 'part',
    importName: 'FeeRow',
    package: PACKAGE,
    since: '3.0.0',
    status: 'stable',
    placeable: false,
    props: [],
    defaults: {},
    docs: 'docs/GUIDE.md#12-show-the-fee-before-signing',
  },
];

/** Manifest by id, or undefined. */
export function getWidgetManifest(id: string): WidgetManifest | undefined {
  return WIDGET_MANIFESTS.find((m) => m.id === id);
}
