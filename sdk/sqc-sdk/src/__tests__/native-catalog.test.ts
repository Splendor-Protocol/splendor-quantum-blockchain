import { afterEach, describe, expect, it } from 'vitest';
import { listModules } from '../core/module-registry.js';
import {
  ROUTED_NATIVE_MODULE_IDS,
  UNSUPPORTED_ADAPTER_MESSAGE,
  _resetUnsupportedAdaptersForTests,
  ensureBuiltInModulesRegistered,
  modules,
  registerUnsupportedApplicationAdapters,
  unsupportedApplicationAdaptersEnabled,
} from '../modules.js';
import type { Connection } from '../core/connection.js';

const ADDR = 'SQ:' + 'aa'.repeat(32);

function fakeConnection(): Connection {
  return { chainId: 'splendor-quantum-1', moduleCall: async () => ({}) } as unknown as Connection;
}

describe('routed native catalog vs unsupported adapters', () => {
  afterEach(() => {
    _resetUnsupportedAdaptersForTests();
    ensureBuiltInModulesRegistered();
  });

  it('auto-registers only routed Program clients', () => {
    expect(listModules().map((m) => m.id).sort()).toEqual(['bridge', 'contract', 'qrc1155', 'qrc20']);
    expect(ROUTED_NATIVE_MODULE_IDS).toEqual([
      'qrc20',
      'qrc1155',
      'bridge',
      'verify',
      'validators',
      'qrc721',
    ]);
    expect(unsupportedApplicationAdaptersEnabled()).toBe(false);
  });

  it('gates leftover DEX/exchange factories until opt-in', () => {
    const conn = fakeConnection();
    expect(() => modules.dex(conn, ADDR)).toThrow(UNSUPPORTED_ADAPTER_MESSAGE);
    expect(() => modules.exchange(conn, ADDR)).toThrow(UNSUPPORTED_ADAPTER_MESSAGE);

    registerUnsupportedApplicationAdapters();
    expect(unsupportedApplicationAdaptersEnabled()).toBe(true);
    expect(modules.dex(conn, ADDR).getAddress()).toBe(ADDR);
    expect(modules.exchange(conn, ADDR).getAddress()).toBe(ADDR);
    expect(listModules().map((m) => m.id)).toEqual(
      expect.arrayContaining(['dex', 'exchange', 'qrc20']),
    );
  });
});
