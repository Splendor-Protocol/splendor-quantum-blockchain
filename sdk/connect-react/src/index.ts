/**
 * @splendor/connect-react — AppKit-style connect UI for Splendor Quantum Chain.
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
  usePayX402,
  useModuleCall,
  useQRC1155,
  useDeployCollection,
  useBridge,
  useDeployBridge,
  useContract,
  useDeployBytecode,
  useModules,
  useSignIn,
  createAuthMessage,
} from './react/hooks.js';
export type { TxHookStatus } from './react/hooks.js';
export type { AccountDto, TxResultDto } from './core/dto.js';
export { toAccountDto, toTxResultDto } from './core/dto.js';
export type { AuthMessageParams, SignedAuth } from './react/auth.js';

export {
  ConnectKit,
  ConnectButton,
  ConnectModal,
  AccountModal,
  NetworkBadge,
  ModalShell,
  ModalHost,
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
  sanitizeIconUrl,
} from './widgets/index.js';
export type {
  AccountCardProps,
  TransferWidgetProps,
  TokenPanelProps,
  DeployTokenWidgetProps,
} from './widgets/index.js';

export type { ThemeName, ThemeOverrides, ThemeProp } from './theme/themes.js';
export { resolveThemeAttr, themeOverridesToStyle } from './theme/themes.js';
