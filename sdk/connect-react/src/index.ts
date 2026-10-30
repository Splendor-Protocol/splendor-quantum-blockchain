/**
 * @splendor/connect-react — ConnectKit for Splendor Quantum Chain.
 * Wallets: Splendor Quantum Wallet extension + native ML-DSA-65 seed only.
 *
 *   import '@splendor/connect-react/style.css';
 *   import { SplendorConnectProvider, ConnectButton, seedConnector } from '@splendor/connect-react';
 */

export {
  seedConnector,
  extensionConnector,
  assertSeedHex,
  generateSeedHex,
  defaultBrowserConnectors,
} from './connectors/index.js';
export type {
  Connector,
  ConnectorContext,
  ConnectorField,
  SeedConnectorOpts,
  ExtensionConnectorOpts,
  WalletUnlockContext,
  WalletUnlockConnector,
} from './connectors/index.js';
export type { NetworkId } from '@splendor/sqc-sdk';

export {
  createConnectController,
  createStubConnectController,
  connectErrorMessage,
} from './core/controller.js';
export type {
  ConnectController,
  CreateConnectControllerOptions,
  ErrorContext,
  SessionWallet,
} from './core/controller.js';
export type {
  ConnectionStatus,
  ConnectingStep,
  ConnectAccount,
  ModalView,
  ConnectControllerState,
} from './core/types.js';
export { describeSignRequest, withSignApproval } from './core/signApproval.js';
export type { SignRequest, SignApprover } from './core/signApproval.js';
export {
  getLastConnectorId,
  setLastConnectorId,
  getLastNetworkId,
  setLastNetworkId,
  broadcastConnectEvent,
  subscribeConnectBroadcast,
} from './core/storage.js';
export type { ConnectBroadcastMessage } from './core/storage.js';

export { SplendorConnectProvider } from './react/provider.js';
export { useConnectController, useConnectState, useNetworkSwitch } from './react/context.js';
export type {
  SplendorConnectProviderProps,
  ConnectBrandLabels,
} from './react/provider.js';
export {
  useConnect,
  useAccount,
  useAccountDto,
  useBalance,
  useNetwork,
  useWallet,
  useSendTransfer,
  useCallContract,
  useDeployContract,
  usePreflight,
  useWaitForConfirmation,
  useQRC20,
  useDeployToken,
  useModuleCall,
  useQRC1155,
  useDeployCollection,
  useQRC721,
  useDeployQRC721,
  useBridge,
  useDeployBridge,
  useContract,
  useDeployBytecode,
  useModules,
  useSignIn,
  useReadConnection,
  useTokenInfo,
  useTokenBalance,
  useReadContract,
  useEstimateFee,
  usePendingTransactions,
  useValidators,
  useActivity,
  useAddressBook,
  parseValidator,
  createAuthMessage,
} from './react/hooks.js';
export type {
  TxHookStatus,
  TokenInfo,
  ReadState,
  ReadContractResult,
  FeeEstimate,
  PendingTx,
  ValidatorInfo,
  ValidatorSet,
  ValidatorStatus,
  ActivityItem,
  ActivityDirection,
  ActivityKind,
  ActivityOptions,
  ActivityResult,
} from './react/hooks.js';
export {
  TOPIC_TRANSFER,
  TOPIC_TRANSFER_SINGLE,
  TOPIC_TRANSFER_BATCH,
  MAX_LOG_BLOCK_RANGE,
  activityFilters,
  parseActivityLog,
  mergeActivity,
} from './core/activity.js';
export {
  ADDRESS_BOOK_KEY,
  ADDRESS_BOOK_MAX,
  getContacts,
  saveContact,
  removeContact,
  subscribeContacts,
} from './core/addressBook.js';
export type { Contact, SaveContactResult } from './core/addressBook.js';
export { buildPaymentUri, parsePaymentUri, PAYMENT_URI_SCHEME } from './core/paymentUri.js';
export type { PaymentRequest } from './core/paymentUri.js';
export {
  PENDING_TX_KEY,
  loadPendingTxs,
  addPendingTx,
  removePendingTx,
  clearPendingTxs,
} from './core/pendingTx.js';
export { getRecentTokens, rememberDeployedToken, subscribeRecentTokens } from './core/recentTokens.js';
export type { RecentToken } from './core/recentTokens.js';
export type { AccountDto, TxResultDto } from './core/dto.js';
export { toAccountDto, toTxResultDto } from './core/dto.js';
export { parseAuthMessage, checkAuthMessage, authCheckMessage } from './react/auth.js';
export type {
  AuthMessageParams,
  SignedAuth,
  ParsedAuthMessage,
  AuthCheckReason,
  AuthCheckResult,
} from './react/auth.js';

export {
  ConnectKit,
  ConnectButton,
  ConnectModal,
  AccountModal,
  NetworkBadge,
  ModalShell,
  ModalHost,
  SignApprovalModal,
  TxToastHost,
  useTxToast,
  WrongNetworkBanner,
} from './ui/index.js';
export type { ConnectButtonProps } from './ui/ConnectButton.js';
export type { NetworkBadgeProps } from './ui/NetworkBadge.js';
export type { TxToastHostProps, TxToast } from './ui/TxToastHost.js';
export type { WrongNetworkBannerProps } from './ui/WrongNetworkBanner.js';

export {
  AccountCard,
  TransferWidget,
  TokenPanel,
  DeployTokenWidget,
  SignInButton,
  NftPanel,
  BridgeClaimWidget,
  ValidatorList,
  ActivityFeed,
  ReceiveWidget,
  AddressBook,
  TokenBalances,
  QrCode,
  ContactPicks,
  encodeQr,
  qrSvgPath,
  parseTokenIds,
  parseTokenList,
  parseAttestations,
  NFT_PANEL_MAX_IDS,
  TOKEN_BALANCES_MAX,
  NFT_LABELS,
  BRIDGE_LABELS,
  VALIDATOR_LABELS,
  ACTIVITY_LABELS,
  RECEIVE_LABELS,
  ADDRESS_BOOK_LABELS,
  BALANCES_LABELS,
  sanitizeIconUrl,
  normalizeSqAddress,
  compareDecimal,
  spendableAfterFee,
  exceedsBalanceWithFee,
  reviewTransfer,
  getRecentRecipients,
  rememberRecipient,
  addressExplorerUrl,
  COMMON_LABELS,
  TRANSFER_LABELS,
  TOKEN_LABELS,
  DEPLOY_TOKEN_LABELS,
  ACCOUNT_LABELS,
  SIGN_IN_LABELS,
  formatLabel,
  mergeWidgetLabels,
  useWidgetLabels,
  WidgetShell,
  WalletGate,
  AddressInput,
  AmountInput,
  RecentRecipients,
  addressFieldError,
  amountFieldError,
  ReviewPanel,
  TransferReviewPanel,
  FeeRow,
  PendingTxList,
  TxResult,
  AddressDisplay,
  CopyButton,
  useTransferForm,
  useStatusChange,
  useWidgetToast,
  useOptionalTxToast,
} from './widgets/index.js';
export type {
  AccountCardProps,
  TransferWidgetProps,
  TransferSentResult,
  TokenPanelProps,
  TokenSentResult,
  DeployTokenWidgetProps,
  TokenDeployedResult,
  SignInButtonProps,
  NftPanelProps,
  NftActionResult,
  BridgeClaimWidgetProps,
  BridgeClaimResult,
  ValidatorListProps,
  ActivityFeedProps,
  ReceiveWidgetProps,
  AddressBookProps,
  TokenBalancesProps,
  QrCodeProps,
  QrEcc,
  QrMatrix,
  NftPanelLabels,
  BridgeClaimLabels,
  ValidatorListLabels,
  ActivityFeedLabels,
  ReceiveWidgetLabels,
  AddressBookLabels,
  TokenBalancesLabels,
  TransferReview,
  TransferReviewReason,
  BaseWidgetProps,
  WidgetStatus,
  CommonWidgetLabels,
  TransferWidgetLabels,
  TokenPanelLabels,
  DeployTokenWidgetLabels,
  AccountCardLabels,
  SignInButtonLabels,
  WidgetLabelOverrides,
  WidgetKind,
  WidgetShellProps,
  WalletGateProps,
  AddressInputProps,
  AmountInputProps,
  ReviewPanelProps,
  FeeRowProps,
  PendingTxListProps,
  TxResultProps,
  AddressDisplayProps,
  CopyButtonProps,
  TransferForm,
  UseTransferFormOptions,
} from './widgets/index.js';

export type { ThemeName, ThemeOverrides, ThemeProp } from './theme/themes.js';
export { resolveThemeAttr, themeOverridesToStyle } from './theme/themes.js';
