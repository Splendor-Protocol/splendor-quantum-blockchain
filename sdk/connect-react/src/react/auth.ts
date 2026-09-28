/**
 * ML-DSA Sign-In recipe — domain-separated auth messages (not eth_siwe).
 * Server verifies via wallet.verifyMessage / WASM verify path.
 */
import { SIGN_MESSAGE_DOMAIN } from '@splendor/sqc-sdk';

const AUTH_PREFIX = 'Splendor Connect v2';
const SIGN_IN_PREFIX = 'Sign in to ';

export type AuthMessageParams = {
  /** Relying-party origin, e.g. https://app.example.com */
  domain: string;
  /** Application / site name shown to the user */
  statement?: string;
  /** Optional opaque nonce from the server */
  nonce?: string;
  /** Unix seconds; defaults to now */
  issuedAt?: number;
  /** Optional expiry (unix seconds) */
  expirationTime?: number;
  /** Chain / network id string */
  chainId?: string;
  /** SQ address binding (optional; often filled after connect) */
  address?: string;
};

/**
 * Build a Connect-style auth challenge for `wallet.signMessage`.
 * Already uses the Splendor Connect v2 prefix so prepareSignMessage leaves it intact.
 */
export function createAuthMessage(params: AuthMessageParams): string {
  const issuedAt = params.issuedAt ?? Math.floor(Date.now() / 1000);
  const lines = [AUTH_PREFIX, `${SIGN_IN_PREFIX}${params.domain}`];
  if (params.statement) lines.push(params.statement);
  if (params.address) lines.push(`Address: ${params.address}`);
  if (params.chainId) lines.push(`Chain ID: ${params.chainId}`);
  if (params.nonce) lines.push(`Nonce: ${params.nonce}`);
  lines.push(`Issued At: ${issuedAt}`);
  if (params.expirationTime != null) lines.push(`Expiration Time: ${params.expirationTime}`);
  return lines.join('\n');
}

export type ParsedAuthMessage = {
  domain: string;
  statement?: string;
  address?: string;
  chainId?: string;
  nonce?: string;
  issuedAt: number;
  expirationTime?: number;
};

const FIELD_KEYS: Record<string, keyof ParsedAuthMessage> = {
  'Address: ': 'address',
  'Chain ID: ': 'chainId',
  'Nonce: ': 'nonce',
  'Issued At: ': 'issuedAt',
  'Expiration Time: ': 'expirationTime',
};

function parseField(line: string): [keyof ParsedAuthMessage, string] | null {
  for (const [prefix, key] of Object.entries(FIELD_KEYS)) {
    if (line.startsWith(prefix)) return [key, line.slice(prefix.length)];
  }
  return null;
}

/**
 * Parse a message produced by `createAuthMessage`. Returns null when the prefix,
 * layout, or timestamps do not match the Connect v2 format.
 */
export function parseAuthMessage(message: string): ParsedAuthMessage | null {
  if (typeof message !== 'string') return null;
  const lines = message.split('\n');
  if (lines[0] !== AUTH_PREFIX) return null;
  if (!lines[1]?.startsWith(SIGN_IN_PREFIX)) return null;
  const domain = lines[1].slice(SIGN_IN_PREFIX.length);
  if (!domain) return null;

  const out: Record<string, string | number> = { domain };
  const seen = new Set<string>();
  for (let i = 2; i < lines.length; i += 1) {
    const field = parseField(lines[i]);
    if (!field) {
      if (i === 2 && lines[i]) {
        out.statement = lines[i];
        continue;
      }
      return null;
    }
    const [key, value] = field;
    if (seen.has(key)) return null;
    seen.add(key);
    if (key === 'issuedAt' || key === 'expirationTime') {
      if (!/^\d+$/.test(value)) return null;
      out[key] = Number(value);
    } else {
      out[key] = value;
    }
  }
  if (out.issuedAt == null) return null;
  return out as unknown as ParsedAuthMessage;
}

export type AuthCheckReason = 'malformed' | 'domain' | 'expired' | 'not-yet-valid';

export type AuthCheckResult =
  | { ok: true; parsed: ParsedAuthMessage }
  | { ok: false; reason: AuthCheckReason };

function normalizeDomain(d: string): string {
  return d.trim().replace(/\/+$/, '').toLowerCase();
}

/**
 * Client-side sanity check before signing (or server-side before verifying):
 * Connect v2 layout, domain matches, not expired, `Issued At` not in the future
 * beyond `maxSkewSec`. This does not verify the signature.
 */
export function checkAuthMessage(
  message: string,
  opts: { domain: string; now?: number; maxSkewSec?: number },
): AuthCheckResult {
  const parsed = parseAuthMessage(message);
  if (!parsed) return { ok: false, reason: 'malformed' };
  if (normalizeDomain(parsed.domain) !== normalizeDomain(opts.domain)) {
    return { ok: false, reason: 'domain' };
  }
  const now = opts.now ?? Math.floor(Date.now() / 1000);
  const skew = opts.maxSkewSec ?? 300;
  if (parsed.expirationTime != null && parsed.expirationTime <= now) {
    return { ok: false, reason: 'expired' };
  }
  if (parsed.issuedAt > now + skew) return { ok: false, reason: 'not-yet-valid' };
  return { ok: true, parsed };
}

export function authCheckMessage(reason: AuthCheckReason): string {
  switch (reason) {
    case 'malformed':
      return 'Sign-in challenge is not a valid Splendor Connect v2 message';
    case 'domain':
      return 'Sign-in challenge is for a different site';
    case 'expired':
      return 'Sign-in challenge has expired';
    case 'not-yet-valid':
      return 'Sign-in challenge is dated in the future';
  }
}

export type SignedAuth = {
  message: string;
  signatureB64: string;
  publicKeyB64: string;
  address: string;
  /** Domain used for unstructured fallbacks (always SIGN_MESSAGE_DOMAIN family). */
  messageDomain: typeof SIGN_MESSAGE_DOMAIN | 'Splendor Connect v2';
};
