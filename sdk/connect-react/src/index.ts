/**
 * @splendor/connect-react — AppKit-style connect UI for Splendor Quantum Chain.
 *
 *   import '@splendor/connect-react/style.css';
 *   import { SplendorConnectProvider, ConnectButton, metamaskConnector } from '@splendor/connect-react';
 */

export {
  metamaskConnector,
  injectedConnector,
  seedConnector,
  hasAuthorizedAccounts,
  assertSeedHex,
  generateSeedHex,
  discoverInjectedConnectors,
  defaultBrowserConnectors,
} from './connectors/index.js';
export type {
  Connector,
  ConnectorContext,
  ConnectorField,
  MetamaskConnectorOpts,
  InjectedConnectorOpts,
  SeedConnectorOpts,
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
  useExchange,
  useDeployExchange,
  useDex,
  useDeployPool,
  useBridge,
  useDeployBridge,
  useContract,
  useDeployBytecode,
  useModules,
} from './react/hooks.js';
export type { TxHookStatus } from './react/hooks.js';

export {
  ConnectKit,
  ConnectButton,
  ConnectModal,
  AccountModal,
  NetworkBadge,
  ModalShell,
  ModalHost,
} from './ui/index.js';
export type { ConnectButtonProps } from './ui/ConnectButton.js';
export type { NetworkBadgeProps } from './ui/NetworkBadge.js';

export type { ThemeName, ThemeOverrides, ThemeProp } from './theme/themes.js';
export { resolveThemeAttr, themeOverridesToStyle } from './theme/themes.js';
