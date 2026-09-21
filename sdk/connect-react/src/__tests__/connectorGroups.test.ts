import { describe, expect, it } from 'vitest';
import { groupConnectors, isNativeConnector } from '../ui/connectorGroups.js';
import { extensionConnector, seedConnector } from '../connectors/index.js';

describe('groupConnectors', () => {
  it('puts seed and extension in native', () => {
    const groups = groupConnectors([extensionConnector(), seedConnector()]);
    expect(groups.native.map((c) => c.id).sort()).toEqual(['seed', 'splendor-extension'].sort());
    expect(groups.legacy).toEqual([]);
  });

  it('isNativeConnector accepts seed', () => {
    expect(isNativeConnector(seedConnector())).toBe(true);
    expect(isNativeConnector(extensionConnector())).toBe(true);
  });
});
