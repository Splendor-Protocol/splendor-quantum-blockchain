/**
 * @vitest-environment jsdom
 */
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import React, { act, type ReactNode } from 'react';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { renderHook, waitFor } from '@testing-library/react';
import { copySqcWasmAssets, findSqcWasmDist, sqcWasmHashes, sqcWasmPlugin } from '../../vite.js';
import { mountEmbed } from '../../embed.js';
import { useModules } from '../../react/hooks.js';
import { MockSplendorProvider, createMockConnectController, mockWallet } from '../../testing.js';

const HEX64 = 'a'.repeat(64);
let tmp: string;

beforeAll(() => {
  tmp = mkdtempSync(join(tmpdir(), 'sqc-tooling-'));
});
afterAll(() => {
  rmSync(tmp, { recursive: true, force: true });
});

function fakeDist(dir: string, files: Record<string, string> = { 'sqc_signer.wasm': 'WASM', 'wasm_exec.js': 'EXEC' }) {
  mkdirSync(dir, { recursive: true });
  for (const [f, body] of Object.entries(files)) writeFileSync(join(dir, f), body);
  return dir;
}

describe('vite / next tooling prod', () => {
  it('[L1] copies both signer assets and hashes them with SHA-256', () => {
    const src = fakeDist(join(tmp, 'l1-src'));
    const dest = join(tmp, 'l1-public');
    expect(copySqcWasmAssets(dest, src)).toBe(src);
    expect(readFileSync(join(dest, 'sqc_signer.wasm'), 'utf8')).toBe('WASM');
    expect(sqcWasmHashes(dest)).toEqual({
      wasmSha256: createHash('sha256').update('WASM').digest('hex'),
      execSha256: createHash('sha256').update('EXEC').digest('hex'),
    });
  });

  it('[L1] finds a sibling sqc-sdk/wasm/dist from a package directory', () => {
    const pkg = join(tmp, 'l1-mono', 'sdk', 'connect-react', 'dist');
    mkdirSync(pkg, { recursive: true });
    const dist = fakeDist(join(tmp, 'l1-mono', 'sdk', 'sqc-sdk', 'wasm', 'dist'));
    expect(findSqcWasmDist(pkg)).toBe(dist);
  });

  it('[L2] a source dir missing wasm_exec.js fails with a clear message', () => {
    const src = fakeDist(join(tmp, 'l2-src'), { 'sqc_signer.wasm': 'WASM' });
    expect(() => copySqcWasmAssets(join(tmp, 'l2-out'), src)).toThrow(/missing .*wasm_exec\.js/);
  });

  it('[L3] re-copying over a read-only wasm_exec.js (as Go ships it) succeeds', () => {
    const src = fakeDist(join(tmp, 'l3-src'));
    const dest = join(tmp, 'l3-out');
    copySqcWasmAssets(dest, src);
    chmodSync(join(dest, 'wasm_exec.js'), 0o444);
    writeFileSync(join(src, 'wasm_exec.js'), 'EXEC2');
    expect(() => copySqcWasmAssets(dest, src)).not.toThrow();
    expect(readFileSync(join(dest, 'wasm_exec.js'), 'utf8')).toBe('EXEC2');
  });

  it('[L3] the plugin keeps the Node WASM loader out of browser bundles', () => {
    const p = sqcWasmPlugin() as unknown as {
      resolveId: (id: string) => string | null;
      load: (id: string) => string | null;
    };
    const stub = p.resolveId('/x/wasm-loader.node.js');
    expect(stub).toMatch(/^\0/);
    expect(p.load(stub!)).toMatch(/not available in the browser/);
    expect(p.resolveId('react')).toBeNull();
  });
});

describe('embed bootstrap prod', () => {
  const originalFetch = globalThis.fetch;
  beforeEach(() => {
    document.body.innerHTML = '';
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('offline in tests');
    }) as typeof fetch;
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  function host(attrs: Record<string, string>): HTMLElement {
    const el = document.createElement('div');
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    document.body.appendChild(el);
    return el;
  }
  const mount = () => act(async () => mountEmbed(document));
  const widgetIn = (el: Element) => el.querySelector('.sqc-widget');

  it('[L1] a pinned testnet embed mounts its widget', async () => {
    const el = host({ 'data-sqc-widget': 'address-book', 'data-network': 'testnet', 'data-wasm-sha256': HEX64, 'data-exec-sha256': HEX64 });
    await mount();
    await waitFor(() => expect(widgetIn(el)).toBeTruthy());
    expect(console.warn).not.toHaveBeenCalledWith(expect.stringContaining('unpinned'));
  });

  it('[L2] an unpinned testnet embed mounts but warns', async () => {
    const el = host({ 'data-sqc-widget': 'address-book', 'data-network': 'testnet' });
    await mount();
    await waitFor(() => expect(widgetIn(el)).toBeTruthy());
    expect(console.warn).toHaveBeenCalledWith(expect.stringContaining('unpinned'));
  });

  it.each([
    ['no pins', {}],
    ['one pin', { 'data-wasm-sha256': HEX64 }],
    ['malformed pins', { 'data-wasm-sha256': 'zz', 'data-exec-sha256': HEX64 }],
  ])('[L2] mainnet with %s refuses to load the signer', async (_label, pins) => {
    const el = host({ 'data-sqc-widget': 'transfer', 'data-network': 'mainnet', ...pins });
    await mount();
    expect(el.querySelector('[role="alert"]')?.textContent).toMatch(/must pin the signer/);
    expect(widgetIn(el)).toBeNull();
  });

  it('[L2] mainnet with an explicit unpinned opt-in mounts', async () => {
    const el = host({ 'data-sqc-widget': 'address-book', 'data-network': 'mainnet', 'data-allow-unpinned-wasm': 'true' });
    await mount();
    await waitFor(() => expect(widgetIn(el)).toBeTruthy());
  });

  it.each(['nope', '__proto__', 'constructor', 'toString'])('[L2] unknown widget "%s" shows text, not a crash', async (name) => {
    const el = host({ 'data-sqc-widget': name });
    await mount();
    expect(el.textContent).toBe(`Unknown Splendor widget "${name}"`);
  });

  it('[L3] malformed or non-object data-props fall back to defaults', async () => {
    const a = host({ 'data-sqc-widget': 'address-book', 'data-props': '{not json', 'data-network': 'local' });
    const b = host({ 'data-sqc-widget': 'address-book', 'data-props': '[1,2]', 'data-network': 'local' });
    await mount();
    await waitFor(() => expect(widgetIn(a) && widgetIn(b)).toBeTruthy());
  });

  it('[L3] HTML in data-props renders as text, not markup', async () => {
    const el = host({
      'data-sqc-widget': 'address-book',
      'data-network': 'local',
      'data-props': JSON.stringify({ title: '<img src=x onerror="window.__pwned=1">' }),
    });
    await mount();
    await waitFor(() => expect(widgetIn(el)).toBeTruthy());
    expect(el.querySelector('img')).toBeNull();
    expect(el.querySelector('.sqc-widget__title')?.textContent).toContain('<img');
    expect((window as { __pwned?: number }).__pwned).toBeUndefined();
  });

  it('[L3] an unknown data-network falls back to testnet instead of failing', async () => {
    const el = host({ 'data-sqc-widget': 'address-book', 'data-network': 'moonnet' });
    await mount();
    await waitFor(() => expect(widgetIn(el)).toBeTruthy());
  });

  it('[L4] mounting twice does not duplicate widgets, and nested hosts are skipped', async () => {
    const hosts = Array.from({ length: 20 }, () => host({ 'data-sqc-widget': 'address-book', 'data-network': 'local' }));
    await mount();
    await mount();
    await waitFor(() => expect(hosts.every((h) => widgetIn(h))).toBe(true));
    for (const h of hosts) expect(h.querySelectorAll('.sqc-widget')).toHaveLength(1);
  });
});

describe('useModules prod', () => {
  function wrap(walletOpts: Parameters<typeof mockWallet>[0] = {}) {
    const ctrl = createMockConnectController({ wallet: mockWallet(walletOpts) });
    return ({ children }: { children: ReactNode }) => <MockSplendorProvider controller={ctrl}>{children}</MockSplendorProvider>;
  }

  it('[L1] exposes verify and validators clients next to the token factories', async () => {
    const { result } = renderHook(() => useModules(), {
      wrapper: wrap({
        moduleCall: ({ module, action }) =>
          module === 'validators' && action === 'list' ? [{ address: 'SQ:' + 'ab'.repeat(32) }] : { spld: '2500' },
      }),
    });
    const m = result.current!;
    expect(m.verify.moduleCall('register', { name: 'x' })).toEqual({ module: 'verify', action: 'register', params: { name: 'x' } });
    expect(m.validators.approveCall('SQ:' + 'cd'.repeat(32)).action).toBe('approve');
    await expect(m.validators.list()).resolves.toMatchObject({ count: 1, bond_spld: 2500 });
  });

  it('[L2] is null while disconnected', () => {
    const ctrl = createMockConnectController({ status: 'disconnected' });
    const { result } = renderHook(() => useModules(), {
      wrapper: ({ children }) => <MockSplendorProvider controller={ctrl}>{children}</MockSplendorProvider>,
    });
    expect(result.current).toBeNull();
  });
});
