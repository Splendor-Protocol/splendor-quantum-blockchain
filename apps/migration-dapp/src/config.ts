/**
 * Worker environment and migration-bridge configuration.
 *
 * Everything is a plain `var` in wrangler.jsonc — this Worker holds no
 * secrets, no keys, and signs nothing. It renders the page and proxies
 * read-only chain state; every write happens in the user's own wallet.
 *
 * Three vars have no safe default because the thing they point at does not
 * exist until deploy day: BURN_CONTRACT_ADDRESS, QUANTUM_RPC_BASE and
 * QUANTUM_BRIDGE_ADDRESS. Until they are set, `appConfig` throws and the API
 * answers 503 `unconfigured` (faucet pattern) instead of 500s.
 */

export interface Env {
  /** Per-IP cooldown bookkeeping for /api/status polling. */
  MIGRATION_KV: KVNamespace;

  // ── public vars (wrangler.jsonc) ───────────────────────────────────────────
  /** Legacy Splendor mainnet JSON-RPC (server-side receipt/blockNumber calls). */
  EVM_RPC_URL?: string;
  /** Legacy chain id, hex — 0xa83 = 2691. */
  EVM_CHAIN_ID_HEX?: string;
  /** Legacy chain explorer, for burn-tx links. */
  EVM_EXPLORER_URL?: string;
  /** QuantumMigrationBurn contract on the legacy chain. REQUIRED. */
  BURN_CONTRACT_ADDRESS?: string;
  /** SPLV ERC-20 on the legacy chain. */
  SPLV_TOKEN_ADDRESS?: string;
  /** Blocks a burn must age on 2691 before the relayer claims. */
  CONFIRMATIONS?: string;
  /** Splendor Quantum node REST base (GET /bridge/{addr}…). REQUIRED. */
  QUANTUM_RPC_BASE?: string;
  /** Bridge module instance address on the quantum chain. REQUIRED. */
  QUANTUM_BRIDGE_ADDRESS?: string;
  /** Quantum chain explorer, for credited links. */
  QUANTUM_EXPLORER_URL?: string;
  /** Per-IP /api/status cooldown in ms (default 2000). */
  STATUS_COOLDOWN_MS?: string;
}

export const DEFAULT_EVM_RPC_URL = 'https://mainnet-rpc.splendor.org/';
export const DEFAULT_EVM_CHAIN_ID_HEX = '0xa83'; // 2691
export const DEFAULT_EVM_EXPLORER_URL = 'https://explorer.splendor.org';
export const DEFAULT_SPLV_TOKEN = '0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689';
export const DEFAULT_CONFIRMATIONS = 120;
export const DEFAULT_QUANTUM_EXPLORER_URL = 'https://quantum-explorer.splendor.org';
export const DEFAULT_STATUS_COOLDOWN_MS = 2000;

export const CHAIN_NAME = 'Splendor Mainnet';

/** Minimum burn: 1 whole token (10^18 units), enforced on-chain — mirrored here for the UI. */
export const MIN_BURN = (10n ** 18n).toString();

const EVM_ADDRESS = /^0x[0-9a-fA-F]{40}$/;
const CHAIN_ID_HEX = /^0x[0-9a-fA-F]{1,16}$/;
// Bridge instance addresses are 32-byte module addresses; accept them with or
// without the SQ: prefix and pass through verbatim (the REST path wants
// whatever form the node registered).
const BRIDGE_ADDRESS = /^(?:SQ:)?[0-9a-fA-F]{64}$/;

export interface AppConfig {
  evmRpcUrl: string;
  chainIdHex: string;
  chainName: string;
  evmExplorerUrl: string;
  /** Lowercased 0x address of QuantumMigrationBurn. */
  burnContract: string;
  /** SPLV ERC-20 address as configured (checksum case preserved for display). */
  splvToken: string;
  /** Smallest-unit decimal string. */
  minBurn: string;
  confirmations: number;
  quantumRpcBase: string;
  quantumBridgeAddress: string;
  quantumExplorerUrl: string;
  statusCooldownMs: number;
}

function req(value: string | undefined, name: string): string {
  const v = (value ?? '').trim();
  if (v === '') throw new Error(`${name} is not set`);
  return v;
}

function parseInt10(value: string | undefined, fallback: number, field: string): number {
  if (value === undefined || value.trim() === '') return fallback;
  const n = Number(value.trim());
  if (!Number.isInteger(n) || n < 0) throw new Error(`${field} must be a non-negative integer`);
  return n;
}

/** Strip trailing slashes so `${base}/bridge/…` never doubles a slash. */
function baseUrl(raw: string, name: string): string {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new Error(`${name} is not a URL`);
  }
  const loopback = url.hostname === 'localhost' || url.hostname === '127.0.0.1';
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) {
    throw new Error(`${name} must be https (got ${url.protocol})`);
  }
  return `${url.origin}${url.pathname}`.replace(/\/+$/, '');
}

/** Full config. Throws (→ 503 unconfigured at the route) when incomplete. */
export function appConfig(env: Env): AppConfig {
  const burnContract = req(env.BURN_CONTRACT_ADDRESS, 'BURN_CONTRACT_ADDRESS');
  if (!EVM_ADDRESS.test(burnContract)) {
    throw new Error('BURN_CONTRACT_ADDRESS is not a 0x…40-hex address');
  }
  const splvToken = (env.SPLV_TOKEN_ADDRESS ?? DEFAULT_SPLV_TOKEN).trim();
  if (!EVM_ADDRESS.test(splvToken)) {
    throw new Error('SPLV_TOKEN_ADDRESS is not a 0x…40-hex address');
  }
  const chainIdHex = (env.EVM_CHAIN_ID_HEX ?? DEFAULT_EVM_CHAIN_ID_HEX).trim();
  if (!CHAIN_ID_HEX.test(chainIdHex)) {
    throw new Error('EVM_CHAIN_ID_HEX is not a 0x-prefixed hex chain id');
  }
  const bridgeAddress = req(env.QUANTUM_BRIDGE_ADDRESS, 'QUANTUM_BRIDGE_ADDRESS');
  if (!BRIDGE_ADDRESS.test(bridgeAddress)) {
    throw new Error('QUANTUM_BRIDGE_ADDRESS is not a 64-hex bridge instance address');
  }

  return {
    evmRpcUrl: baseUrl((env.EVM_RPC_URL ?? DEFAULT_EVM_RPC_URL).trim(), 'EVM_RPC_URL'),
    chainIdHex,
    chainName: CHAIN_NAME,
    evmExplorerUrl: baseUrl(
      (env.EVM_EXPLORER_URL ?? DEFAULT_EVM_EXPLORER_URL).trim(),
      'EVM_EXPLORER_URL',
    ),
    burnContract: burnContract.toLowerCase(),
    splvToken,
    minBurn: MIN_BURN,
    confirmations: parseInt10(env.CONFIRMATIONS, DEFAULT_CONFIRMATIONS, 'CONFIRMATIONS'),
    quantumRpcBase: baseUrl(req(env.QUANTUM_RPC_BASE, 'QUANTUM_RPC_BASE'), 'QUANTUM_RPC_BASE'),
    quantumBridgeAddress: bridgeAddress,
    quantumExplorerUrl: baseUrl(
      (env.QUANTUM_EXPLORER_URL ?? DEFAULT_QUANTUM_EXPLORER_URL).trim(),
      'QUANTUM_EXPLORER_URL',
    ),
    statusCooldownMs: parseInt10(
      env.STATUS_COOLDOWN_MS,
      DEFAULT_STATUS_COOLDOWN_MS,
      'STATUS_COOLDOWN_MS',
    ),
  };
}
