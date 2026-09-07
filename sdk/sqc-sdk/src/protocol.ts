/**
 * SQDP-1 — machine-readable Splendor dApp Protocol constants.
 * Dynamics (fees, versions, networks) live in src/config/sqc.networks.json.
 */
import { DERIVATION_DOMAIN_MESSAGE, CHAIN_ID } from './derive.js';
import { getProtocolConfig, getNetworksFile } from './networks.js';
import { SIGN_MESSAGE_DOMAIN } from './message-domain.js';

const proto = getProtocolConfig();
const file = getNetworksFile();

/** Splendor dApp Protocol major version string. */
export const SQDP_VERSION = file.sqdpVersion as '1';

/** Client header value for X-SQC-Bridge. */
export const BRIDGE_CLIENT_VERSION = proto.bridgeClientVersion;

export const ADDRESS_PREFIX = proto.addressPrefix as 'SQ:';
export const ADDRESS_HEX_LEN = proto.addressHexLen;

/** Canonical address pattern (case-insensitive hex). */
export const ADDRESS_PATTERN = new RegExp(
  `^${ADDRESS_PREFIX}[0-9a-fA-F]{${ADDRESS_HEX_LEN}}$`,
);

export const PROTOCOL = {
  version: SQDP_VERSION,
  chainId: CHAIN_ID,
  wireChainId: proto.wireChainId || CHAIN_ID,
  addressPrefix: ADDRESS_PREFIX,
  addressHexLen: ADDRESS_HEX_LEN,
  minFee: proto.minFee,
  derivationDomainVersion: proto.derivationDomainVersion,
  derivationDomainMessage: DERIVATION_DOMAIN_MESSAGE,
  /** Domain prefix for unstructured wallet.signMessage payloads. */
  signMessageDomain: SIGN_MESSAGE_DOMAIN,
  confirmation: proto.confirmation,
  bridgeClientHeader: BRIDGE_CLIENT_VERSION,
  abiHash: proto.abiHash,
  goldenTransferSelector: proto.goldenTransferSelector,
  defaultNetwork: file.defaultNetwork,
  /**
   * Runtime: receipt-first (GET /tx/{hash}/receipt), then nonce fallback.
   * `confirmation` config value names the fallback mechanism for minimal RPC mounts.
   */
  confirmationCaveat: 'receipt-preferred;nonce-fallback-race',
} as const;

export type ProtocolConstants = typeof PROTOCOL;
