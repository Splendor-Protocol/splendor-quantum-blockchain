/**
 * Domain separation for arbitrary wallet.signMessage() payloads.
 * Only already-prefixed messages and fully parsed Connect v2 challenges are left unchanged.
 */
import { parseAuthMessage } from './auth-message.js';

/** Prefix applied to unstructured signMessage payloads (phishing hardening). */
export const SIGN_MESSAGE_DOMAIN = 'SPLENDOR-QC-MSG-v1';

/**
 * Prepare a message for ML-DSA signing / verification.
 * A message that merely starts with `Splendor Connect v2` but does not parse as a complete
 * challenge is prefixed like any other text, so it cannot pass as a sign-in.
 */
export function prepareSignMessage(message: string, opts?: { raw?: boolean }): string {
  if (opts?.raw) return message;
  if (message.startsWith(`${SIGN_MESSAGE_DOMAIN}\n`)) return message;
  if (parseAuthMessage(message)) return message;
  return `${SIGN_MESSAGE_DOMAIN}\n${message}`;
}
