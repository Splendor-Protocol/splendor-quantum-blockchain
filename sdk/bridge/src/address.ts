/** SQ address validation helpers. */
import { BridgeError } from './errors.js';

const SQ_RE = /^SQ:[0-9a-fA-F]{64}$/;

export function isValidAddress(addr: string): boolean {
  return typeof addr === 'string' && SQ_RE.test(addr);
}

export function assertAddress(addr: string): string {
  if (!isValidAddress(addr)) {
    throw BridgeError.invalidAddress(addr);
  }
  return addr;
}
