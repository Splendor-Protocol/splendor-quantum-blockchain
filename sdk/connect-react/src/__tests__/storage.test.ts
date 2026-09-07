import { describe, expect, it, beforeEach, vi } from 'vitest';
import { getLastConnectorId, setLastConnectorId, getLastNetworkId, setLastNetworkId } from '../core/storage.js';

const localStore = new Map<string, string>();
const sessionStore = new Map<string, string>();

function makeStorage(map: Map<string, string>) {
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => {
      map.set(k, v);
    },
    removeItem: (k: string) => {
      map.delete(k);
    },
    clear: () => map.clear(),
  };
}

vi.stubGlobal('localStorage', makeStorage(localStore));
vi.stubGlobal('sessionStorage', makeStorage(sessionStore));

describe('storage', () => {
  beforeEach(() => {
    localStore.clear();
    sessionStore.clear();
  });

  it('get/set/remove last connector id in localStorage', () => {
    expect(getLastConnectorId()).toBeNull();
    setLastConnectorId('metamask');
    expect(getLastConnectorId()).toBe('metamask');
    expect(localStore.get('splendor.connect.lastConnectorId')).toBe('metamask');
    setLastConnectorId(null);
    expect(getLastConnectorId()).toBeNull();
  });

  it('migrates sessionStorage → localStorage', () => {
    sessionStore.set('splendor.connect.lastConnectorId', 'injected');
    expect(getLastConnectorId()).toBe('injected');
    expect(localStore.get('splendor.connect.lastConnectorId')).toBe('injected');
    expect(sessionStore.has('splendor.connect.lastConnectorId')).toBe(false);
  });

  it('get/set last network id', () => {
    expect(getLastNetworkId()).toBeNull();
    setLastNetworkId('testnet');
    expect(getLastNetworkId()).toBe('testnet');
    expect(localStore.get('splendor.connect.lastNetworkId')).toBe('testnet');
    setLastNetworkId(null);
    expect(getLastNetworkId()).toBeNull();
  });
});
