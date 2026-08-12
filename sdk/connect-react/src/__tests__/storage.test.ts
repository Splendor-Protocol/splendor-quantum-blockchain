import { describe, expect, it, beforeEach, vi } from 'vitest';
import { getLastConnectorId, setLastConnectorId } from '../core/storage.js';

const memoryStore = new Map<string, string>();

vi.stubGlobal('sessionStorage', {
  getItem: (k: string) => memoryStore.get(k) ?? null,
  setItem: (k: string, v: string) => {
    memoryStore.set(k, v);
  },
  removeItem: (k: string) => {
    memoryStore.delete(k);
  },
  clear: () => memoryStore.clear(),
});

describe('storage', () => {
  beforeEach(() => {
    memoryStore.clear();
  });

  it('get/set/remove last connector id', () => {
    expect(getLastConnectorId()).toBeNull();
    setLastConnectorId('metamask');
    expect(getLastConnectorId()).toBe('metamask');
    setLastConnectorId(null);
    expect(getLastConnectorId()).toBeNull();
  });
});
