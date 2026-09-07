/**
 * Shared public API surface for Node (`index`) and browser (`browser`) entries.
 */
export {
  DERIVATION_DOMAIN_MESSAGE,
  CHAIN_ID,
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
export type { DerivationScheme, Eip712DeriveTypedData } from './derive.js';
export { fromSPLD, toSPLD, SqRpc } from './rpc.js';
export type { RpcOptions } from './rpc.js';
export { SqcProvider } from './provider.js';
export type { ProviderOptions } from './provider.js';
export {
  connectMetaMask,
  signDerivationWithMetaMask,
  onMetaMaskAccountsChanged,
  onMetaMaskDisconnect,
} from './metamask.js';
export type { EthereumProvider, SignDerivationOpts } from './metamask.js';
export { BridgeError, isBridgeError } from './errors.js';
export type { BridgeErrorCode } from './errors.js';
export { isValidAddress, assertAddress, canonicalizeAddress } from './address.js';
export { MIN_FEE, MIN_FEE_V1, bytesToHex, hexToBytes, isHexString } from './encoding.js';
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
export type { BridgeEventMap } from './events.js';
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
  PermitParams,
  SignedPermit,
  OrderParams,
  SignedOrder,
  SignedCancel,
  IncrementNonceParams,
  SignedIncrementNonce,
  BridgeClaimSignParams,
  SignedBridgeClaim,
  VerifyInstruction,
  VerifyInstructionInput,
  SignedVerifyInstruction,
  VerifyAttestationSignParams,
  SignedVerifyAttestation,
} from './types.js';
export { Connection } from './core/connection.js';
export { buildModuleCall, encodeModuleCallHex, type ModuleCall } from './core/module-call.js';
export { Program } from './core/program.js';
export { RpcTransport } from './core/transport/rpc-transport.js';
export { MockTransport } from './core/transport/mock-transport.js';
export { MockSigner, type Signer } from './core/signer.js';
export type { Transport } from './core/transport/types.js';
export { MODULE_ROUTER_ADDRESS, qrc20Address, qrc1155Address, bytecodeContractAddress, exchangeAddress, bridgeAddress, bridgeSplvAddress, dexPoolAddress, verifyAddress } from './addresses/index.js';
export { QRC20, deployQRC20, getToken } from './tokens/qrc20.js';
export { QRC1155, deployQRC1155, getCollection } from './tokens/qrc1155.js';
export { Contract, deployBytecode, getContract } from './contracts/contract.js';
export { Exchange, deployExchange, getExchange } from './defi/exchange.js';
export { DEX, deployPool, getPool } from './defi/dex.js';
export { BridgeModule, deployBridge, getBridge } from './bridge/module.js';
export { VerifyModule, deployVerify, getVerify } from './verify.js';
export type { VerifyDeployParams, VerifyAttestationInput } from './verify.js';
export { modules, bindModules, ensureBuiltInModulesRegistered } from './modules.js';
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
export { fromUnits, toUnits } from './units.js';
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
  ConnectorConnectOptions,
  WalletUnlockConnector,
  WalletUnlockContext,
} from './wallet/connectors/index.js';
export { seedConnector, metamaskConnector, evmKeyConnector } from './wallet/connectors/index.js';
export { createSession } from './session.js';
export type { Session } from './session.js';
export { SplendorClient } from './client.js';
export { loadWasmSigner, resetWasmLoader } from './wasm-loader.js';
