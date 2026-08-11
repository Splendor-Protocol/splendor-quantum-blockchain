/**
 * @splendor/connect-react — AppKit-style connect UI for Splendor Quantum Chain.
 *
 *   import '@splendor/connect-react/style.css';
 *   import { SplendorConnectProvider, ConnectKit, metamaskConnector } from '@splendor/connect-react';
 */

export {
  metamaskConnector,
  injectedConnector,
  seedConnector,
} from './connectors/index.js';
export type {
  Connector,
  ConnectorContext,
  MetamaskConnectorOpts,
  InjectedConnectorOpts,
  SeedConnectorOpts,
} from './connectors/index.js';

export {
  createConnectController,
  connectErrorMessage,
} from './core/controller.js';
export type {
  ConnectController,
  CreateConnectControllerOptions,
} from './core/controller.js';
export type {
  ConnectionStatus,
  ConnectAccount,
  ModalView,
  ConnectControllerState,
} from './core/types.js';

export { SplendorConnectProvider } from './react/provider.js';
export type { SplendorConnectProviderProps } from './react/provider.js';
export {
  useConnect,
  useAccount,
  useBalance,
  useNetwork,
  useWallet,
} from './react/hooks.js';

export {
  ConnectKit,
  ConnectButton,
  ConnectModal,
  AccountModal,
  NetworkBadge,
  ModalShell,
} from './ui/index.js';
export type { ConnectButtonProps } from './ui/ConnectButton.js';
export type { NetworkBadgeProps } from './ui/NetworkBadge.js';

export type { ThemeName, ThemeOverrides, ThemeProp } from './theme/themes.js';
export { resolveThemeAttr, themeOverridesToStyle } from './theme/themes.js';
