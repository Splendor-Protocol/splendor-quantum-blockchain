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
  createAuthMessage,
} from './react/hooks.js';
export type { TxHookStatus, TokenInfo, ReadState, ReadContractResult, FeeEstimate, PendingTx } from './react/hooks.js';
export {
  PENDING_TX_KEY,
  loadPendingTxs,
  addPendingTx,
  removePendingTx,
  clearPendingTxs,
} from './core/pendingTx.js';
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
  sanitizeIconUrl,
  normalizeSqAddress,
  compareDecimal,
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
