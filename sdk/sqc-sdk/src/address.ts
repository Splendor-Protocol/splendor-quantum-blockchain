/** SQ address validation helpers (SQDP-1). */
import { BridgeError } from './errors.js';
import { ADDRESS_PATTERN, ADDRESS_PREFIX, ADDRESS_HEX_LEN } from './protocol.js';

export function isValidAddress(addr: string): boolean {
  return typeof addr === 'string' && ADDRESS_PATTERN.test(addr);
}

/**
 * Validate and return canonical SQ address: `SQ:` + lowercase hex.
 * dApps MUST use the `SQ:` form (raw 64-hex alone is not a dApp API).
 */
export function assertAddress(addr: string): string {
  if (!isValidAddress(addr)) {
    throw BridgeError.invalidAddress(addr);
  }
  return ADDRESS_PREFIX + addr.slice(ADDRESS_PREFIX.length).toLowerCase();
}

/** Normalize a valid address to canonical form; returns null if invalid. */
export function canonicalizeAddress(addr: string): string | null {
  if (!isValidAddress(addr)) return null;
  return ADDRESS_PREFIX + addr.slice(ADDRESS_PREFIX.length).toLowerCase();
}
