/**
 * Browser entry — loads WASM via fetch only (no Node fs).
 * Import: `import { SplendorPQBridge } from '@splendor/pq-bridge/browser'`
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
  getNetwork,
  resolveNetwork,
  derivationDomainMessage,
  networkToRpcOptions,
} from './networks.js';
export type { NetworkConfig, NetworkId, ProtocolConfig, NetworksFile } from './networks.js';
export { createSplendorClient, connectSplendor } from './connect.js';
export type {
  ConnectMode,
  CreateClientOptions,
  ConnectSplendorOptions,
  SplendorClient,
} from './connect.js';
export { SplendorPQBridge } from './wallet.js';
export type {
  BridgeOptions,
  DeriveInfo,
  ConnectionState,
  TransferOpts,
  CallOpts,
  DeployOpts,
  X402PayOpts,
  PreflightResult,
} from './types.js';
export { loadWasmInBrowser } from './wasm-loader.browser.js';
export { loadWasmSigner, resetWasmLoader } from './wasm-loader.js';

export { SplendorPQBridge as default } from './wallet.js';
