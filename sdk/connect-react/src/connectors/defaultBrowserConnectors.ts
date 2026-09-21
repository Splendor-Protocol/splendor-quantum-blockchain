import { seedConnector } from './seed.js';
import { extensionConnector } from './extension.js';
import type { Connector } from './types.js';

/**
 * Native ML-DSA paths only: Splendor extension (when installed) + in-page seed.
 * MetaMask / EIP-6963 removed — ECDSA-derived accounts are no longer offered.
 */
export async function defaultBrowserConnectors(opts?: {
  /** Drop the native seed connector. */
  includeSeed?: boolean;
  /** Drop the extension connector. */
  includeExtension?: boolean;
}): Promise<Connector[]> {
  const list: Connector[] = [];
  if (opts?.includeExtension !== false) {
    list.push(extensionConnector());
  }
  if (opts?.includeSeed !== false) {
    list.push(seedConnector());
  }
  return list;
}
