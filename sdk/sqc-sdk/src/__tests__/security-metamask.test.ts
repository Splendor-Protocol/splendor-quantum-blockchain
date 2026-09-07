/**
 * SQDP-1 security: MetaMask accountsChanged fail-closed + USER_REJECTED on connect.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { SplendorPQBridge } from '../index.js';
import {
  signDerivationMessage,
  evmAddressFromPrivateKey,
  DERIVATION_DOMAIN_MESSAGE,
} from '../derive.js';
import { BridgeError, isBridgeError } from '../errors.js';
import type { EthereumProvider } from '../metamask.js';

const KEY_A = '55'.repeat(32);
const KEY_B = '66'.repeat(32);
const ADDR_A = evmAddressFromPrivateKey(KEY_A);
const ADDR_B = evmAddressFromPrivateKey(KEY_B);

const bridges: SplendorPQBridge[] = [];

afterEach(() => {
  for (const b of bridges) {
    try {
      b.disconnect();
    } catch {
      /* ignore */
    }
  }
  bridges.length = 0;
  delete (globalThis as { ethereum?: unknown }).ethereum;
});

function track(b: SplendorPQBridge) {
  bridges.push(b);
  return b;
}

function makeMockProvider(opts: {
  accounts: string[];
  signKeyByAddress?: Record<string, string>;
  rejectSign?: boolean;
  rejectConnect?: boolean;
}): EthereumProvider & {
  emitAccountsChanged: (accounts: string[]) => void;
  emitDisconnect: () => void;
} {
  const listeners = new Map<string, Set<(...args: unknown[]) => void>>();
  return {
    request: async ({ method, params }) => {
      if (method === 'eth_requestAccounts') {
        if (opts.rejectConnect) {
          throw new Error('User rejected the request');
        }
        return opts.accounts;
      }
      if (method === 'personal_sign') {
        if (opts.rejectSign) {
          throw new Error('User rejected the request');
        }
        const address = String(params?.[1] || opts.accounts[0]).toLowerCase();
        const key =
          opts.signKeyByAddress?.[address] ||
          opts.signKeyByAddress?.[Object.keys(opts.signKeyByAddress || {})[0]];
        if (!key) throw new Error('no key for address');
        void DERIVATION_DOMAIN_MESSAGE;
        return '0x' + signDerivationMessage(key);
      }
      throw new Error(`unexpected method ${method}`);
    },
    on: (event, handler) => {
      if (!listeners.has(event)) listeners.set(event, new Set());
      listeners.get(event)!.add(handler);
    },
    removeListener: (event, handler) => {
      listeners.get(event)?.delete(handler);
    },
    emitAccountsChanged(accounts: string[]) {
      for (const h of listeners.get('accountsChanged') || []) {
        h(accounts);
      }
    },
    emitDisconnect() {
      for (const h of listeners.get('disconnect') || []) {
        h();
      }
    },
  };
}

describe('security-metamask', () => {
  it('connectFromMetaMask maps user reject to USER_REJECTED', async () => {
    const provider = makeMockProvider({
      accounts: [ADDR_A],
      rejectConnect: true,
    });
    const b = track(new SplendorPQBridge({ ethereumProvider: provider }));
    try {
      await b.connectFromMetaMask(provider);
      expect.fail('expected rejection');
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('USER_REJECTED');
    }
  }, 30_000);

  it('accountsChanged to empty destroys session (full disconnect)', async () => {
    const provider = makeMockProvider({
      accounts: [ADDR_A],
      signKeyByAddress: { [ADDR_A.toLowerCase()]: KEY_A },
    });
    const b = track(
      new SplendorPQBridge({
        ethereumProvider: provider,
        watchMetaMaskAccounts: true,
      }),
    );
    await b.connectFromMetaMask(provider);
    expect(b.isUnlocked()).toBe(true);

    let disconnected = false;
    b.on('disconnect', () => {
      disconnected = true;
    });

    provider.emitAccountsChanged([]);
    await new Promise((r) => setTimeout(r, 50));
    expect(b.isUnlocked()).toBe(false);
    expect(b.getConnectionState()).toBe('disconnected');
    expect(disconnected).toBe(true);
    expect(() => b.getAddress()).toThrow();
  }, 30_000);

  it('EIP-1193 disconnect destroys session', async () => {
    const provider = makeMockProvider({
      accounts: [ADDR_A],
      signKeyByAddress: { [ADDR_A.toLowerCase()]: KEY_A },
    });
    const b = track(
      new SplendorPQBridge({
        ethereumProvider: provider,
        watchMetaMaskAccounts: true,
      }),
    );
    await b.connectFromMetaMask(provider);
    expect(b.isUnlocked()).toBe(true);

    let disconnected = false;
    b.on('disconnect', () => {
      disconnected = true;
    });

    provider.emitDisconnect();
    await new Promise((r) => setTimeout(r, 50));
    expect(b.isUnlocked()).toBe(false);
    expect(b.getConnectionState()).toBe('disconnected');
    expect(disconnected).toBe(true);
  }, 30_000);

  it('accountsChanged re-sign failure fails closed', async () => {
    let signCalls = 0;
    const listeners = new Map<string, Set<(...args: unknown[]) => void>>();
    const provider: EthereumProvider & { emitAccountsChanged: (a: string[]) => void } = {
      request: async ({ method, params }) => {
        if (method === 'eth_requestAccounts') return [ADDR_A];
        if (method === 'personal_sign') {
          signCalls += 1;
          if (signCalls === 1) {
            return '0x' + signDerivationMessage(KEY_A);
          }
          throw new Error('User rejected the request');
        }
        void params;
        throw new Error(`unexpected ${method}`);
      },
      on: (event, handler) => {
        if (!listeners.has(event)) listeners.set(event, new Set());
        listeners.get(event)!.add(handler);
      },
      removeListener: (event, handler) => {
        listeners.get(event)?.delete(handler);
      },
      emitAccountsChanged(accounts: string[]) {
        for (const h of listeners.get('accountsChanged') || []) h(accounts);
      },
    };

    const b = track(
      new SplendorPQBridge({
        ethereumProvider: provider,
        watchMetaMaskAccounts: true,
      }),
    );
    await b.connectFromMetaMask(provider);
    expect(b.isUnlocked()).toBe(true);

    let sawError = false;
    let disconnected = false;
    b.on('error', () => {
      sawError = true;
    });
    b.on('disconnect', () => {
      disconnected = true;
    });

    provider.emitAccountsChanged([ADDR_B]);
    await new Promise((r) => setTimeout(r, 100));
    expect(b.isUnlocked()).toBe(false);
    expect(sawError).toBe(true);
    expect(disconnected).toBe(true);
  }, 30_000);

  it('connectFromMetaMask maps missing provider to NO_PROVIDER', async () => {
    delete (globalThis as { ethereum?: unknown }).ethereum;
    const b = track(
      new SplendorPQBridge({
        rpcUrl: 'http://127.0.0.1:1',
        allowUnpinnedWasm: true,
        networkId: 'local',
      }),
    );
    try {
      await b.connectFromMetaMask();
      expect.fail('expected throw');
    } catch (e) {
      expect(isBridgeError(e)).toBe(true);
      expect((e as BridgeError).code).toBe('NO_PROVIDER');
    }
  });
});
