/**
 * Thin account / tx DTOs so UI apps rarely need SplendorPQBridge for common flows.
 */
import type { ConnectionStatus, ConnectAccount } from '../core/types.js';
import type { TxHookStatus } from '../react/useTxAction.js';

export type AccountDto = {
  address: string | null;
  evmAddress: string | null;
  status: ConnectionStatus;
  isConnected: boolean;
  isLocked: boolean;
  postQuantumSecure: boolean | null;
};

export type TxResultDto = {
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  confirmed?: boolean;
  errorMessage: string | null;
};

export function toAccountDto(
  account: ConnectAccount | null,
  status: ConnectionStatus,
): AccountDto {
  return {
    address: account?.address ?? null,
    evmAddress: account?.evmAddress ?? null,
    status,
    isConnected: status === 'connected' && !!account,
    isLocked: status === 'locked',
    postQuantumSecure: account?.info?.postQuantumSecure ?? null,
  };
}

export function toTxResultDto(input: {
  status: TxHookStatus;
  hash: string | null;
  explorerUrl: string | null;
  confirmed?: boolean;
  errorMessage?: string | null;
}): TxResultDto {
  return {
    status: input.status,
    hash: input.hash,
    explorerUrl: input.explorerUrl,
    confirmed: input.confirmed,
    errorMessage: input.errorMessage ?? null,
  };
}
