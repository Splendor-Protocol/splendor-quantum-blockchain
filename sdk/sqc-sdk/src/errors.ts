/** Typed errors for dApp-friendly handling. */

export type BridgeErrorCode =
  | 'NOT_CONNECTED'
  | 'USER_REJECTED'
  | 'NO_PROVIDER'
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
    return new BridgeError('NOT_CONNECTED', msg);
  }

  static wasm(msg: string): BridgeError {
    return new BridgeError('WASM', msg);
  }

  static rpc(msg: string, details?: unknown): BridgeError {
    return new BridgeError('RPC', msg, details);
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
    return new BridgeError('NETWORK', msg, details);
  }

  static invalidAddress(addr: string): BridgeError {
    return new BridgeError('INVALID_ADDRESS', `Invalid SQ address: ${addr}`);
  }

  static invalidAmount(msg: string): BridgeError {
    return new BridgeError('INVALID_AMOUNT', msg);
  }

  static invalidFee(msg: string): BridgeError {
    return new BridgeError('INVALID_FEE', msg);
  }

  static timeout(msg: string): BridgeError {
    return new BridgeError('TIMEOUT', msg);
  }

  static txNotConfirmed(msg: string): BridgeError {
    return new BridgeError('TX_NOT_CONFIRMED', msg);
  }

  static txFailed(msg: string, details?: unknown): BridgeError {
    return new BridgeError('TX_FAILED', msg, details);
  }

  static chainMismatch(got: string, want: string): BridgeError {
    return new BridgeError('CHAIN_MISMATCH', `chain_id mismatch: got ${got}, want ${want}`);
  }

  static userRejected(msg = 'User rejected the request'): BridgeError {
    return new BridgeError('USER_REJECTED', msg);
  }

  static noProvider(
    msg = 'Splendor Quantum Wallet extension not found (window.splendor)',
  ): BridgeError {
    return new BridgeError('NO_PROVIDER', msg);
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
  constructor(code: BridgeErrorCode, message: string, details?: unknown) {
    super(code, message, details);
    this.name = 'WalletError';
  }

  static override notConnected(msg = 'Not connected — call connect*() first'): WalletError {
    return new WalletError('NOT_CONNECTED', msg);
  }

  static override txFailed(msg: string, details?: unknown): WalletError {
    return new WalletError('TX_FAILED', msg, details);
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
  return new BridgeError(fallback, msg);
}
