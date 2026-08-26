import type { Connector } from '../connectors/types.js';

export type ConnectorGroups = {
  /** Native ML-DSA-65 wallets — what the modal offers first. */
  native: Connector[];
  /** Offered after the native path, under a "Legacy" heading. */
  legacy: Connector[];
  /** Hidden behind the Advanced accordion (dev / agent connectors). */
  advanced: Connector[];
};

/**
 * Only a connector that declares native post-quantum entropy earns the first
 * slot. Anything else — including a third-party connector that declares
 * nothing — falls to legacy, so an account is never presented as quantum-safe
 * on the strength of an omission.
 */
export function isNativeConnector(c: Connector): boolean {
  return c.keyOrigin === 'native' && c.postQuantumSecure !== false;
}

/**
 * Split connectors for the modal. Grouping (not array position) decides the
 * order, so a dApp that lists MetaMask first still gets the post-quantum path
 * on top.
 */
export function groupConnectors(connectors: Connector[]): ConnectorGroups {
  const groups: ConnectorGroups = { native: [], legacy: [], advanced: [] };
  for (const c of connectors) {
    if (c.advanced) groups.advanced.push(c);
    else if (isNativeConnector(c)) groups.native.push(c);
    else groups.legacy.push(c);
  }
  return groups;
}
