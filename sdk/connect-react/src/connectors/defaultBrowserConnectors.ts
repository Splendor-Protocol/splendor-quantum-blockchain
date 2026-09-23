import { seedConnector } from './seed.js';
import { extensionConnector } from './extension.js';
import type { Connector } from './types.js';

/**
 * Splendor PQ only: Quantum Wallet extension (when installed) + in-page seed.
 * No MetaMask / EIP-6963 / WalletConnect.
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
