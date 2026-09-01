/**
 * Explorer / contract-interaction RPC types.
 */
export type EstimateGasRequest = {
  type?: number;
  from?: string;
  to?: string;
  data?: string | Uint8Array;
  encoding?: 'hex' | 'base64';
  value?: string;
};

export type EstimateGasResponse = {
  gas: number;
  fee: string;
  fee_is_floor: boolean;
  reverted?: boolean;
  revert_reason?: string;
  note?: string;
};

export type LogFilter = {
  address?: string;
  from_block?: number;
  to_block?: number;
  topics?: string[];
};

export type ChainLog = {
  address: string;
  topics: string[];
  data: string;
  block_height?: number;
  tx_hash?: string;
  log_index?: number;
};

export type GetLogsResponse = {
  logs: ChainLog[];
  count: number;
  from_block: number;
  to_block: number;
  truncated: boolean;
};

export type VerifyContractResponse = {
  verified: boolean;
  kind: string;
  address?: string;
  reason?: string;
  code_hash?: string;
  bytecode_size?: number;
};

export type WsEventType = 'new_block' | 'new_tx' | 'new_receipt' | 'node_status' | '*';

export type WsEvent = {
  type: string;
  data: unknown;
  timestamp?: number;
};

export type Subscription = {
  close: () => void;
};
