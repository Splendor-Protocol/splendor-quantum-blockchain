import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadVendorOptions, parseVendorBundle, sdkSource, sdkSourceLabel } from './vendor';

const WASM = 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64';
const bundle = {
  'vendor/@splendor/sqc-sdk/package.json': '{"name":"@splendor/sqc-sdk"}',
  'vendor/@splendor/connect-react/package.json': '{"name":"@splendor/connect-react"}',
  [WASM]: 'AAEC',
};

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe('parseVendorBundle', () => {
  it('accepts a bundle with both packages and picks up the signer', () => {
    const r = parseVendorBundle(bundle)!;
    expect(r.sdk).toBe('vendor');
    expect(r.vendorWasmBase64).toBe('AAEC');
    expect(Object.keys(r.vendorFiles!)).toHaveLength(3);
  });

  it('rejects anything else (HTML fallbacks, partial bundles, junk)', () => {
    expect(parseVendorBundle(null)).toBeNull();
    expect(parseVendorBundle([])).toBeNull();
    expect(parseVendorBundle('<html>')).toBeNull();
    expect(parseVendorBundle({ 'vendor/@splendor/sqc-sdk/package.json': '{}' })).toBeNull();
  });
});

describe('VITE_PLAYGROUND_SDK_SOURCE', () => {
  it('defaults to vendor and understands npm', () => {
    vi.stubEnv('VITE_PLAYGROUND_SDK_SOURCE', '');
    expect(sdkSource()).toBe('vendor');
    vi.stubEnv('VITE_PLAYGROUND_SDK_SOURCE', ' NPM ');
    expect(sdkSource()).toBe('npm');
    vi.stubEnv('VITE_PLAYGROUND_SDK_SOURCE', 'something-else');
    expect(sdkSource()).toBe('vendor');
  });

  it('npm source never fetches the bundle', async () => {
    vi.stubEnv('VITE_PLAYGROUND_SDK_SOURCE', 'npm');
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    expect(await loadVendorOptions()).toEqual({ sdk: 'npm' });
    expect(fetchMock).not.toHaveBeenCalled();
    expect(sdkSourceLabel()).toBe('npm registry');
  });

  it('vendor source loads the bundle, and falls back to npm when it is missing or not JSON', async () => {
    vi.stubEnv('VITE_PLAYGROUND_SDK_SOURCE', 'vendor');
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(bundle), { status: 200 })));
    const { loadVendorOptions: load1 } = await import('./vendor');
    expect((await load1()).sdk).toBe('vendor');

    vi.resetModules();
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<!doctype html>', { status: 200 })));
    const { loadVendorOptions: load2 } = await import('./vendor');
    expect(await load2()).toEqual({ sdk: 'npm' });

    vi.resetModules();
    vi.stubGlobal('fetch', vi.fn(async () => new Response('nope', { status: 404 })));
    const { loadVendorOptions: load3 } = await import('./vendor');
    expect(await load3()).toEqual({ sdk: 'npm' });
  });
});
