/**
 * Splendor Connect v2 sign-in messages — build, parse and check (no signature work here).
 * Pure module: safe to import from the signing path and from servers.
 */

export const AUTH_PREFIX = 'Splendor Connect v2';
const SIGN_IN_PREFIX = 'Sign in to ';

/** Any C0 control character or DEL. Fields are single-line; the message joins them with `\n`. */
const CONTROL_RE = /[\u0000-\u001f\u007f\u0085\u2028\u2029]/;
/** Same, but allows the `\n` line separator inside a whole message. */
const MESSAGE_CONTROL_RE = /[\u0000-\u0009\u000b-\u001f\u007f\u0085\u2028\u2029]/;

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

function assertField(name: string, value: string | undefined): void {
  if (value == null) return;
  if (CONTROL_RE.test(value)) {
    throw new TypeError(`createAuthMessage: ${name} must not contain line breaks or control characters`);
  }
  if (name === 'statement' && Object.keys(FIELD_KEYS).some((p) => value.startsWith(p))) {
    throw new TypeError('createAuthMessage: statement must not look like a message field');
  }
}

/**
 * Build a Connect v2 auth challenge for `wallet.signMessage`.
 * Uses the `Splendor Connect v2` prefix so the signing domain leaves it intact.
 * Throws when any field contains CR/LF or other control characters (field injection).
 */
export function createAuthMessage(params: AuthMessageParams): string {
  if (!params.domain) throw new TypeError('createAuthMessage: domain is required');
  assertField('domain', params.domain);
  assertField('statement', params.statement);
  assertField('address', params.address);
  assertField('chainId', params.chainId);
  assertField('nonce', params.nonce);
  const issuedAt = params.issuedAt ?? Math.floor(Date.now() / 1000);
  if (!Number.isSafeInteger(issuedAt) || issuedAt < 0) {
    throw new TypeError('createAuthMessage: issuedAt must be unix seconds');
  }
  if (
    params.expirationTime != null &&
    (!Number.isSafeInteger(params.expirationTime) || params.expirationTime < 0)
  ) {
    throw new TypeError('createAuthMessage: expirationTime must be unix seconds');
  }
  const lines = [AUTH_PREFIX, `${SIGN_IN_PREFIX}${params.domain}`];
  if (params.statement) lines.push(params.statement);
  if (params.address) lines.push(`Address: ${params.address}`);
  if (params.chainId) lines.push(`Chain ID: ${params.chainId}`);
  if (params.nonce) lines.push(`Nonce: ${params.nonce}`);
  lines.push(`Issued At: ${issuedAt}`);
  if (params.expirationTime != null) lines.push(`Expiration Time: ${params.expirationTime}`);
  return lines.join('\n');
}

function parseField(line: string): [keyof ParsedAuthMessage, string] | null {
  for (const [prefix, key] of Object.entries(FIELD_KEYS)) {
    if (line.startsWith(prefix)) return [key, line.slice(prefix.length)];
  }
  return null;
}

/**
 * Parse a message produced by `createAuthMessage`. Returns null when the prefix,
 * layout, control characters or timestamps do not match the Connect v2 format.
 */
export function parseAuthMessage(message: string): ParsedAuthMessage | null {
  if (typeof message !== 'string' || MESSAGE_CONTROL_RE.test(message)) return null;
  const lines = message.split('\n');
  if (lines[0] !== AUTH_PREFIX) return null;
  if (!lines[1]?.startsWith(SIGN_IN_PREFIX)) return null;
  const domain = lines[1].slice(SIGN_IN_PREFIX.length);
  if (!domain) return null;

  const out: Record<string, string | number> = { domain };
  const seen = new Set<string>();
  for (let i = 2; i < lines.length; i += 1) {
    const field = parseField(lines[i]!);
    if (!field) {
      if (i === 2 && lines[i]) {
        out.statement = lines[i]!;
        continue;
      }
      return null;
    }
    const [key, value] = field;
    if (seen.has(key)) return null;
    seen.add(key);
    if (key === 'issuedAt' || key === 'expirationTime') {
      if (!/^\d+$/.test(value)) return null;
      const n = Number(value);
      if (!Number.isSafeInteger(n)) return null;
      out[key] = n;
    } else {
      if (!value) return null;
      out[key] = value;
    }
  }
  if (out.issuedAt == null) return null;
  return out as unknown as ParsedAuthMessage;
}

export type AuthCheckReason =
  | 'malformed'
  | 'domain'
  | 'expired'
  | 'not-yet-valid'
  | 'too-old'
  | 'nonce'
  | 'address'
  | 'chain';

export type AuthCheckResult =
  | { ok: true; parsed: ParsedAuthMessage }
  | { ok: false; reason: AuthCheckReason };

export type AuthCheckOptions = {
  domain: string;
  now?: number;
  /** Allowed clock skew for `Issued At` in the future. Default 300 s. */
  maxSkewSec?: number;
  /** Maximum age of `Issued At` when the message has no `Expiration Time`. Default 600 s. */
  maxAgeSec?: number;
  /** When set, the message must carry exactly this nonce. */
  expectedNonce?: string;
  /** When set, the message `Address` (if present) must match (case-insensitive). */
  expectedAddress?: string;
  /** When set, the message must carry this `Chain ID`; a message without one is rejected. */
  expectedChainId?: string;
};

function normalizeDomain(d: string): string {
  return d.trim().replace(/\/+$/, '').toLowerCase();
}

/**
 * Client-side sanity check before signing (or server-side before verifying):
 * Connect v2 layout, domain, expiry / age, nonce, address and chain bindings.
 * This does not verify the signature — servers use `verifySignIn` from `@splendor/sqc-sdk/auth`.
 */
export function checkAuthMessage(message: string, opts: AuthCheckOptions): AuthCheckResult {
  const parsed = parseAuthMessage(message);
  if (!parsed) return { ok: false, reason: 'malformed' };
  if (normalizeDomain(parsed.domain) !== normalizeDomain(opts.domain)) {
    return { ok: false, reason: 'domain' };
  }
  const now = opts.now ?? Math.floor(Date.now() / 1000);
  const skew = opts.maxSkewSec ?? 300;
  if (parsed.expirationTime != null) {
    if (parsed.expirationTime <= now) return { ok: false, reason: 'expired' };
  } else if (parsed.issuedAt < now - (opts.maxAgeSec ?? 600)) {
    return { ok: false, reason: 'too-old' };
  }
  if (parsed.issuedAt > now + skew) return { ok: false, reason: 'not-yet-valid' };
  if (opts.expectedNonce != null && parsed.nonce !== opts.expectedNonce) {
    return { ok: false, reason: 'nonce' };
  }
  if (
    opts.expectedAddress != null &&
    parsed.address != null &&
    parsed.address.toLowerCase() !== opts.expectedAddress.toLowerCase()
  ) {
    return { ok: false, reason: 'address' };
  }
  // A server that pins a chain must not accept a message that names none: it would be valid on every chain.
  if (opts.expectedChainId != null && parsed.chainId !== opts.expectedChainId) {
    return { ok: false, reason: 'chain' };
  }
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
    case 'too-old':
      return 'Sign-in challenge is too old — request a new one';
    case 'nonce':
      return 'Sign-in challenge nonce does not match';
    case 'address':
      return 'Sign-in challenge is for a different account';
    case 'chain':
      return 'Sign-in challenge is for a different network';
  }
}
