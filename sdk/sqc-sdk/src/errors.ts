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
  | 'INVALID_PARAMS'
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
    // Users sometimes paste a seed or key into an address field; never echo it whole.
    const s = String(addr ?? '');
    const shown = s.length > 16 ? `${s.slice(0, 8)}…(${s.length} chars)` : s;
    return new BridgeError(
      'INVALID_ADDRESS',
      `Invalid SQ address: ${shown.replace(/[\u0000-\u001f\u007f]/g, '?')}`,
      undefined,
      'Enter a valid SQ: address',
    );
  }

  static invalidAmount(msg: string): BridgeError {
    return new BridgeError('INVALID_AMOUNT', msg, undefined, 'Enter a valid amount');
  }

  static invalidParams(msg: string, details?: unknown): BridgeError {
    return new BridgeError('INVALID_PARAMS', msg, details, 'Check the values you entered');
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

/** SQDP extension ProviderError numeric codes (research extension lib/sqdp.js). */
const SQDP_CODE_MAP: Record<number, BridgeErrorCode> = {
  4001: 'USER_REJECTED',
  4100: 'NOT_CONNECTED',
  4200: 'RPC',
  4900: 'INVALID_ADDRESS',
  4901: 'INVALID_FEE',
  4902: 'CHAIN_MISMATCH',
};

function factoryForCode(code: BridgeErrorCode, msg: string): BridgeError {
  switch (code) {
    case 'USER_REJECTED':
      return BridgeError.userRejected(msg);
    case 'NOT_CONNECTED':
      return BridgeError.notConnected(msg);
    case 'NO_PROVIDER':
      return BridgeError.noProvider(msg);
    case 'TIMEOUT':
      return BridgeError.timeout(msg);
    case 'CHAIN_MISMATCH':
      return BridgeError.chainMismatch('extension', 'expected');
    case 'INVALID_ADDRESS':
      return BridgeError.invalidAddress(msg);
    case 'INVALID_FEE':
      return BridgeError.invalidFee(msg);
    case 'BUSY':
      return BridgeError.busy(msg);
    case 'RPC_UNAVAILABLE':
      return BridgeError.rpcUnavailable(msg);
    case 'NETWORK':
      return BridgeError.network(msg);
    case 'RPC':
      return BridgeError.rpc(msg);
    default:
      return new BridgeError(code, msg, undefined, 'Something went wrong — try again');
  }
}

/**
 * Map extension / provider throws (numeric SQDP codes or message heuristics) to BridgeError.
 * Prefer this over bare wrapUnknown for wallet RPC boundaries.
 */
export function mapProviderError(e: unknown, fallback: BridgeErrorCode = 'RPC'): BridgeError {
  if (e instanceof BridgeError) return e;

  const codeRaw =
    e && typeof e === 'object' && 'code' in e
      ? (e as { code?: unknown }).code
      : undefined;
  const numeric =
    typeof codeRaw === 'number'
      ? codeRaw
      : typeof codeRaw === 'string' && /^\d+$/.test(codeRaw)
        ? Number(codeRaw)
        : undefined;
  const msg = e instanceof Error ? e.message : String(e);

  if (numeric != null && SQDP_CODE_MAP[numeric]) {
    const mapped = SQDP_CODE_MAP[numeric]!;
    if (mapped === 'CHAIN_MISMATCH') {
      return new BridgeError(
        'CHAIN_MISMATCH',
        msg || 'chain mismatch',
        e,
        'Wrong network — chain mismatch; switch to the expected Splendor network',
      );
    }
    if (mapped === 'INVALID_ADDRESS') {
      return new BridgeError(
        'INVALID_ADDRESS',
        msg || 'Invalid address',
        e,
        'Enter a valid SQ: address',
      );
    }
    if (mapped === 'NOT_CONNECTED') {
      return new BridgeError(
        'NOT_CONNECTED',
        msg || 'Not connected',
        e,
        /lock/i.test(msg)
          ? 'Wallet locked — unlock the Splendor Quantum Wallet'
          : 'Wallet is not connected — connect to continue',
      );
    }
    if (mapped === 'RPC' && msg && !/method not supported/i.test(msg)) {
      // The wallet rejected the request itself; its message is the actionable part.
      return new BridgeError('RPC', msg, e, `Wallet rejected the request: ${msg}`);
    }
    return factoryForCode(mapped, msg);
  }

  if (/abort|timeout/i.test(msg)) return BridgeError.timeout(msg);
  if (
    /EIP-1193 provider|window\.ethereum.*not found|provider.*not found|extension not found/i.test(
      msg,
    )
  ) {
    return BridgeError.noProvider(msg);
  }
  if (/user rejected|denied|4001/i.test(msg)) return BridgeError.userRejected(msg);
  if (/wallet locked|not connected|4100/i.test(msg)) {
    return new BridgeError(
      'NOT_CONNECTED',
      msg,
      e,
      /lock/i.test(msg)
        ? 'Wallet locked — unlock the Splendor Quantum Wallet'
        : 'Wallet is not connected — connect to continue',
    );
  }
  if (/chain.?mismatch|wrong network|4902/i.test(msg)) {
    return new BridgeError(
      'CHAIN_MISMATCH',
      msg,
      e,
      'Wrong network — chain mismatch; switch to the expected Splendor network',
    );
  }
  if (/invalid.?address|4900/i.test(msg)) {
    return new BridgeError('INVALID_ADDRESS', msg, e, 'Enter a valid SQ: address');
  }
  if (/invalid.?fee|4901/i.test(msg)) {
    return BridgeError.invalidFee(msg);
  }

  return factoryForCode(fallback, msg);
}

export function wrapUnknown(e: unknown, fallback: BridgeErrorCode = 'UNKNOWN'): BridgeError {
  return mapProviderError(e, fallback);
}
