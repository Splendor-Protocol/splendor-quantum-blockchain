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
