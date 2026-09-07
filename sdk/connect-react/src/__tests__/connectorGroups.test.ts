import { describe, expect, it } from 'vitest';
import { groupConnectors, isNativeConnector } from '../ui/connectorGroups.js';
import { metamaskConnector, seedConnector } from '../connectors/index.js';
import type { Connector } from '../connectors/types.js';

const advancedConnector: Connector = {
  id: 'custom-advanced',
  name: 'Custom',
  advanced: true,
  keyOrigin: 'native',
  postQuantumSecure: true,
  ready: () => true,
  connect: async () => {
    throw new Error('unused');
  },
};

describe('groupConnectors', () => {
  it('puts the native connector first however the dApp ordered the array', () => {
    const groups = groupConnectors([metamaskConnector(), seedConnector()]);
    expect(groups.native.map((c) => c.id)).toEqual(['seed']);
    expect(groups.legacy.map((c) => c.id)).toEqual(['metamask']);
  });

  it('keeps advanced connectors out of both lists', () => {
    const groups = groupConnectors([advancedConnector, seedConnector()]);
    expect(groups.advanced.map((c) => c.id)).toEqual(['custom-advanced']);
    expect(groups.native.map((c) => c.id)).toEqual(['seed']);
    expect(groups.legacy).toEqual([]);
  });

  it('treats an undeclared connector as legacy', () => {
    // Failing closed matters more than being generous: a connector that says
    // nothing about its key origin must not sit in the post-quantum slot.
    const undeclared = { id: 'x', name: 'X', ready: () => true } as unknown as Connector;
    expect(isNativeConnector(undeclared)).toBe(false);
    expect(groupConnectors([undeclared]).legacy.map((c) => c.id)).toEqual(['x']);
  });
});
