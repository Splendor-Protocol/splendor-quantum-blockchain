import { describe, expect, it } from 'vitest';
import {
  isPlaceholderNetwork,
  listSelectableNetworks,
  getNetwork,
  listNetworks,
} from '../networks.js';

describe('isPlaceholderNetwork / listSelectableNetworks', () => {
  it('detects placeholder URLs on testnet/mainnet config', () => {
    const testnet = getNetwork('testnet');
    expect(testnet).toBeTruthy();
    expect(isPlaceholderNetwork(testnet!)).toBe(true);
    expect(isPlaceholderNetwork('testnet')).toBe(true);
  });

  it('local is not treated as placeholder for picker', () => {
    const local = getNetwork('local');
    expect(local).toBeTruthy();
    expect(isPlaceholderNetwork(local!)).toBe(false);
  });

  it('listSelectableNetworks excludes placeholders by default', () => {
    const selectable = listSelectableNetworks();
    expect(selectable.every((n) => n.id === 'local' || !isPlaceholderNetwork(n))).toBe(true);
    expect(selectable.some((n) => n.id === 'local')).toBe(true);
  });

  it('listSelectableNetworks can include placeholders', () => {
    const all = listSelectableNetworks({ allowPlaceholder: true });
    expect(all.length).toBe(listNetworks().length);
  });
});
