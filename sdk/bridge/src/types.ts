/** Shared bridge types (no runtime deps — breaks circular imports). */
import type { RpcOptions } from './rpc.js';
import type { SqcProvider } from './provider.js';
import type { AbiArg } from './abi.js';
import type { EthereumProvider } from './metamask.js';
import type { DerivationScheme } from './derive.js';

export type BridgeOptions = RpcOptions & {
  wasmUrl?: string;
  execUrl?: string;
  /** Optional SHA-256 hex of sqc_signer.wasm — verified when set. */
  wasmSha256?: string;
  /** Optional SHA-256 hex of wasm_exec.js — verified when set. */
  execSha256?: string;
  /**
   * Allow loading WASM without wasmSha256 on non-local browser networks.
   * Default false — fail closed unless pin is provided.
   */
  allowUnpinnedWasm?: boolean;
  /**
   * Network id from sqc.networks.json (local | testnet | mainnet).
   * Used for browser pin policy and HTTPS checks.
   */
  networkId?: string;
  /**
   * Allow http:// RPC URLs for non-local networks (private LAN).
   * Default false — resolveNetwork requires https for testnet/mainnet.
   */
  allowInsecureHttp?: boolean;
  /**
   * Auto-lock after this many ms of inactivity (browser). 0 / undefined = disabled.
   */
  sessionIdleMs?: number;
  /**
   * When true, also lock immediately when the tab becomes hidden.
   * Default false — only the idle timer locks (avoids surprise desync on tab switch).
   */
  lockOnHidden?: boolean;
  /** When true (default), MetaMask account switches re-derive the PQ wallet. */
  watchMetaMaskAccounts?: boolean;
  /** Optional shared read provider; RPC options are taken from it when provided. */
  provider?: SqcProvider;
  /** Injected EIP-1193 provider (defaults to window.ethereum). */
  ethereumProvider?: EthereumProvider;
  /** Derivation scheme: eip191-v1 (default) or eip712-v1. */
  derivation?: DerivationScheme;
  /** Optional request/error middleware for observability. */
  onRequest?: (info: { method: string; path?: string }) => void;
  onError?: (err: unknown) => void;
};

export type DeriveInfo = {
  address: string;
  public_key_b64: string;
  public_key_hex: string;
  evmAddress?: string;
};

export type ConnectionState = 'disconnected' | 'connected';

export type TransferOpts = {
  to: string;
  /** Atto amount string; omit when amountSPLD is set. */
  amount?: string;
  amountSPLD?: string;
  fee?: string;
  memo?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
};

export type CallOpts = {
  contract: string;
  calldata?: Uint8Array | string;
  function?: string;
  args?: AbiArg[];
  value?: string;
  valueSPLD?: string;
  fee?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
};

export type DeployOpts = {
  bytecode: Uint8Array | string;
  fee?: string;
  waitForConfirmation?: boolean;
  confirmTimeoutMs?: number;
};

export type X402PayOpts = {
  to: string;
  amount: string;
  amountSPLD?: string;
  validAfter?: number;
  validBefore?: number;
  requirements?: object;
  settle?: boolean;
};

/** Dry-run / preflight estimate (no signing). */
export type PreflightResult = {
  from: string;
  balance: string;
  balance_spld: string;
  nonce: number;
  fee: string;
  chainId: string;
  sufficient: boolean;
};
