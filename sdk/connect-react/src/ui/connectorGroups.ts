import type { Connector } from '../connectors/types.js';

export type ConnectorGroups = {
  /** Splendor PQ wallets — extension + native seed (what the modal offers). */
  native: Connector[];
  /**
   * @deprecated MetaMask / ECDSA-derived paths removed. Always empty from
   * default connectors; non-native entries are dropped (not shown).
   */
  legacy: Connector[];
  /** Hidden behind the Advanced accordion (dev / agent connectors). */
  advanced: Connector[];
};

/**
 * Only native post-quantum connectors (Splendor extension / in-page seed).
 */
export function isNativeConnector(c: Connector): boolean {
  return c.keyOrigin === 'native' && c.postQuantumSecure !== false;
}

/**
 * Split connectors for the modal. Non-native (legacy EVM) connectors are
 * dropped — ConnectKit only supports Splendor Quantum Wallet + native seed.
 */
export function groupConnectors(connectors: Connector[]): ConnectorGroups {
  const groups: ConnectorGroups = { native: [], legacy: [], advanced: [] };
  for (const c of connectors) {
    if (c.advanced) {
      if (isNativeConnector(c)) groups.advanced.push(c);
      continue;
    }
    if (isNativeConnector(c)) groups.native.push(c);
    // Non-native / MetaMask-style connectors intentionally omitted.
  }
  return groups;
}
