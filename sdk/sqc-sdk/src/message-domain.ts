/**
 * Domain separation for arbitrary wallet.signMessage() payloads.
 * Connect v2 challenges and already-prefixed messages are left unchanged.
 */

/** Prefix applied to unstructured signMessage payloads (phishing hardening). */
export const SIGN_MESSAGE_DOMAIN = 'SPLENDOR-QC-MSG-v1';

const KNOWN_PREFIXES = [
  `${SIGN_MESSAGE_DOMAIN}\n`,
  'Splendor Connect v1',
  'Splendor Connect v2',
] as const;

/**
 * Prepare a message for ML-DSA signing / verification.
 * Structured protocol messages (Connect challenges, already-prefixed) pass through.
 */
export function prepareSignMessage(message: string, opts?: { raw?: boolean }): string {
  if (opts?.raw) return message;
  for (const p of KNOWN_PREFIXES) {
    if (message.startsWith(p)) return message;
  }
  return `${SIGN_MESSAGE_DOMAIN}\n${message}`;
}
