/**
 * Shared public API surface for Node (`index`) and browser (`browser`) entries.
 */

/** Wire `chain_id` constant — still required for tx / assertChainId. */
export { CHAIN_ID } from './derive.js';

/**
 * @deprecated Do not use in dApps — EVM→PQ derivation removed from ConnectKit.
 * Historical offline / tooling helpers only (see SQDP-1 Appendix A).
 */
export {
  DERIVATION_DOMAIN_MESSAGE,
  seedFromEvmSignature,
  signDerivationMessage,
  recoverPersonalSignAddress,
  recoverEip712Address,
  assertSignatureMatchesAddress,
  evmAddressFromPrivateKey,
  eip712DerivationTypedData,
  eip712Hash,
  personalSignHash,
} from './derive.js';
/** @deprecated Do not use in dApps — EVM→PQ derivation removed from ConnectKit. */
export type { DerivationScheme, Eip712DeriveTypedData } from './derive.js';
export { fromSPLD, toSPLD, assertSafeRpcUrl, redactRpcUrl } from './rpc.js';
/** @deprecated Prefer Connection / Transport. SqRpc remains the private adapter behind RpcTransport. */
export { SqRpc } from './rpc.js';
export type { RpcOptions } from './rpc.js';
export { SqcProvider } from './provider.js';
export type { ProviderOptions } from './provider.js';
export { BridgeError, isBridgeError, WalletError, isWalletError, wrapUnknown, mapProviderError } from './errors.js';
export type { BridgeErrorCode, WalletErrorCode } from './errors.js';
export { isValidAddress, assertAddress, canonicalizeAddress } from './address.js';
export { MIN_FEE, bytesToHex, hexToBytes, isHexString } from './encoding.js';
export {
  encodeFunctionData,
  encodeFunctionDataHex,
  functionSelector,
  functionSelectorHex,
  decodeAddress,
  decodeUint256,
  decodeArgAddress,
  decodeArgUint256,
  assertQSolSignature,
} from './abi.js';
export type { AbiArg } from './abi.js';
export type { BridgeEventMap, WalletEventMap } from './events.js';
export {
  SQDP_VERSION,
  PROTOCOL,
  BRIDGE_CLIENT_VERSION,
  ADDRESS_PREFIX,
  ADDRESS_HEX_LEN,
  ADDRESS_PATTERN,
} from './protocol.js';
export type { ProtocolConstants } from './protocol.js';
export {
  getNetworksFile,
  getProtocolConfig,
  getDefaultNetworkId,
  getDefaultNetwork,
  listNetworks,
  listSelectableNetworks,
  getNetwork,
  resolveNetwork,
  isPlaceholderNetwork,
  registerNetwork,
  unregisterNetwork,
  clearRegisteredNetworks,
  txExplorerUrl,
  derivationDomainMessage,
  networkToRpcOptions,
  isLoopbackOrLocalhostUrl,
} from './networks.js';
export type {
  NetworkConfig,
  NetworkId,
  ProtocolConfig,
  NetworksFile,
  ResolveNetworkOptions,
} from './networks.js';
export { createSplendorClient, connectSplendor } from './connect.js';
export type {
  ConnectMode,
  CreateClientOptions,
  ConnectSplendorOptions,
  ClientSession,
} from './connect.js';
export { SplendorPQBridge, Wallet } from './splendor-wallet.js';
export type {
  BridgeOptions,
  DeriveInfo,
  ConnectionState,
  TransferOpts,
  CallOpts,
  DeployOpts,
  X402PayOpts,
  PreflightResult,
  ModuleCallOpts,
  WriteOpts,
  PermitParams,
  SignedPermit,
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
  BridgeClaimSignParams,
  SignedBridgeClaim,
  WalletAccount,
  WalletConnectOpts,
  UnsignedTx,
  SignedTx,
  SendTxResult,
  SignMessageOpts,
} from './types.js';
export { createPublicClient, createWalletClient, createWalletClientAsync } from './clients.js';
export type { PublicClientOptions } from './clients.js';
export { SIGN_MESSAGE_DOMAIN, prepareSignMessage } from './message-domain.js';
export {
  createAuthMessage,
  parseAuthMessage,
  checkAuthMessage,
  authCheckMessage,
  type AuthMessageParams,
  type ParsedAuthMessage,
  type AuthCheckReason,
  type AuthCheckResult,
  type AuthCheckOptions,
} from './auth-message.js';
export {
  createSplendorProvider,
  installSplendorProvider,
  uninstallSplendorProvider,
  getSplendorProvider,
} from './injected-provider.js';
export type {
  SplendorInjectedProvider,
  SplendorProviderRequestArgs,
  SplendorProviderEventMap,
  InstallSplendorProviderOptions,
} from './injected-provider.js';
export { Connection } from './core/connection.js';
export { buildModuleCall, encodeModuleCallHex, type ModuleCall } from './core/module-call.js';
export { Program } from './core/program.js';
export { RpcTransport } from './core/transport/rpc-transport.js';
export { MockTransport } from './core/transport/mock-transport.js';
export { MockSigner, type Signer } from './core/signer.js';
export type { Transport } from './core/transport/types.js';
export { MODULE_ROUTER_ADDRESS, qrc20Address, qrc1155Address, qrc721Address, bytecodeContractAddress, bridgeAddress, bridgeSplvAddress } from './addresses/index.js';
export { QRC20, deployQRC20, getToken } from './tokens/qrc20.js';
export { QRC1155, deployQRC1155, getCollection } from './tokens/qrc1155.js';
export { QRC721, deployQRC721, getNftCollection } from './tokens/qrc721.js';
export { Contract, deployBytecode, getContract } from './contracts/contract.js';
export { BridgeModule, deployBridge, getBridge } from './bridge/module.js';
export { VerifyClient, getVerify } from './verify.js';
export type { VerifyInstruction } from './verify.js';
export { ValidatorsClient, getValidators, VALIDATOR_BOND_SPLD } from './validators.js';
export {
  modules,
  bindModules,
  ensureBuiltInModulesRegistered,
  registerUnsupportedApplicationAdapters,
  unsupportedApplicationAdaptersEnabled,
  UNSUPPORTED_APPLICATION_ADAPTER_IDS,
  UNSUPPORTED_ADAPTER_MESSAGE,
  ROUTED_NATIVE_MODULE_IDS,
} from './modules.js';
export type { Modules, BoundModules, SignedBoundModules } from './modules.js';
export {
  registerModule,
  defineModule,
  getModule,
  listModules,
  createModuleClient,
  bindRegisteredModules,
} from './modules.js';
export { withSigner } from './bound.js';
export type { BoundClient, WalletLike } from './bound.js';
export { fromUnits, toUnits, parseRawAmount } from './units.js';
export { X402Client, createX402Middleware } from './x402/client.js';
export type { PaymentRequirements, PaymentPayload } from './x402/client.js';
export { subscribe } from './core/subscribe.js';
export type {
  EstimateGasRequest,
  EstimateGasResponse,
  LogFilter,
  GetLogsResponse,
  WsEventType,
  WsEvent,
  Subscription,
  VerifyContractResponse,
} from './core/explorer-types.js';
export { Keypair } from './wallet/keypair.js';
export { connect } from './wallet/connect.js';
export type { ConnectOptions, ConnectResult } from './wallet/connect.js';
export type {
  Connector,
  WalletFactoryConnector,
  ConnectorConnectOptions,
  WalletUnlockConnector,
  WalletUnlockContext,
} from './wallet/connectors/index.js';
export { seedWalletFactory, seedConnector, extensionWalletFactory, extensionConnector } from './wallet/connectors/index.js';
export {
  ExtensionSigner,
  connectExtension,
  type ExtensionSignerOptions,
  type ConnectExtensionResult,
} from './wallet/extension-signer.js';
export {
  METHOD_ALIASES,
  toCanonicalMethod,
  toExtensionMethod,
  toExtensionParams,
  getExtensionProvider,
  getSplendorInjected,
  discoverExtensionProvider,
  clearExtensionDiscoveryCache,
  requestWithAlias,
  isExtensionProvider,
  isInPageProvider,
} from './wallet/extension-provider.js';
export type { SplendorProviderLike } from './wallet/extension-provider.js';
export { createSession } from './session.js';
export type { Session } from './session.js';
export { SplendorClient } from './client.js';
export { loadWasmSigner, resetWasmLoader } from './wasm-loader.js';
export { installBrowserSessionPolicy } from './wallet/browser-session-policy.js';
export { WasmSession } from './wallet/wasm-session.js';
