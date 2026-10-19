/**
 * Response headers for the built playground. One source for `vite preview` (and so the e2e
 * suite); `public/_headers` and `vercel.json` must say the same, which securityHeaders.test.ts
 * checks. Add your API origin to connect-src if it is not served under /api on this origin, and
 * your RPC hosts if you override VITE_SQC_<ID>_RPC_URL with hosts outside *.splendor.org.
 */
export const CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  // The signer is WebAssembly; nothing else is compiled or evaluated at runtime.
  "script-src 'self' 'wasm-unsafe-eval'",
  // React sets inline style attributes for theme overrides.
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: https:",
  "font-src 'self' data:",
  "connect-src 'self' https://*.splendor.org wss://*.splendor.org",
  "frame-src 'self'",
  "frame-ancestors 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'self' https://stackblitz.com",
].join('; ');

export const SECURITY_HEADERS: Record<string, string> = {
  'Content-Security-Policy': CONTENT_SECURITY_POLICY,
  'Strict-Transport-Security': 'max-age=31536000; includeSubDomains',
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'Permissions-Policy': 'camera=(), microphone=(), geolocation=(), payment=()',
  'Cross-Origin-Opener-Policy': 'same-origin-allow-popups',
};

/**
 * The isolated preview iframe has an opaque origin, and Vite's module scripts and stylesheets are
 * loaded with `crossorigin`, so /assets must allow any origin. They are public build output.
 */
export const ASSET_HEADERS: Record<string, string> = {
  'Access-Control-Allow-Origin': '*',
  'Cache-Control': 'public, max-age=31536000, immutable',
};
