export type ApiConfig = {
  port: number;
  host: string;
  /** `sqlite:./data/playground.db`, `sqlite::memory:` or `postgres://…` */
  databaseUrl: string;
  /** Browser origins allowed to call the API and to appear as the sign-in domain. */
  origins: string[];
  /** Addresses that are always admins (lowercased). */
  adminAddresses: string[];
  sessionTtlSec: number;
  /** `Secure` session cookie. On by default in production. */
  cookieSecure: boolean;
  /** `SameSite` for the session cookie. Use `none` only when the playground is on another site. */
  cookieSameSite: 'lax' | 'strict' | 'none';
  /** Expected chain id in sign-in messages (optional). */
  chainId?: string;
  /** `apiUrl` / `npmRegistry` change only for GitHub Enterprise, a registry mirror, or tests. */
  github: { token?: string; repo: string; workflow: string; ref: string; apiUrl: string; npmRegistry: string };
  /** Phase-2 hosted publishing routes. */
  hostingEnabled: boolean;
  /** Test-only password-less login for e2e. Refused when NODE_ENV=production. */
  allowTestLogin: boolean;
  production: boolean;
};

function list(v: string | undefined): string[] {
  return (v ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): ApiConfig {
  const production = env.NODE_ENV === 'production';
  const allowTestLogin = env.ALLOW_TEST_LOGIN === '1';
  if (production && allowTestLogin) throw new Error('ALLOW_TEST_LOGIN must not be set in production');
  const origins = list(env.PLAYGROUND_ORIGINS);
  if (production && origins.length === 0) throw new Error('PLAYGROUND_ORIGINS is required in production');
  const sameSite = (env.COOKIE_SAMESITE ?? 'lax').toLowerCase();
  if (!['lax', 'strict', 'none'].includes(sameSite)) throw new Error('COOKIE_SAMESITE must be lax, strict or none');
  return {
    port: Number(env.PORT ?? 8787),
    host: env.HOST ?? '127.0.0.1',
    databaseUrl: env.DATABASE_URL ?? 'sqlite:./data/playground.db',
    origins: origins.length ? origins : ['http://localhost:5180', 'http://127.0.0.1:5180'],
    adminAddresses: list(env.ADMIN_ADDRESSES).map((a) => a.toLowerCase()),
    sessionTtlSec: Number(env.SESSION_TTL_SEC ?? 7 * 24 * 3600),
    cookieSecure: env.COOKIE_SECURE ? env.COOKIE_SECURE === '1' : production,
    cookieSameSite: sameSite as ApiConfig['cookieSameSite'],
    chainId: env.SIGNIN_CHAIN_ID || undefined,
    github: {
      token: env.GITHUB_TOKEN || undefined,
      repo: env.GITHUB_REPO ?? 'Splendor-Protocol/splendor-quantum-blockchain',
      workflow: env.GITHUB_RELEASE_WORKFLOW ?? 'release-packages.yml',
      ref: env.GITHUB_RELEASE_REF ?? 'main',
      apiUrl: (env.GITHUB_API_URL || 'https://api.github.com').replace(/\/+$/, ''),
      npmRegistry: (env.NPM_REGISTRY_URL || 'https://registry.npmjs.org').replace(/\/+$/, ''),
    },
    hostingEnabled: env.HOSTING_ENABLED === '1',
    allowTestLogin,
    production,
  };
}
