import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { addressFromPublicKey, createAuthMessage } from '@splendor/sqc-sdk/auth';
import { TEMPLATES, instantiateTemplate } from '@splendor/dapp-spec';
import { buildApp } from './app.js';
import { loadConfig } from './config.js';
import { createReleaseService, type FetchLike } from './releases.js';
import { createStore, type Store } from './store/index.js';

const ORIGIN = 'http://localhost:5180';
const PUB_ADMIN = Buffer.from('admin-public-key').toString('base64');
const PUB_USER = Buffer.from('user-public-key').toString('base64');
const ADMIN = addressFromPublicKey(PUB_ADMIN)!;
const USER = addressFromPublicKey(PUB_USER)!;

type App = Awaited<ReturnType<typeof buildApp>>;

function fakeGitHub() {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchImpl: FetchLike = async (url, init) => {
    calls.push({ url, init });
    const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
    if (url.includes('registry.npmjs.org')) return url.includes('dapp-spec') ? json({}, 404) : json({ latest: '3.0.0', next: '3.1.0-next.1' });
    if (url.endsWith('/dispatches')) return new Response(null, { status: 204 });
    if (url.includes('/contents/.changeset/brave-cats.md')) return json({ encoding: 'base64', content: Buffer.from('---\n"@splendor/connect-react": minor\n---\n\nAdd manifest\n').toString('base64') });
    if (url.includes('/contents/.changeset')) return json([{ name: 'brave-cats.md', path: '.changeset/brave-cats.md', type: 'file' }, { name: 'README.md', path: '.changeset/README.md', type: 'file' }]);
    if (url.includes('/contents/')) return json({ encoding: 'base64', content: Buffer.from(JSON.stringify({ version: '3.0.0' })).toString('base64') });
    if (url.includes('/runs')) return json({ workflow_runs: [{ id: 1, status: 'completed', conclusion: 'success', html_url: 'https://github.com/x/y/actions/runs/1', created_at: '2026-01-01T00:00:00Z', event: 'workflow_dispatch', display_title: 'Release' }] });
    return json({ message: 'not found' }, 404);
  };
  return { calls, fetchImpl };
}

let store: Store;
let app: App;
let gh: ReturnType<typeof fakeGitHub>;

beforeEach(async () => {
  store = await createStore('sqlite::memory:');
  gh = fakeGitHub();
  const config = loadConfig({ NODE_ENV: 'test', ADMIN_ADDRESSES: ADMIN, GITHUB_TOKEN: 'gh-test', PLAYGROUND_ORIGINS: ORIGIN });
  app = await buildApp({
    config,
    store,
    verifier: async (_pk, _msg, sig) => sig === 'valid-signature',
    releases: createReleaseService(config.github, gh.fetchImpl),
  });
});

afterEach(async () => {
  await app.close();
  await store.close();
});

const json = (body: unknown, cookie?: string) => ({
  headers: { 'content-type': 'application/json', origin: ORIGIN, ...(cookie ? { cookie } : {}) },
  payload: JSON.stringify(body),
});

async function signIn(pub: string, address: string, opts: { signature?: string; domain?: string } = {}) {
  const { nonce } = (await app.inject({ method: 'POST', url: '/auth/nonce', ...json({}) })).json() as { nonce: string };
  const message = createAuthMessage({ domain: opts.domain ?? ORIGIN, nonce, address, statement: 'Sign in', expirationTime: Math.floor(Date.now() / 1000) + 300 });
  const res = await app.inject({ method: 'POST', url: '/auth/verify', ...json({ message, signatureB64: opts.signature ?? 'valid-signature', publicKeyB64: pub, address }) });
  const setCookie = res.headers['set-cookie'];
  const cookie = (Array.isArray(setCookie) ? setCookie[0] : setCookie)?.split(';')[0];
  return { res, cookie, message, nonce };
}

describe('auth', () => {
  it('signs in with a valid signature and sets an httpOnly cookie', async () => {
    const { res, cookie } = await signIn(PUB_USER, USER);
    expect(res.statusCode).toBe(200);
    expect(res.json()).toEqual({ address: USER, isAdmin: false });
    expect(String(res.headers['set-cookie'])).toMatch(/HttpOnly/i);
    const me = await app.inject({ method: 'GET', url: '/auth/me', headers: { cookie: cookie! } });
    expect(me.json()).toEqual({ address: USER, isAdmin: false });
  });

  it('rejects a replayed nonce', async () => {
    const { message } = await signIn(PUB_USER, USER);
    const replay = await app.inject({ method: 'POST', url: '/auth/verify', ...json({ message, signatureB64: 'valid-signature', publicKeyB64: PUB_USER, address: USER }) });
    expect(replay.statusCode).toBe(401);
    expect(replay.json().error).toMatch(/nonce-used/);
  });

  it('rejects bad signatures, foreign domains and key/address mismatches', async () => {
    expect((await signIn(PUB_USER, USER, { signature: 'forged' })).res.statusCode).toBe(401);
    expect((await signIn(PUB_USER, USER, { domain: 'https://evil.example' })).res.json().error).toMatch(/another site/);
    expect((await signIn(PUB_USER, ADMIN)).res.json().error).toMatch(/key-mismatch/);
  });

  it('logs out', async () => {
    const { cookie } = await signIn(PUB_USER, USER);
    await app.inject({ method: 'POST', url: '/auth/logout', ...json({}, cookie) });
    expect((await app.inject({ method: 'GET', url: '/auth/me', headers: { cookie: cookie! } })).statusCode).toBe(401);
  });

  it('has no test-login route unless enabled', async () => {
    expect((await app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: ADMIN }) })).statusCode).toBe(404);
    expect(() => loadConfig({ NODE_ENV: 'production', ALLOW_TEST_LOGIN: '1', PLAYGROUND_ORIGINS: ORIGIN })).toThrow();
  });
});

describe('csrf', () => {
  it('refuses state changes from other origins or without JSON', async () => {
    const evil = await app.inject({ method: 'POST', url: '/auth/nonce', headers: { 'content-type': 'application/json', origin: 'https://evil.example' }, payload: '{}' });
    expect(evil.statusCode).toBe(403);
    const form = await app.inject({ method: 'POST', url: '/auth/nonce', headers: { 'content-type': 'application/x-www-form-urlencoded', origin: ORIGIN }, payload: 'a=1' });
    expect(form.statusCode).toBe(415);
  });
});

describe('projects', () => {
  const spec = () => {
    const s = instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!);
    return s;
  };

  it('requires sign-in', async () => {
    expect((await app.inject({ method: 'GET', url: '/projects' })).statusCode).toBe(401);
  });

  it('validates specs server-side', async () => {
    const { cookie } = await signIn(PUB_USER, USER);
    const bad = { ...spec(), pages: [{ path: '/', title: 'x', blocks: [{ id: 'a', widget: 'evil-widget', props: {} }] }] };
    const res = await app.inject({ method: 'POST', url: '/projects', ...json({ spec: bad }, cookie) });
    expect(res.statusCode).toBe(422);
    expect(res.json().issues[0].message).toMatch(/unknown widget/);
  });

  it('creates, updates, shares and isolates projects by owner', async () => {
    const user = await signIn(PUB_USER, USER);
    const created = await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) });
    expect(created.statusCode).toBe(201);
    const id = created.json().id as string;

    const renamed = { ...spec(), name: 'Renamed' };
    const upd = await app.inject({ method: 'PUT', url: `/projects/${id}`, ...json({ spec: renamed }, user.cookie) });
    expect(upd.json().name).toBe('Renamed');

    const list = await app.inject({ method: 'GET', url: '/projects', headers: { cookie: user.cookie! } });
    expect(list.json().projects).toHaveLength(1);

    const admin = await signIn(PUB_ADMIN, ADMIN);
    expect((await app.inject({ method: 'GET', url: `/projects/${id}`, headers: { cookie: admin.cookie! } })).statusCode).toBe(404);

    const share = await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) });
    const shared = await app.inject({ method: 'GET', url: `/share/${share.json().shareId}` });
    expect(shared.json().name).toBe('Renamed');

    const del = await app.inject({ method: 'DELETE', url: `/projects/${id}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect(del.statusCode).toBe(200);
  });
});

describe('admin', () => {
  it('rejects non-admins', async () => {
    const { cookie } = await signIn(PUB_USER, USER);
    for (const [method, url] of [
      ['GET', '/admin/releases'],
      ['GET', '/admin/audit'],
      ['GET', '/admin/admins'],
    ] as const) {
      expect((await app.inject({ method, url, headers: { cookie: cookie! } })).statusCode).toBe(403);
    }
    expect((await app.inject({ method: 'PUT', url: '/admin/catalog/transfer', ...json({ hidden: true }, cookie) })).statusCode).toBe(403);
  });

  it('sets catalog flags and records them in the audit log', async () => {
    const { cookie } = await signIn(PUB_ADMIN, ADMIN);
    const res = await app.inject({ method: 'PUT', url: '/admin/catalog/nft-drop', ...json({ hidden: true, status: 'experimental' }, cookie) });
    expect(res.statusCode).toBe(200);
    const pub = await app.inject({ method: 'GET', url: '/catalog' });
    expect(pub.json().flags['nft-drop']).toMatchObject({ hidden: true, status: 'experimental' });
    expect((await app.inject({ method: 'PUT', url: '/admin/catalog/nft-drop', ...json({ status: 'gold' }, cookie) })).statusCode).toBe(400);
    const audit = await app.inject({ method: 'GET', url: '/admin/audit', headers: { cookie: cookie! } });
    expect(audit.json().entries.map((e: { action: string }) => e.action)).toContain('catalog.flag');
  });

  it('reports release status and dispatches the workflow', async () => {
    const { cookie } = await signIn(PUB_ADMIN, ADMIN);
    const status = (await app.inject({ method: 'GET', url: '/admin/releases', headers: { cookie: cookie! } })).json();
    expect(status.configured).toBe(true);
    expect(status.packages.find((p: { name: string }) => p.name === '@splendor/connect-react')).toMatchObject({ repoVersion: '3.0.0', distTags: { latest: '3.0.0' } });
    expect(status.packages.find((p: { name: string }) => p.name === '@splendor/dapp-spec').distTags).toEqual({});
    expect(status.changesets).toEqual([{ file: 'brave-cats.md', summary: 'Add manifest' }]);
    expect(status.runs[0].conclusion).toBe('success');

    expect((await app.inject({ method: 'POST', url: '/admin/releases', ...json({ channel: 'beta', dryRun: true }, cookie) })).statusCode).toBe(400);
    const r = await app.inject({ method: 'POST', url: '/admin/releases', ...json({ channel: 'next', dryRun: true }, cookie) });
    expect(r.json()).toEqual({ dispatched: true, dryRun: true });
    const dispatch = gh.calls.find((c) => c.url.endsWith('/dispatches'))!;
    expect(dispatch.url).toContain('/actions/workflows/release-packages.yml/dispatches');
    expect(JSON.parse(String(dispatch.init?.body))).toEqual({ ref: 'main', inputs: { channel: 'next', dry_run: 'true' } });
    expect((dispatch.init?.headers as Record<string, string>).authorization).toBe('Bearer gh-test');
  });

  it('manages db admins but never removes yourself or env admins', async () => {
    const { cookie } = await signIn(PUB_ADMIN, ADMIN);
    expect((await app.inject({ method: 'POST', url: '/admin/admins', ...json({ address: USER }, cookie) })).statusCode).toBe(200);
    const user = await signIn(PUB_USER, USER);
    expect(user.res.json().isAdmin).toBe(true);
    expect((await app.inject({ method: 'DELETE', url: `/admin/admins/${ADMIN}`, headers: { origin: ORIGIN, cookie: cookie! } })).statusCode).toBe(400);
    expect((await app.inject({ method: 'DELETE', url: `/admin/admins/${USER}`, headers: { origin: ORIGIN, cookie: cookie! } })).statusCode).toBe(200);
  });
});

/** A separate app with its own env, for the behaviour that depends on configuration. */
async function makeApp(env: Record<string, string>, wrap: (s: Store) => Store = (s) => s) {
  const s = await createStore('sqlite::memory:');
  const config = loadConfig({ NODE_ENV: 'test', ADMIN_ADDRESSES: ADMIN, PLAYGROUND_ORIGINS: ORIGIN, ...env });
  const a = await buildApp({ config, store: wrap(s), verifier: async (_pk, _msg, sig) => sig === 'valid-signature' });
  return { app: a, store: s, config, close: async () => (await a.close(), await s.close()) };
}

const cookieOf = (res: { headers: Record<string, unknown> }) => {
  const c = res.headers['set-cookie'];
  return (Array.isArray(c) ? c[0] : String(c ?? '')).split(';')[0]!;
};

describe('proxy trust', () => {
  const failedSignInIp = async (a: App, forwardedFor: string) => {
    const { nonce } = (await a.inject({ method: 'POST', url: '/auth/nonce', ...json({}) })).json() as { nonce: string };
    const message = createAuthMessage({ domain: ORIGIN, nonce, address: USER, statement: 'x', expirationTime: Math.floor(Date.now() / 1000) + 300 });
    await a.inject({
      method: 'POST',
      url: '/auth/verify',
      headers: { 'content-type': 'application/json', origin: ORIGIN, 'x-forwarded-for': forwardedFor },
      payload: JSON.stringify({ message, signatureB64: 'forged', publicKeyB64: PUB_USER, address: USER }),
    });
  };

  it('ignores X-Forwarded-For unless TRUST_PROXY is set', async () => {
    const plain = await makeApp({});
    await failedSignInIp(plain.app, '6.6.6.6');
    expect((await plain.store.listAudit(5))[0]!.detail).toContain('127.0.0.1');
    await plain.close();

    const proxied = await makeApp({ TRUST_PROXY: 'loopback' });
    await failedSignInIp(proxied.app, '6.6.6.6');
    expect((await proxied.store.listAudit(5))[0]!.detail).toContain('6.6.6.6');
    await proxied.close();
  });

  it('parses TRUST_PROXY', () => {
    const tp = (v?: string) => loadConfig({ NODE_ENV: 'test', ...(v === undefined ? {} : { TRUST_PROXY: v }) }).trustProxy;
    expect(tp()).toBe(false);
    expect(tp('true')).toBe(true);
    expect(() => tp('2')).toThrow(/not a hop count/);
    expect(tp('loopback, 10.0.0.0/8')).toEqual(['loopback', '10.0.0.0/8']);
  });
});

describe('csrf with a session cookie', () => {
  it('needs an allowed Origin or Sec-Fetch-Site: same-origin when the cookie is sent', async () => {
    const { cookie } = await signIn(PUB_USER, USER);
    const body = { 'content-type': 'application/json', cookie: cookie! };
    const noOrigin = await app.inject({ method: 'POST', url: '/projects', headers: body, payload: JSON.stringify({ spec: {} }) });
    expect(noOrigin.statusCode).toBe(403);
    expect(noOrigin.json().error).toMatch(/Origin header required/);
    const sameOrigin = await app.inject({ method: 'POST', url: '/auth/logout', headers: { ...body, 'sec-fetch-site': 'same-origin' }, payload: '{}' });
    expect(sameOrigin.statusCode).toBe(200);
    const crossSite = await app.inject({ method: 'DELETE', url: '/projects/xxxxxxxxxxxx', headers: { cookie: cookie!, 'sec-fetch-site': 'cross-site' } });
    expect(crossSite.statusCode).toBe(403);
  });

  it('leaves cookie-less clients alone', async () => {
    const res = await app.inject({ method: 'POST', url: '/auth/nonce', headers: { 'content-type': 'application/json' }, payload: '{}' });
    expect(res.statusCode).toBe(200);
  });
});

describe('session cookie', () => {
  it('uses a __Host- cookie when Secure and clears it with the same attributes', async () => {
    const t = await makeApp({ COOKIE_SECURE: '1', ALLOW_TEST_LOGIN: '1' });
    const login = await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER }) });
    const set = String(login.headers['set-cookie']);
    expect(set).toMatch(/^__Host-sqc_pg_session=/);
    expect(set).toMatch(/Secure/);
    expect(set).toMatch(/Path=\//);
    const logout = await t.app.inject({ method: 'POST', url: '/auth/logout', ...json({}, cookieOf(login)) });
    const cleared = String(logout.headers['set-cookie']);
    expect(cleared).toMatch(/^__Host-sqc_pg_session=;/);
    expect(cleared).toMatch(/Secure/);
    expect(cleared).toMatch(/HttpOnly/);
    expect(cleared).toMatch(/SameSite=Lax/);
    await t.close();
  });

  it('replaces the previous session on a new sign-in', async () => {
    const first = await signIn(PUB_USER, USER);
    const { nonce } = (await app.inject({ method: 'POST', url: '/auth/nonce', ...json({}) })).json() as { nonce: string };
    const message = createAuthMessage({ domain: ORIGIN, nonce, address: USER, statement: 'Sign in', expirationTime: Math.floor(Date.now() / 1000) + 300 });
    await app.inject({ method: 'POST', url: '/auth/verify', ...json({ message, signatureB64: 'valid-signature', publicKeyB64: PUB_USER, address: USER }, first.cookie) });
    expect((await app.inject({ method: 'GET', url: '/auth/me', headers: { cookie: first.cookie! } })).statusCode).toBe(401);
  });

  it('refuses SameSite=None without Secure', () => {
    expect(() => loadConfig({ NODE_ENV: 'test', COOKIE_SAMESITE: 'none' })).toThrow(/COOKIE_SECURE/);
  });
});

describe('canonical addresses', () => {
  it('treats upper- and lowercase hex as the same account', async () => {
    const t = await makeApp({ ALLOW_TEST_LOGIN: '1' });
    const upper = `SQ:${USER.slice(3).toUpperCase()}`;
    const a = await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: upper }) });
    expect(a.json().address).toBe(USER.toLowerCase().replace(/^sq:/, 'SQ:'));
    const spec = instantiateTemplate(TEMPLATES.find((x) => x.id === 'wallet-dashboard')!);
    expect((await t.app.inject({ method: 'POST', url: '/projects', ...json({ spec }, cookieOf(a)) })).statusCode).toBe(201);
    const b = await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER.toLowerCase().replace(/^sq:/, 'SQ:') }) });
    const list = await t.app.inject({ method: 'GET', url: '/projects', headers: { cookie: cookieOf(b) } });
    expect(list.json().projects).toHaveLength(1);
    await t.close();
  });

  it('canonicalises ADMIN_ADDRESSES and rejects junk', () => {
    expect(loadConfig({ NODE_ENV: 'test', ADMIN_ADDRESSES: `SQ:${'AB'.repeat(32)}` }).adminAddresses).toEqual([`SQ:${'ab'.repeat(32)}`]);
    expect(() => loadConfig({ NODE_ENV: 'test', ADMIN_ADDRESSES: 'alice' })).toThrow(/ADMIN_ADDRESSES/);
  });
});

describe('config validation', () => {
  it('rejects origins with paths, bad ports and TTLs, and test-login on public hosts', () => {
    expect(() => loadConfig({ NODE_ENV: 'test', PLAYGROUND_ORIGINS: 'https://a.example/' })).toThrow(/bare origin/);
    expect(() => loadConfig({ NODE_ENV: 'test', PLAYGROUND_ORIGINS: 'a.example' })).toThrow(/PLAYGROUND_ORIGINS/);
    expect(() => loadConfig({ NODE_ENV: 'test', PORT: 'http' })).toThrow(/PORT/);
    expect(() => loadConfig({ NODE_ENV: 'test', SESSION_TTL_SEC: '5' })).toThrow(/SESSION_TTL_SEC/);
    expect(() => loadConfig({ NODE_ENV: 'development', ALLOW_TEST_LOGIN: '1', HOST: '0.0.0.0' })).toThrow(/ALLOW_TEST_LOGIN/);
    expect(loadConfig({ NODE_ENV: 'development', ALLOW_TEST_LOGIN: '1', HOST: '127.0.0.1' }).allowTestLogin).toBe(true);
  });

  it('warns about SQLite in production', () => {
    expect(loadConfig({ NODE_ENV: 'production', PLAYGROUND_ORIGINS: 'https://p.example' }).warnings.join()).toMatch(/SQLite/);
  });
});

describe('errors, headers and schemas', () => {
  it('never leaks internal error details', async () => {
    const t = await makeApp({}, (s) => Object.assign(Object.create(s) as Store, { getFlags: async () => { throw new Error('relation "secret_table" does not exist'); } }));
    const res = await t.app.inject({ method: 'GET', url: '/catalog' });
    expect(res.statusCode).toBe(500);
    expect(res.json()).toEqual({ error: 'Internal error', requestId: expect.any(String) });
    expect(res.body).not.toContain('secret_table');
    await t.close();
  });

  it('sets security and cache headers', async () => {
    const me = await app.inject({ method: 'GET', url: '/auth/me', headers: { 'x-request-id': 'trace-123' } });
    expect(me.headers['cache-control']).toBe('no-store');
    expect(me.headers['x-content-type-options']).toBe('nosniff');
    expect(me.headers['x-frame-options']).toBe('DENY');
    expect(me.headers['x-request-id']).toBe('trace-123');
    expect((await app.inject({ method: 'GET', url: '/catalog' })).headers['cache-control']).toBe('no-cache');
    const bogus = await app.inject({ method: 'GET', url: '/health', headers: { 'x-request-id': 'bad id with spaces' } });
    expect(bogus.headers['x-request-id']).not.toBe('bad id with spaces');
  });

  it('rejects unknown fields and wrong types instead of coercing them', async () => {
    const { cookie } = await signIn(PUB_ADMIN, ADMIN);
    const spec = instantiateTemplate(TEMPLATES[0]!);
    const extra = await app.inject({ method: 'POST', url: '/projects', ...json({ spec, owner: USER }, cookie) });
    expect(extra.statusCode).toBe(400);
    const coerced = await app.inject({ method: 'POST', url: '/admin/releases', ...json({ channel: 'next', dryRun: 'false' }, cookie) });
    expect(coerced.statusCode).toBe(400);
    const flag = await app.inject({ method: 'PUT', url: '/admin/catalog/transfer', ...json({ hidden: true, updatedAt: 'x' }, cookie) });
    expect(flag.statusCode).toBe(400);
  });
});

describe('store maintenance', () => {
  it('migrates idempotently', async () => {
    await store.migrate();
    await store.migrate();
    await expect(store.ping()).resolves.toBeUndefined();
  });

  it('purges expired sessions and nonces and old audit rows', async () => {
    const past = Math.floor(Date.now() / 1000) - 10;
    await store.createSession('expired-hash', USER, past);
    await store.putNonce('old-nonce', past);
    await store.audit(USER, 'test.old');
    const { runHousekeeping } = await import('./housekeeping.js');
    const removed = await runHousekeeping(store, { auditRetentionDays: 1 }, Date.now() + 2 * 24 * 3600 * 1000);
    expect(removed.sessions).toBeGreaterThanOrEqual(1);
    expect(removed.nonces).toBeGreaterThanOrEqual(1);
    expect(removed.audit).toBeGreaterThanOrEqual(1);
    expect(await store.listAudit(10)).toEqual([]);
  });

  it('reports readiness from the database', async () => {
    expect((await app.inject({ method: 'GET', url: '/health/ready' })).json()).toEqual({ ok: true });
    const t = await makeApp({}, (s) => Object.assign(Object.create(s) as Store, { ping: async () => { throw new Error('down'); } }));
    expect((await t.app.inject({ method: 'GET', url: '/health/ready' })).statusCode).toBe(503);
    await t.close();
  });
});

describe('release service', () => {
  it('times out a stalled upstream and reports it instead of hanging', async () => {
    const stalled: FetchLike = (_url, init) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'TimeoutError' })));
      });
    const config = loadConfig({ NODE_ENV: 'test', GITHUB_TOKEN: 't' });
    const svc = createReleaseService(config.github, stalled, { timeoutMs: 50 });
    const status = await svc.status(true);
    expect(status.errors.length).toBeGreaterThan(0);
    expect(status.errors.join('\n')).toMatch(/timed out/);
    await expect(svc.dispatch({ channel: 'next', dryRun: true })).rejects.toThrow(/timed out/);
  });
});

describe('deployments', () => {
  it('is disabled unless HOSTING_ENABLED', async () => {
    const { cookie } = await signIn(PUB_USER, USER);
    const res = await app.inject({ method: 'POST', url: '/deployments', ...json({ projectId: 'xxxxxxxxxxxx', subdomain: 'demo' }, cookie) });
    expect(res.statusCode).toBe(404);
  });
});
