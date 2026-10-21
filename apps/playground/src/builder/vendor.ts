import type { GenerateOptions } from '@splendor/dapp-spec';

export type SdkSourceSetting = 'vendor' | 'npm';
export type VendorOptions = Pick<GenerateOptions, 'sdk' | 'vendorFiles' | 'vendorWasmBase64'>;

/**
 * Where exported projects get the Splendor packages from (`VITE_PLAYGROUND_SDK_SOURCE`):
 * - `vendor` (default): `file:vendor/@splendor/*`, bundled by `npm run pack:sdk-vendor`.
 *   Works before the packages are published and inside StackBlitz.
 * - `npm`: registry versions pinned to the ones this playground ships with.
 */
export function sdkSource(): SdkSourceSetting {
  const raw = (import.meta.env.VITE_PLAYGROUND_SDK_SOURCE as string | undefined)?.trim().toLowerCase();
  return raw === 'npm' ? 'npm' : 'vendor';
}

const WASM_KEY = 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64';

let cached: VendorOptions | null = null;

/** Parse a vendor bundle; null unless it carries both SDK packages. Exported for tests. */
export function parseVendorBundle(raw: unknown): VendorOptions | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const files = raw as Record<string, unknown>;
  if (typeof files['vendor/@splendor/sqc-sdk/package.json'] !== 'string') return null;
  if (typeof files['vendor/@splendor/connect-react/package.json'] !== 'string') return null;
  const vendorFiles: Record<string, string> = {};
  for (const [k, v] of Object.entries(files)) if (typeof v === 'string') vendorFiles[k] = v;
  return { sdk: 'vendor', vendorFiles, vendorWasmBase64: vendorFiles[WASM_KEY] };
}

/**
 * Load vendored package files from /vendor/vendor-bundle.json (written by `npm run pack:sdk-vendor`).
 * Falls back to registry versions when the source is `npm` or the bundle is missing, so exports
 * never end up with `file:` dependencies that point at nothing.
 */
export async function loadVendorOptions(): Promise<VendorOptions> {
  if (sdkSource() !== 'vendor') return { sdk: 'npm' };
  if (cached) return cached;
  try {
    const res = await fetch('/vendor/vendor-bundle.json');
    // A SPA host answers unknown paths with index.html (200): only accept real JSON.
    const parsed = res.ok ? parseVendorBundle(await res.json()) : null;
    cached = parsed ?? { sdk: 'npm' };
  } catch {
    cached = { sdk: 'npm' };
  }
  return cached;
}

export function sdkSourceLabel(options?: VendorOptions | null): string {
  const vendored = options ? options.sdk === 'vendor' : sdkSource() === 'vendor';
  return vendored ? 'vendored SDK (works offline / StackBlitz)' : 'npm registry';
}
