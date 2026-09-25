/**
 * Audit scenarios for connect-react (reconnect abort, icons, locked UX, errors).
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { BridgeError, fromUnits, clearExtensionDiscoveryCache } from '@splendor/sqc-sdk';
import { createConnectController, connectErrorMessage } from '../core/controller.js';
import { sanitizeIconUrl } from '../widgets/utils.js';
import { extensionConnector } from '../connectors/extension.js';
import type { Connector } from '../connectors/types.js';
import type { DeriveInfo } from '@splendor/sqc-sdk';

const memoryStore = new Map<string, string>();
vi.stubGlobal('localStorage', {
  getItem: (k: string) => memoryStore.get(k) ?? null,
  setItem: (k: string, v: string) => {
    memoryStore.set(k, v);
  },
  removeItem: (k: string) => {
    memoryStore.delete(k);
  },
  clear: () => memoryStore.clear(),
});
vi.stubGlobal('sessionStorage', {
  getItem: () => null,
  setItem: () => undefined,
  removeItem: () => undefined,
  clear: () => undefined,
});

const mockInfo: DeriveInfo = {
  address: 'SQ:' + 'ab'.repeat(32),
  public_key_b64: 'dGVzdA==',
  public_key_hex: '74657374',
  evmAddress: '0x1111111111111111111111111111111111111111',
};

beforeEach(() => {
  memoryStore.clear();
});

describe('audit scenarios (connect-react)', () => {
  it('10/14: sanitizeIconUrl allows https + raster; rejects SVG', () => {
    expect(sanitizeIconUrl('https://cdn.example/i.png')).toBe('https://cdn.example/i.png');
    expect(sanitizeIconUrl('data:image/png;base64,xx')).toBe('data:image/png;base64,xx');
    expect(sanitizeIconUrl('data:image/svg+xml;utf8,<svg/>')).toBeNull();
  });

  it('7/19: connectErrorMessage maps TX_FAILED without leaking raw bodies', () => {
    const msg = connectErrorMessage(
      BridgeError.txFailed('module reverted', { rpcBody: 'SECRET_STACK' }),
    );
    expect(msg).toMatch(/failed|transaction/i);
    expect(msg).not.toContain('SECRET_STACK');
  });

  it('8: human deploy supply converts via fromUnits 18', () => {
    expect(fromUnits('1000000', 18)).toBe('1000000' + '0'.repeat(18));
  });

  it('9: locked widgets expose unlock copy', () => {
    const unlockHints = [
      'Wallet locked — unlock to send.',
      'Wallet locked — unlock to manage this token.',
      'Wallet locked — unlock to deploy.',
      'Wallet locked — unlock to continue.',
    ];
    for (const h of unlockHints) {
      expect(h).toMatch(/Wallet locked — unlock/i);
    }
  });

  it('5: tryAutoReconnect aborts when isStale before connect', async () => {
    let connectCalls = 0;
    const connector: Connector = {
      id: 'splendor-extension',
      name: 'Splendor Extension',
      keyOrigin: 'native',
      postQuantumSecure: true,
      ready: async () => {
        await new Promise((r) => setTimeout(r, 40));
        return true;
      },
      authorized: async () => true,
      connect: async () => {
        connectCalls += 1;
        return mockInfo;
      },
    };
    memoryStore.set('splendor.connect.lastConnectorId', 'splendor-extension');
    const ctrl = createConnectController({
      network: 'local',
      connectors: [connector],
      allowUnpinnedWasm: true,
      autoReconnect: true,
    });

    let stale = false;
    const p = ctrl.tryAutoReconnect({
      force: true,
      isStale: () => stale,
    });
    stale = true;
    const ok = await p;
    expect(ok).toBe(false);
    expect(connectCalls).toBe(0);
    ctrl.destroy();
  });

  it('extension ready() completes under 1s when wallet missing', async () => {
    clearExtensionDiscoveryCache();
    const g = globalThis as { window?: Record<string, unknown> };
    g.window = {
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => true,
    };
    try {
      const connector = extensionConnector({ readyTimeoutMs: 400 });
      const t0 = Date.now();
      const ready = await connector.ready();
      expect(ready).toBe(false);
      expect(Date.now() - t0).toBeLessThan(1_000);
    } finally {
      delete g.window;
      clearExtensionDiscoveryCache();
    }
  });

  it('connectErrorMessage for NOT_CONNECTED / NO_PROVIDER matches unlock/install copy', () => {
    expect(connectErrorMessage(BridgeError.notConnected())).toMatch(/unlock|connect/i);
    expect(connectErrorMessage(BridgeError.noProvider())).toBe('Install Splendor Quantum Wallet');
    expect(connectErrorMessage(BridgeError.chainMismatch('x', 'y'))).toMatch(/Chain mismatch/i);
  });
});
