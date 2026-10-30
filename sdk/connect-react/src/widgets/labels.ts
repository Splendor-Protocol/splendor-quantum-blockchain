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
  contacts: 'Contacts:',
  loading: 'Loading…',
  refresh: 'Refresh',
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

export const NFT_LABELS = {
  ...COMMON_LABELS,
  title: 'NFT collection',
  lockedHint: 'Wallet locked — unlock to manage your NFTs.',
  disconnectedHint: 'Connect a wallet to see and send your NFTs.',
  collectionLabel: 'Collection (SQ:…)',
  invalidCollection: 'Enter a valid SQ: collection address',
  enterCollection: 'Enter a QRC-721 collection address.',
  summary: '{name} ({symbol}) · {supply} minted',
  owned: 'You own {count}',
  yours: 'Yours',
  empty: 'No tokens in this range.',
  tokenLabel: 'Token #{id}',
  unminted: 'Not minted',
  selectToken: 'Choose one of your NFTs to send',
  toLabel: 'To (SQ:…)',
  toPlaceholder: 'SQ:…',
  send: 'Send NFT',
  mintTitle: 'Mint (collection owner)',
  mintId: 'Token id',
  mintTo: 'Mint to (SQ:…)',
  mintUri: 'Metadata URI (optional)',
  mint: 'Mint',
  invalidId: 'Token id must be a whole number',
  alreadyMinted: 'Token #{id} is already minted',
  toastPending: 'Sending NFT #{id}',
  toastSuccess: 'NFT #{id} sent',
  toastError: 'NFT transfer failed',
  toastMintPending: 'Minting NFT #{id}',
  toastMintSuccess: 'NFT #{id} minted',
  toastMintError: 'Mint failed',
};

export const BRIDGE_LABELS = {
  ...COMMON_LABELS,
  title: 'Bridge',
  lockedHint: 'Wallet locked — unlock to submit a claim.',
  disconnectedHint: 'Connect a wallet to submit a claim.',
  enterBridge: 'Set a bridge address to show its status.',
  status: 'Status',
  active: 'Active',
  paused: 'Paused',
  threshold: 'Attestations required',
  evmSource: 'Source chain',
  maxClaim: 'Max per claim',
  pool: 'SPLD pool',
  claimedTotals: 'Claimed so far',
  checkTitle: 'Check a burn',
  burnId: 'Burn id',
  check: 'Check',
  claimedAt: 'Burn #{id} was claimed at block {height}.',
  notClaimed: 'Burn #{id} has not been claimed yet.',
  invalidBurnId: 'Burn ids are whole numbers starting at 1',
  claimTitle: 'Submit claim (operator)',
  asset: 'Asset',
  amount: 'Amount (base units)',
  recipient: 'Recipient (SQ:…)',
  evmTx: 'EVM burn tx hash',
  attestations: 'Attestations (JSON array of { pubkey, signature })',
  invalidAttestations: 'Attestations must be a non-empty JSON array of { pubkey, signature }',
  invalidAmount: 'Amount must be a whole number of base units',
  submitClaim: 'Submit claim',
  toastPending: 'Submitting bridge claim',
  toastSuccess: 'Bridge claim submitted',
  toastError: 'Bridge claim failed',
};

export const VALIDATOR_LABELS = {
  ...COMMON_LABELS,
  title: 'Validators',
  count: '{count} active · bond {bond} SPLD',
  countNoBond: '{count} active',
  empty: 'No active validators reported.',
  address: 'Validator',
  bond: 'Bond',
  since: 'Since block',
  status: 'Status',
  active: 'Active',
  exiting: 'Exiting',
  inactive: 'Inactive',
  you: 'You',
  checkTitle: 'Admission check',
  checkLabel: 'Address (SQ:…)',
  check: 'Check',
  approved: 'This address holds an admission approval.',
  notApproved: 'This address has no admission approval.',
  loadFailed: 'Could not load validators: {error}',
};

export const ACTIVITY_LABELS = {
  ...COMMON_LABELS,
  title: 'Activity',
  lockedHint: 'Wallet locked — unlock to see your activity.',
  disconnectedHint: 'Connect a wallet to see your activity.',
  empty: 'No token transfers in the last {blocks} blocks.',
  window: 'Blocks {from}–{to}',
  sent: 'Sent',
  received: 'Received',
  self: 'Moved',
  minted: 'Minted',
  burned: 'Burned',
  pending: 'Pending',
  token: 'Token {address}',
  tokenId: '#{id}',
  block: 'Block {height}',
  truncated: 'Showing the latest results only.',
  loadFailed: 'Could not load activity: {error}',
};

export const RECEIVE_LABELS = {
  ...COMMON_LABELS,
  title: 'Receive',
  disconnectedHint: 'Connect a wallet to show your receive address.',
  addressLabel: 'Your SQ: address',
  amountLabel: 'Request amount (SPLD, optional)',
  qrLabel: 'QR code for {target}',
  copyLink: 'Copy payment link',
  hint: 'Only send SPLD and Splendor tokens on {network} to this address.',
};

export const ADDRESS_BOOK_LABELS = {
  ...COMMON_LABELS,
  title: 'Address book',
  empty: 'No contacts on this network yet.',
  labelLabel: 'Name',
  labelPlaceholder: 'Alice',
  addressLabel: 'Address (SQ:…)',
  add: 'Save contact',
  remove: 'Remove',
  removeAria: 'Remove {label}',
  invalidLabel: 'Name must be 1–40 characters',
  full: 'Your address book on this network is full',
  search: 'Search contacts',
  stored: 'Saved in this browser only.',
};

export const BALANCES_LABELS = {
  ...COMMON_LABELS,
  title: 'Balances',
  lockedHint: 'Wallet locked — unlock to see your balances.',
  disconnectedHint: 'Connect a wallet to see your balances.',
  native: 'SPLD',
  empty: 'No tokens configured.',
  unknown: '—',
  hideZero: 'Hide zero balances',
};

export type CommonWidgetLabels = typeof COMMON_LABELS;
export type NftPanelLabels = typeof NFT_LABELS;
export type BridgeClaimLabels = typeof BRIDGE_LABELS;
export type ValidatorListLabels = typeof VALIDATOR_LABELS;
export type ActivityFeedLabels = typeof ACTIVITY_LABELS;
export type ReceiveWidgetLabels = typeof RECEIVE_LABELS;
export type AddressBookLabels = typeof ADDRESS_BOOK_LABELS;
export type TokenBalancesLabels = typeof BALANCES_LABELS;
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
  nft?: Partial<NftPanelLabels>;
  bridge?: Partial<BridgeClaimLabels>;
  validators?: Partial<ValidatorListLabels>;
  activity?: Partial<ActivityFeedLabels>;
  receive?: Partial<ReceiveWidgetLabels>;
  addressBook?: Partial<AddressBookLabels>;
  balances?: Partial<TokenBalancesLabels>;
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
