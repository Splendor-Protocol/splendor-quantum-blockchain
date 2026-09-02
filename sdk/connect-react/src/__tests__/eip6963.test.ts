import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { discoverInjectedConnectors } from '../connectors/eip6963.js';
import type { EthereumProvider } from '@splendor/sqc-sdk';

describe('discoverInjectedConnectors', () => {
  beforeEach(() => {
    vi.stubGlobal('window', {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('returns connectors from eip6963 announce events', async () => {
    const provider: EthereumProvider = { request: vi.fn() };
    const listeners = new Map<string, Set<(e: Event) => void>>();

    const win = {
      addEventListener: (type: string, fn: (e: Event) => void) => {
        if (!listeners.has(type)) listeners.set(type, new Set());
        listeners.get(type)!.add(fn);
      },
      removeEventListener: (type: string, fn: (e: Event) => void) => {
        listeners.get(type)?.delete(fn);
      },
      dispatchEvent: (event: Event) => {
        if (event.type === 'eip6963:requestProvider') {
          const detail = {
            info: {
              uuid: 'rabby-1',
              name: 'Rabby',
              icon: 'data:image/svg+xml,',
              rdns: 'io.rabby',
            },
            provider,
          };
          const ev = new CustomEvent('eip6963:announceProvider', { detail });
          listeners.get('eip6963:announceProvider')?.forEach((fn) => fn(ev));
        }
        return true;
      },
    };
    vi.stubGlobal('window', win);

    const connectors = await discoverInjectedConnectors({ timeoutMs: 10 });
    expect(connectors.length).toBe(1);
    expect(connectors[0].name).toBe('Rabby');
    expect(connectors[0].id).toContain('eip6963:');
  });

  it('skips MetaMask rdns by default', async () => {
    const provider: EthereumProvider = { request: vi.fn() };
    const listeners = new Map<string, Set<(e: Event) => void>>();
    vi.stubGlobal('window', {
      addEventListener: (type: string, fn: (e: Event) => void) => {
        if (!listeners.has(type)) listeners.set(type, new Set());
        listeners.get(type)!.add(fn);
      },
      removeEventListener: (type: string, fn: (e: Event) => void) => {
        listeners.get(type)?.delete(fn);
      },
      dispatchEvent: (event: Event) => {
        if (event.type === 'eip6963:requestProvider') {
          const detail = {
            info: {
              uuid: 'mm-1',
              name: 'MetaMask',
              icon: '',
              rdns: 'io.metamask',
            },
            provider,
          };
          listeners
            .get('eip6963:announceProvider')
            ?.forEach((fn) => fn(new CustomEvent('eip6963:announceProvider', { detail })));
        }
        return true;
      },
    });

    const connectors = await discoverInjectedConnectors({ timeoutMs: 10 });
    expect(connectors.length).toBe(0);
  });
});
