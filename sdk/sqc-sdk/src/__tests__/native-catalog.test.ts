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

  it('keeps DEX/exchange off public modules; opt-in only via registry', () => {
    const conn = fakeConnection();
    expect(modules).not.toHaveProperty('dex');
    expect(modules).not.toHaveProperty('exchange');
    expect(UNSUPPORTED_ADAPTER_MESSAGE).toMatch(/@splendor\/sqc-sdk\/defi/);

    registerUnsupportedApplicationAdapters();
    expect(unsupportedApplicationAdaptersEnabled()).toBe(true);
    expect(listModules().map((m) => m.id)).toEqual(
      expect.arrayContaining(['dex', 'exchange', 'qrc20']),
    );
    const bound = modules.bind(conn);
    expect(bound).not.toHaveProperty('dex');
    expect((bound.registered.dex(ADDR) as { getAddress(): string }).getAddress()).toBe(ADDR);
    expect((bound.registered.exchange(ADDR) as { getAddress(): string }).getAddress()).toBe(ADDR);
  });
});
