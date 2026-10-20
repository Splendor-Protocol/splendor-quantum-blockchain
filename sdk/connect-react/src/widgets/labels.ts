import { useContext } from 'react';
import { BrandContext } from '../react/context.js';

/** Text shared by every widget and part. `{name}` placeholders are filled by `formatLabel`. */
export const COMMON_LABELS = {
  connect: 'Connect',
  unlock: 'Unlock',
  lockedHint: 'Wallet locked — unlock to continue.',
  disconnectedHint: 'Connect a wallet to continue.',
  review: 'Review',
  confirmSend: 'Confirm send',
  sending: 'Sending…',
  back: 'Back',
  max: 'Max',
  copy: 'Copy',
  copied: 'Copied',
  explorer: 'Explorer',
  reviewTo: 'To',
  reviewFrom: 'From',
  reviewAmount: 'Amount',
  reviewNetwork: 'Network',
  reviewContract: 'Token contract',
  reviewFee: 'Network fee',
  reviewAria: 'Review transfer',
  feeEstimating: 'Estimating…',
  feeUnavailable: 'Unavailable',
  feeFloor: 'at least {fee} SPLD',
  willRevert: 'This transaction is expected to fail: {reason}',
  wrongNetwork: 'Your wallet is on a different network than this app — switch networks to continue.',
  tooPrecise: 'Use at most {decimals} decimal places',
  recent: 'Recent:',
  recentRecipients: 'Recent recipients',
  submitted: 'Submitted',
  confirmed: 'Confirmed',
  invalidAddress: 'Enter a valid SQ: address (0x and raw hex are not accepted)',
  selfSend: 'You cannot send to your own address',
  badAmount: 'Enter a valid amount greater than zero',
  insufficient: 'Amount exceeds your {symbol} balance',
  insufficientWithFee: 'Amount plus the network fee exceeds your SPLD balance',
  feeUnaffordable: 'Not enough SPLD to pay the network fee',
};

export const TRANSFER_LABELS = {
  ...COMMON_LABELS,
  title: 'Transfer SPLD',
  lockedHint: 'Wallet locked — unlock to send.',
  disconnectedHint: 'Connect a wallet to send SPLD.',
  toLabel: 'To (SQ:…)',
  toPlaceholder: 'SQ:…',
  amountLabel: 'Amount (SPLD)',
  balance: 'Balance: {balance} {symbol}',
  toastPending: 'Sending SPLD',
  toastSuccess: 'SPLD sent',
  toastError: 'Transfer failed',
};

export const TOKEN_LABELS = {
  ...COMMON_LABELS,
  title: 'Token',
  lockedHint: 'Wallet locked — unlock to manage this token.',
  disconnectedHint: 'Connect a wallet to manage this token.',
  tokenLabel: 'Token (SQ:…)',
  tokenPlaceholder: 'SQ:…',
  invalidToken: 'Enter a valid SQ: token address',
  enterToken: 'Enter a QRC-20 token address.',
  recentTokens: 'Your tokens:',
  loading: 'Loading…',
  loadFailed: 'Could not load this token: {error}',
  toLabel: 'To (SQ:…)',
  toPlaceholder: 'SQ:…',
  amountLabel: 'Amount (human, e.g. 1.5)',
  balance: 'Balance: {balance} {symbol}',
  toastPending: 'Sending {symbol}',
  toastSuccess: '{symbol} sent',
  toastError: 'Token transfer failed',
};

export const DEPLOY_TOKEN_LABELS = {
  ...COMMON_LABELS,
  title: 'Deploy QRC-20',
  lockedHint: 'Wallet locked — unlock to deploy.',
  disconnectedHint: 'Connect a wallet to deploy a token.',
  nameLabel: 'Name',
  symbolLabel: 'Symbol',
  supplyLabel: 'Total supply (human)',
  maxSupplyLabel: 'Max supply',
  decimalsLabel: 'Decimals',
  invalidSupply: 'Enter a valid supply greater than zero',
  supplyOverMax: 'Total supply cannot exceed the max supply ({max})',
  invalidDecimals: 'Decimals must be a whole number from 0 to 18',
  invalidName: 'Name must be 1–{max} characters',
  invalidSymbol: 'Symbol must be 1–{max} letters or digits',
  reviewDeploy: 'Review deploy',
  confirmDeploy: 'Confirm deploy',
  reviewDeployAria: 'Review token deploy',
  deploy: 'Deploy',
  deploying: 'Deploying…',
  tokenAddress: 'Token',
  toastPending: 'Deploying {symbol}',
  toastSuccess: '{symbol} deployed',
  toastError: 'Deploy failed',
};

export const ACCOUNT_LABELS = {
  ...COMMON_LABELS,
  title: 'Account',
  disconnectedHint: 'Connect a wallet to view your SQ: identity.',
  addressLabel: 'SQ address',
  pqBadge: 'Post-quantum',
  pqCheck: 'Check key origin',
  balanceLabel: 'Balance',
  refresh: 'Refresh',
  disconnect: 'Disconnect',
};

export const SIGN_IN_LABELS = {
  ...COMMON_LABELS,
  title: 'Sign in',
  lockedHint: 'Wallet locked — unlock to sign in.',
  disconnectedHint: 'Connect a wallet to sign in.',
  button: 'Sign in',
  waiting: 'Waiting for signature…',
  signedInAs: 'Signed in as {address}',
  nonceFailed: 'Could not get a sign-in challenge — try again',
  thisSite: 'this site',
};

export type CommonWidgetLabels = typeof COMMON_LABELS;
export type TransferWidgetLabels = typeof TRANSFER_LABELS;
export type TokenPanelLabels = typeof TOKEN_LABELS;
export type DeployTokenWidgetLabels = typeof DEPLOY_TOKEN_LABELS;
export type AccountCardLabels = typeof ACCOUNT_LABELS;
export type SignInButtonLabels = typeof SIGN_IN_LABELS;

/** Provider-wide overrides: `common` applies to every widget, the rest to one widget each. */
export type WidgetLabelOverrides = {
  common?: Partial<CommonWidgetLabels>;
  transfer?: Partial<TransferWidgetLabels>;
  token?: Partial<TokenPanelLabels>;
  deploy?: Partial<DeployTokenWidgetLabels>;
  account?: Partial<AccountCardLabels>;
  signIn?: Partial<SignInButtonLabels>;
};

export type WidgetKind = Exclude<keyof WidgetLabelOverrides, 'common'>;

/** Replace `{name}` placeholders; unknown names are left as-is. */
export function formatLabel(template: string, vars: Record<string, string | number | null | undefined>): string {
  return template.replace(/\{(\w+)\}/g, (m, k: string) => {
    const v = vars[k];
    return v == null ? m : String(v);
  });
}

function assignDefined<T extends Record<string, string>>(
  target: T,
  source: Partial<Record<string, string | undefined>> | undefined,
  onlyKnown: boolean,
): void {
  if (!source) return;
  for (const [k, v] of Object.entries(source)) {
    if (v === undefined) continue;
    if (onlyKnown && !(k in target)) continue;
    (target as Record<string, string>)[k] = v;
  }
}

/** Merge widget text: built-in defaults, then provider `labels.widgets.common`, then `labels.widgets[kind]`, then `overrides`. */
export function mergeWidgetLabels<T extends Record<string, string>>(
  defaults: T,
  provider: WidgetLabelOverrides | undefined,
  kind: WidgetKind | null,
  overrides?: Partial<T>,
): T {
  const out = { ...defaults };
  assignDefined(out, provider?.common, true);
  if (kind) assignDefined(out, provider?.[kind] as Record<string, string> | undefined, true);
  assignDefined(out, overrides as Record<string, string> | undefined, false);
  return out;
}

/** Labels for a widget or part, honouring provider-level `labels.widgets` overrides. */
export function useWidgetLabels<T extends Record<string, string>>(
  defaults: T,
  overrides?: Partial<T>,
  kind: WidgetKind | null = null,
): T {
  const brand = useContext(BrandContext);
  return mergeWidgetLabels(defaults, brand.widgets, kind, overrides);
}
