/** Shared bridge types (no runtime deps — breaks circular imports). */
import type { RpcOptions } from './rpc.js';
import type { SqcProvider } from './provider.js';
import type { AbiArg } from './abi.js';
import type { EthereumProvider } from './metamask.js';
import type { DerivationScheme } from './derive.js';

export type BridgeOptions = RpcOptions & {
  wasmUrl?: string;
  execUrl?: string;
  /** Optional SHA-256 hex of sqc_signer.wasm — verified in browser when set. */
  wasmSha256?: string;
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
  amount: string;
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
};

export type DeployOpts = {
  bytecode: Uint8Array | string;
  fee?: string;
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
