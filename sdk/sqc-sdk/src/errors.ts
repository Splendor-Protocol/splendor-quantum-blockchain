/** Typed errors for dApp-friendly handling. */

export type BridgeErrorCode =
  | 'NOT_CONNECTED'
  | 'USER_REJECTED'
  | 'NO_PROVIDER'
  | 'BUSY'
  | 'RPC'
  | 'RPC_UNAVAILABLE'
  | 'NETWORK'
  | 'WASM'
  | 'INVALID_ADDRESS'
  | 'INVALID_AMOUNT'
  | 'INVALID_FEE'
  | 'TIMEOUT'
  | 'TX_NOT_CONFIRMED'
  | 'TX_FAILED'
  | 'CHAIN_MISMATCH'
  | 'UNKNOWN';

export class BridgeError extends Error {
  readonly code: BridgeErrorCode;
  readonly details?: unknown;
  /** Short UI-safe hint when `message` is technical. */
  readonly userHint?: string;

  constructor(
    code: BridgeErrorCode,
    message: string,
    details?: unknown,
    userHint?: string,
  ) {
    super(message);
    this.name = 'BridgeError';
    this.code = code;
    this.details = details;
    this.userHint = userHint;
  }

  static notConnected(msg = 'Not connected — call connect*() first'): BridgeError {
    return new BridgeError(
      'NOT_CONNECTED',
      msg,
      undefined,
      'Wallet is not connected — connect to continue',
    );
  }

  static wasm(msg: string): BridgeError {
    return new BridgeError('WASM', msg, undefined, 'Signer failed — reload and try again');
  }

  static rpc(msg: string, details?: unknown): BridgeError {
    return new BridgeError('RPC', msg, details, 'Network request failed — try again');
  }

  /** All configured RPC endpoints failed (timeout / network / 5xx). */
  static rpcUnavailable(msg: string, details?: unknown): BridgeError {
    return new BridgeError(
      'RPC_UNAVAILABLE',
      msg,
      details,
      'Network unavailable — check your connection or try again later',
    );
  }

  static network(msg: string, details?: unknown): BridgeError {
    return new BridgeError('NETWORK', msg, details, 'Network error — check your connection');
  }

  static invalidAddress(addr: string): BridgeError {
    return new BridgeError(
      'INVALID_ADDRESS',
      `Invalid SQ address: ${addr}`,
      undefined,
      'Enter a valid SQ: address',
    );
  }

  static invalidAmount(msg: string): BridgeError {
    return new BridgeError('INVALID_AMOUNT', msg, undefined, 'Enter a valid amount');
  }

  static invalidFee(msg: string): BridgeError {
    return new BridgeError('INVALID_FEE', msg, undefined, 'Fee is below the network minimum');
  }

  static timeout(msg: string): BridgeError {
    return new BridgeError('TIMEOUT', msg, undefined, 'Request timed out — try again');
  }

  static txNotConfirmed(msg: string): BridgeError {
    return new BridgeError(
      'TX_NOT_CONFIRMED',
      msg,
      undefined,
      'Transaction was not confirmed in time',
    );
  }

  static txFailed(msg: string, details?: unknown): BridgeError {
    return new BridgeError('TX_FAILED', msg, details, 'Transaction failed');
  }

  static chainMismatch(got: string, want: string): BridgeError {
    return new BridgeError(
      'CHAIN_MISMATCH',
      `chain_id mismatch: got ${got}, want ${want}`,
      undefined,
      'Wrong network — chain mismatch; switch to the expected Splendor network',
    );
  }

  static userRejected(msg = 'User rejected the request'): BridgeError {
    return new BridgeError('USER_REJECTED', msg, undefined, 'Request was rejected');
  }

  static busy(msg = 'Another connect is already in progress'): BridgeError {
    return new BridgeError(
      'BUSY',
      msg,
      undefined,
      'Connection already in progress — wait a moment',
    );
  }

  static noProvider(
    msg = 'Splendor Quantum Wallet extension not found (window.splendor)',
  ): BridgeError {
    return new BridgeError(
      'NO_PROVIDER',
      msg,
      undefined,
      'Install or unlock the Splendor Quantum Wallet',
    );
  }
}

export function isBridgeError(e: unknown): e is BridgeError {
  return e instanceof BridgeError;
}

/**
 * Preferred name for new code — same codes as BridgeError, `name === 'WalletError'`.
 * Runtime throw sites may still use BridgeError; use factories when you need the name.
 */
export class WalletError extends BridgeError {
  constructor(code: BridgeErrorCode, message: string, details?: unknown, userHint?: string) {
    super(code, message, details, userHint);
    this.name = 'WalletError';
  }

  static override notConnected(msg = 'Not connected — call connect*() first'): WalletError {
    return new WalletError(
      'NOT_CONNECTED',
      msg,
      undefined,
      'Wallet is not connected — connect to continue',
    );
  }

  static override txFailed(msg: string, details?: unknown): WalletError {
    return new WalletError('TX_FAILED', msg, details, 'Transaction failed');
  }
}

export type WalletErrorCode = BridgeErrorCode;
export const isWalletError = isBridgeError;

export function wrapUnknown(e: unknown, fallback: BridgeErrorCode = 'UNKNOWN'): BridgeError {
  if (e instanceof BridgeError) return e;
  const msg = e instanceof Error ? e.message : String(e);
  if (/abort|timeout/i.test(msg)) return BridgeError.timeout(msg);
  if (/EIP-1193 provider|window\.ethereum.*not found|provider.*not found/i.test(msg)) {
    return BridgeError.noProvider(msg);
  }
  if (/user rejected|denied/i.test(msg)) return BridgeError.userRejected(msg);
  return new BridgeError(fallback, msg, undefined, 'Something went wrong — try again');
}
