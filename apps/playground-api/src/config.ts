export type ApiConfig = {
  port: number;
  host: string;
  /** `sqlite:./data/playground.db`, `sqlite::memory:` or `postgres://…` */
  databaseUrl: string;
  /** Browser origins allowed to call the API and to appear as the sign-in domain. */
  origins: string[];
  /** Addresses that are always admins (canonical `SQ:` + lowercase hex). */
  adminAddresses: string[];
  sessionTtlSec: number;
  /** `Secure` session cookie. On by default in production. */
  cookieSecure: boolean;
  /** `SameSite` for the session cookie. Use `none` only when the playground is on another site. */
  cookieSameSite: 'lax' | 'strict' | 'none';
  /** `__Host-` prefixed when Secure, so the cookie cannot be set for a parent domain or another path. */
  cookieName: string;
  /**
   * Which peers may set `X-Forwarded-*` (Fastify `trustProxy`): addresses, CIDRs or `loopback`. Off by
   * default: an API reachable directly would otherwise let any client choose its own IP and dodge the
   * rate limits.
   */
  trustProxy: boolean | string[];
  /** Expected chain id in sign-in messages (optional). */
  chainId?: string;
  /** `apiUrl` / `npmRegistry` change only for GitHub Enterprise, a registry mirror, or tests. */
  github: { token?: string; repo: string; workflow: string; ref: string; apiUrl: string; npmRegistry: string };
  /** Audit rows older than this are deleted by the housekeeping job. */
  auditRetentionDays: number;
  /** Phase-2 hosted publishing routes. */
  hostingEnabled: boolean;
  /** Test-only password-less login for e2e. Refused in production and on non-loopback hosts. */
  allowTestLogin: boolean;
  production: boolean;
  /** Non-fatal configuration problems, logged at startup. */
  warnings: string[];
};

const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;

/** `SQ:` + lowercase hex, or null when not an address. */
export function canonicalAddress(value: unknown): string | null {
  return typeof value === 'string' && ADDRESS_RE.test(value) ? `SQ:${value.slice(3).toLowerCase()}` : null;
}

function list(v: string | undefined): string[] {
  return (v ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

function int(env: NodeJS.ProcessEnv, key: string, fallback: number, min: number, max: number): number {
  const raw = env[key];
  if (raw === undefined || raw === '') return fallback;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < min || n > max) throw new Error(`${key} must be an integer from ${min} to ${max}`);
  return n;
}

function parseOrigin(value: string): string {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`PLAYGROUND_ORIGINS: "${value}" is not a URL`);
  }
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== value) {
    throw new Error(`PLAYGROUND_ORIGINS: "${value}" must be a bare origin like https://playground.example.com (no path or trailing slash)`);
  }
  return value;
}

function parseTrustProxy(raw: string | undefined): ApiConfig['trustProxy'] {
  const v = (raw ?? '').trim();
  if (!v || v === 'false' || v === '0') return false;
  if (v === 'true') return true;
  // Fastify ignores hop counts (they cannot tell who the immediate peer is), so say so instead.
  if (/^\d+$/.test(v)) throw new Error('TRUST_PROXY must list proxy addresses or CIDRs (e.g. loopback or 10.0.0.0/8), not a hop count');
  return list(v);
}

const LOOPBACK = new Set(['127.0.0.1', '::1', 'localhost']);

export function loadConfig(env: NodeJS.ProcessEnv = process.env): ApiConfig {
  const production = env.NODE_ENV === 'production';
  const warnings: string[] = [];
  const host = env.HOST ?? '127.0.0.1';

  const allowTestLogin = env.ALLOW_TEST_LOGIN === '1';
  if (allowTestLogin && production) throw new Error('ALLOW_TEST_LOGIN must not be set in production');
  if (allowTestLogin && env.NODE_ENV !== 'test' && !LOOPBACK.has(host)) {
    throw new Error('ALLOW_TEST_LOGIN needs NODE_ENV=test or a loopback HOST');
  }

  const origins = list(env.PLAYGROUND_ORIGINS).map(parseOrigin);
  if (production && origins.length === 0) throw new Error('PLAYGROUND_ORIGINS is required in production');

  const sameSite = (env.COOKIE_SAMESITE ?? 'lax').toLowerCase();
  if (!['lax', 'strict', 'none'].includes(sameSite)) throw new Error('COOKIE_SAMESITE must be lax, strict or none');
  const cookieSecure = env.COOKIE_SECURE ? env.COOKIE_SECURE === '1' : production;
  if (sameSite === 'none' && !cookieSecure) throw new Error('COOKIE_SAMESITE=none requires COOKIE_SECURE=1');

  const adminAddresses = list(env.ADMIN_ADDRESSES).map((a) => {
    const c = canonicalAddress(a);
    if (!c) throw new Error(`ADMIN_ADDRESSES: "${a}" is not an SQ: address`);
    return c;
  });

  const databaseUrl = env.DATABASE_URL ?? 'sqlite:./data/playground.db';
  if (production && databaseUrl.startsWith('sqlite:')) {
    warnings.push('DATABASE_URL is SQLite in production: fine for one small instance, but use Postgres for anything you need to back up and keep');
  }

  return {
    port: int(env, 'PORT', 8787, 1, 65535),
    host,
    databaseUrl,
    origins: origins.length ? origins : ['http://localhost:5180', 'http://127.0.0.1:5180'],
    adminAddresses,
    sessionTtlSec: int(env, 'SESSION_TTL_SEC', 7 * 24 * 3600, 300, 90 * 24 * 3600),
    cookieSecure,
    cookieSameSite: sameSite as ApiConfig['cookieSameSite'],
    cookieName: cookieSecure ? '__Host-sqc_pg_session' : 'sqc_pg_session',
    trustProxy: parseTrustProxy(env.TRUST_PROXY),
    chainId: env.SIGNIN_CHAIN_ID || undefined,
    github: {
      token: env.GITHUB_TOKEN || undefined,
      repo: env.GITHUB_REPO ?? 'Splendor-Protocol/splendor-quantum-blockchain',
      workflow: env.GITHUB_RELEASE_WORKFLOW ?? 'release-packages.yml',
      ref: env.GITHUB_RELEASE_REF ?? 'main',
      apiUrl: (env.GITHUB_API_URL || 'https://api.github.com').replace(/\/+$/, ''),
      npmRegistry: (env.NPM_REGISTRY_URL || 'https://registry.npmjs.org').replace(/\/+$/, ''),
    },
    auditRetentionDays: int(env, 'AUDIT_RETENTION_DAYS', 365, 1, 3650),
    hostingEnabled: env.HOSTING_ENABLED === '1',
    allowTestLogin,
    production,
    warnings,
  };
}
