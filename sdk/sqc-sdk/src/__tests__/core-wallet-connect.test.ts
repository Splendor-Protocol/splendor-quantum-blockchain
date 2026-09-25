/**
 * Core wallet-connect surface: probeNetwork, ExtensionSigner soft-rebind,
 * BridgeError.BUSY, address helpers — mocked RPC / mock provider (no Chrome).
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Connection } from '../core/connection.js';
import { SqRpc } from '../rpc.js';
import { CHAIN_ID } from '../derive.js';
import { BridgeError, isBridgeError } from '../errors.js';
import { ExtensionSigner } from '../wallet/extension-signer.js';
import {
  MODULE_ROUTER_ADDRESS,
  qrc20Address,
  qrc1155Address,
  bytecodeContractAddress,
  bridgeAddress,
  bridgeSplvAddress,
  dexPoolAddress,
  exchangeAddress,
  deriveModuleRouterAddress,
} from '../addresses/index.js';
import { isValidAddress, assertAddress, canonicalizeAddress } from '../address.js';
import { fromUnits, toUnits } from '../units.js';
import { fromSPLD, toSPLD } from '../core/connection.js';
import { toCanonicalMethod, toExtensionMethod } from '../wallet/extension-provider.js';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const ADDR = 'SQ:' + 'ab'.repeat(32);
const ADDR2 = 'SQ:' + 'cd'.repeat(32);

describe('BridgeError.BUSY', () => {
  it('constructs BUSY with userHint', () => {
    const e = BridgeError.busy();
    expect(e.code).toBe('BUSY');
    expect(isBridgeError(e)).toBe(true);
    expect(e.userHint).toMatch(/progress/i);
  });

  it('accepts custom message', () => {
    const e = BridgeError.busy('wait');
    expect(e.message).toBe('wait');
    expect(e.code).toBe('BUSY');
  });
});

describe('Connection.probeNetwork', () => {
  it('ok when chain id matches', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({ chain_id: CHAIN_ID })) as typeof fetch;
    const conn = new Connection({ rpcUrl: 'http://rpc.test', chainId: CHAIN_ID, retries: 0 });
    const probe = await conn.probeNetwork();
    expect(probe).toEqual({ ok: true, chainId: CHAIN_ID });
  });

  it('fails closed on chain mismatch', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({ chain_id: 'other-chain' })) as typeof fetch;
    const conn = new Connection({ rpcUrl: 'http://rpc.test', chainId: CHAIN_ID, retries: 0 });
    const probe = await conn.probeNetwork();
    expect(probe.ok).toBe(false);
    if (!probe.ok) {
      expect(probe.error.code).toBe('CHAIN_MISMATCH');
    }
  });

  it('fails when RPC is down', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('fetch failed');
    }) as typeof fetch;
    const conn = new Connection({ rpcUrl: 'http://rpc.test', chainId: CHAIN_ID, retries: 0 });
    const probe = await conn.probeNetwork();
    expect(probe.ok).toBe(false);
    if (!probe.ok) {
      expect(['NETWORK', 'RPC', 'RPC_UNAVAILABLE', 'TIMEOUT', 'UNKNOWN']).toContain(probe.error.code);
    }
  });
});

describe('SqRpc.probeNetwork', () => {
  it('ok when chain matches', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({ chain_id: CHAIN_ID })) as typeof fetch;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc.test', chainId: CHAIN_ID, retries: 0 });
    await expect(rpc.probeNetwork()).resolves.toEqual({ ok: true, chainId: CHAIN_ID });
  });

  it('returns ok:false on empty status body', async () => {
    globalThis.fetch = vi.fn(async () => jsonResponse({})) as typeof fetch;
    const rpc = new SqRpc({ rpcUrl: 'http://rpc.test', chainId: CHAIN_ID, retries: 0 });
    const probe = await rpc.probeNetwork();
    expect(probe.ok).toBe(false);
  });
});

describe('ExtensionSigner core', () => {
  const addr = ADDR2;

  function mockProvider(
    handlers: Record<string, (params?: unknown) => unknown> = {},
  ) {
    const listeners = new Map<string, Set<(...args: unknown[]) => void>>();
    return {
      isSplendor: true as const,
      request: vi.fn(async ({ method, params }: { method: string; params?: unknown }) => {
        if (handlers[method]) return handlers[method]!(params);
        if (method === 'sqc_requestAccounts' || method === 'splendor_connect') return [addr];
        if (method === 'sqc_accounts' || method === 'splendor_accounts') return [addr];
        if (method === 'sqc_disconnect' || method === 'splendor_disconnect' || method === 'wallet_disconnect')
          return null;
        if (method === 'sqc_sign' || method === 'splendor_signMessage') {
          return { address: addr, signatureB64: 'c2ln', publicKeyB64: 'cGs=', message: 'hi' };
        }
        throw new Error(`unexpected ${method}`);
      }),
      on(event: string, fn: (...args: unknown[]) => void) {
        if (!listeners.has(event)) listeners.set(event, new Set());
        listeners.get(event)!.add(fn);
      },
      removeListener(event: string, fn: (...args: unknown[]) => void) {
        listeners.get(event)?.delete(fn);
      },
      emit(event: string, ...args: unknown[]) {
        listeners.get(event)?.forEach((fn) => fn(...args));
      },
    };
  }

  function connection(rpcUrl = 'http://127.0.0.1:9') {
    return new Connection({ rpcUrl, chainId: CHAIN_ID, retries: 0 });
  }

  it('connect sets derive info and connection state', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    const info = await wallet.connect();
    expect(info.address).toBe(addr);
    expect(info.keyOrigin).toBe('native');
    expect(info.postQuantumSecure).toBe(true);
    expect(wallet.getConnectionState()).toBe('connected');
    expect(wallet.getDeriveInfo()?.address).toBe(addr);
    expect(wallet.isUnlocked()).toBe(true);
    wallet.disconnect();
  });

  it('rebindConnection updates RPC without splendor_disconnect', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({
      connection: connection('http://old.test'),
      provider,
      networkId: 'local',
    });
    await wallet.connect();
    const disconnectCallsBefore = provider.request.mock.calls.filter(
      (c) => String(c[0]?.method ?? '').includes('disconnect'),
    ).length;
    const next = connection('http://new.test');
    const chains: unknown[] = [];
    wallet.on('chainChanged', (p) => chains.push(p));
    wallet.rebindConnection(next, { networkId: 'testnet' });
    expect(wallet.connection).toBe(next);
    expect(wallet.getConnectionState()).toBe('connected');
    expect(wallet.getDeriveInfo()?.address).toBe(addr);
    expect(chains).toHaveLength(1);
    expect(chains[0]).toMatchObject({ networkId: 'testnet' });
    const disconnectCallsAfter = provider.request.mock.calls.filter(
      (c) => String(c[0]?.method ?? '').includes('disconnect'),
    ).length;
    expect(disconnectCallsAfter).toBe(disconnectCallsBefore);
    wallet.disconnect();
  });

  it('disconnect clears session and calls extension disconnect', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const events: string[] = [];
    wallet.on('disconnect', () => events.push('disconnect'));
    wallet.disconnect();
    expect(wallet.getConnectionState()).toBe('disconnected');
    expect(wallet.getDeriveInfo()).toBeNull();
    expect(events).toEqual(['disconnect']);
    expect(
      provider.request.mock.calls.some((c) =>
        /wallet_disconnect|sqc_disconnect/i.test(String(c[0]?.method ?? '')),
      ),
    ).toBe(true);
  });

  it('accountsChanged updates address', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const next = 'SQ:' + 'ef'.repeat(32);
    const seen: string[] = [];
    wallet.on('accountsChanged', (p) => seen.push(p.address));
    provider.emit('accountsChanged', [next]);
    expect(wallet.getDeriveInfo()?.address).toBe(next);
    expect(seen).toEqual([next]);
    wallet.disconnect();
  });

  it('empty accountsChanged disconnects', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const events: string[] = [];
    wallet.on('disconnect', () => events.push('disconnect'));
    provider.emit('accountsChanged', []);
    expect(wallet.getConnectionState()).toBe('disconnected');
    expect(events).toEqual(['disconnect']);
  });

  it('provider disconnect event clears info', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    provider.emit('disconnect');
    expect(wallet.getDeriveInfo()).toBeNull();
    expect(wallet.getConnectionState()).toBe('disconnected');
  });

  it('lock clears local view without extension disconnect method required', async () => {
    const provider = mockProvider();
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await wallet.connect();
    const locks: unknown[] = [];
    wallet.on('lock', (p) => locks.push(p));
    wallet.lock();
    expect(wallet.getDeriveInfo()).toBeNull();
    expect(locks).toHaveLength(1);
  });

  it('rejects connect when extension returns no accounts', async () => {
    const provider = mockProvider({
      sqc_requestAccounts: () => [],
      splendor_connect: () => [],
      sqc_accounts: () => [],
      splendor_accounts: () => [],
    });
    const wallet = new ExtensionSigner({ connection: connection(), provider, networkId: 'local' });
    await expect(wallet.connect()).rejects.toMatchObject({ code: 'USER_REJECTED' });
  });
});

describe('extension method aliases matrix', () => {
  it.each([
    ['sqc_accounts', 'splendor_accounts'],
    ['sqc_requestAccounts', 'splendor_connect'],
    ['sqc_sign', 'splendor_signMessage'],
    ['sqc_disconnect', 'splendor_disconnect'],
  ] as const)('toCanonicalMethod(%s) → %s', (from, to) => {
    expect(toCanonicalMethod(from)).toBe(to);
  });

  it.each([
    ['splendor_accounts', 'sqc_accounts'],
    ['splendor_connect', 'sqc_requestAccounts'],
    ['splendor_signMessage', 'sqc_sign'],
    ['splendor_disconnect', 'wallet_disconnect'],
  ] as const)('toExtensionMethod(%s) → %s', (from, to) => {
    expect(toExtensionMethod(from)).toBe(to);
  });
});

describe('address helpers (core)', () => {
  it('MODULE_ROUTER_ADDRESS matches deriveModuleRouterAddress', () => {
    expect(deriveModuleRouterAddress()).toBe(MODULE_ROUTER_ADDRESS);
  });

  it('qrc20Address is deterministic', () => {
    expect(qrc20Address(ADDR, 0)).toBe(qrc20Address(ADDR, 0));
    expect(qrc20Address(ADDR, 0)).not.toBe(qrc20Address(ADDR, 1));
    expect(qrc20Address(ADDR, 0)).toMatch(/^SQ:[0-9a-f]{64}$/);
  });

  it('qrc1155Address differs from qrc20 at same nonce', () => {
    expect(qrc1155Address(ADDR, 0)).not.toBe(qrc20Address(ADDR, 0));
  });

  it('bytecodeContractAddress is deterministic', () => {
    expect(bytecodeContractAddress(ADDR, 3)).toBe(bytecodeContractAddress(ADDR, 3));
  });

  it('bridgeAddress includes height', () => {
    expect(bridgeAddress(ADDR, 1, 10)).not.toBe(bridgeAddress(ADDR, 1, 11));
  });

  it('bridgeSplvAddress derives from bridge', () => {
    const b = bridgeAddress(ADDR, 0, 1);
    expect(bridgeSplvAddress(b)).toMatch(/^SQ:/);
  });

  it('dexPoolAddress is order-independent', () => {
    expect(dexPoolAddress(ADDR, ADDR2)).toBe(dexPoolAddress(ADDR2, ADDR));
  });

  it('exchangeAddress is deterministic', () => {
    expect(exchangeAddress(ADDR, 0, 5)).toBe(exchangeAddress(ADDR, 0, 5));
  });
});

describe('SQ address validation', () => {
  it.each([
    [ADDR, true],
    [ADDR2, true],
    ['SQ:' + 'ff'.repeat(32), true],
    ['0x' + 'ab'.repeat(20), false],
    ['SQ:short', false],
    ['', false],
    ['sq:' + 'ab'.repeat(32), false],
  ] as const)('isValidAddress(%s) → %s', (input, ok) => {
    expect(isValidAddress(input)).toBe(ok);
  });

  it('assertAddress accepts canonical SQ', () => {
    expect(assertAddress(ADDR)).toBe(ADDR);
  });

  it('assertAddress rejects EVM', () => {
    expect(() => assertAddress('0x' + '11'.repeat(20))).toThrow();
  });

  it('canonicalizeAddress lowercases hex body', () => {
    const upper = 'SQ:' + 'AB'.repeat(32);
    expect(canonicalizeAddress(upper)).toBe('SQ:' + 'ab'.repeat(32));
  });
});

describe('units / SPLD conversion matrix', () => {
  it.each([
    ['0', '0'],
    ['1', '1000000000000000000'],
    ['0.1', '100000000000000000'],
    ['1.5', '1500000000000000000'],
  ] as const)('fromSPLD(%s)', (human, atto) => {
    expect(fromSPLD(human)).toBe(atto);
    expect(fromSPLD(toSPLD(atto))).toBe(atto);
  });

  it('fromSPLD accepts 1 atto as human dust', () => {
    expect(fromSPLD('0.000000000000000001')).toBe('1');
  });

  it.each([
    ['1', 6, '1000000'],
    ['1.5', 6, '1500000'],
    ['0.000001', 6, '1'],
    ['100', 0, '100'],
  ] as const)('fromUnits(%s, %i)', (human, dec, raw) => {
    expect(fromUnits(human, dec)).toBe(raw);
    expect(toUnits(raw, dec).startsWith(human.split('.')[0]!)).toBe(true);
  });

  it.each(['', 'abc', '1e18', '-1', '1.', '.1', '01', '1.2.3'] as const)(
    'fromUnits rejects %s',
    (bad) => {
      expect(() => fromUnits(bad, 18)).toThrow();
    },
  );
});
