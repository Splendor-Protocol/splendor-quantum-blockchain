import { randomBytes } from 'node:crypto';
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

/**
 * `TEST_DATABASE_URL=postgres://…` runs the suite on Postgres: each store gets its own schema,
 * dropped when it closes. Default: in-memory SQLite.
 */
const TEST_DB = process.env.TEST_DATABASE_URL ?? 'sqlite::memory:';
const isPg = /^postgres(ql)?:\/\//.test(TEST_DB);

async function withPgAdmin(sql: string) {
  const { default: pg } = await import('pg');
  const c = new pg.Client({ connectionString: TEST_DB });
  await c.connect();
  try {
    await c.query(sql);
  } finally {
    await c.end();
  }
}

async function newStore(): Promise<Store> {
  if (!isPg) return createStore('sqlite::memory:');
  const schema = `t_${randomBytes(6).toString('hex')}`;
  await withPgAdmin(`CREATE SCHEMA ${schema}`);
  const s = await createStore(TEST_DB, { schema });
  const close = s.close.bind(s);
  s.close = async () => {
    await close();
    await withPgAdmin(`DROP SCHEMA ${schema} CASCADE`);
  };
  return s;
}

let store: Store;
let app: App;
let gh: ReturnType<typeof fakeGitHub>;

beforeEach(async () => {
  store = await newStore();
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
    expect(replay.json()).toEqual({ error: 'Sign-in failed', code: 'AUTH_FAILED' });
  });

  it('rejects bad signatures, foreign domains and key/address mismatches', async () => {
    expect((await signIn(PUB_USER, USER, { signature: 'forged' })).res.statusCode).toBe(401);
    expect((await signIn(PUB_USER, USER, { domain: 'https://evil.example' })).res.json().error).toMatch(/another site/);
    const mismatch = (await signIn(PUB_USER, ADMIN)).res;
    expect(mismatch.statusCode).toBe(401);
    expect(mismatch.json()).toEqual({ error: 'Sign-in failed', code: 'AUTH_FAILED' });
  });

  it('gives every failed sign-in the same answer, and keeps the reason in the audit log', async () => {
    const bodies = [
      (await signIn(PUB_USER, USER, { signature: 'forged' })).res.json(),
      (await signIn(PUB_USER, ADMIN)).res.json(),
    ];
    for (const b of bodies) expect(b).toEqual({ error: 'Sign-in failed', code: 'AUTH_FAILED' });
    const reasons = (await store.listAudit(10)).filter((e) => e.action === 'auth.failed').map((e) => JSON.parse(e.detail!).reason);
    expect(reasons).toEqual(expect.arrayContaining(['bad-signature', 'key-mismatch']));
  });

  it('rejects a sign-in without a Chain ID when the server pins one', async () => {
    const pinned = await buildApp({
      config: loadConfig({ NODE_ENV: 'test', PLAYGROUND_ORIGINS: ORIGIN, SIGNIN_CHAIN_ID: 'splendor-quantum-1' }),
      store,
      verifier: async (_pk, _msg, sig) => sig === 'valid-signature',
    });
    try {
      const attempt = async (chainId?: string) => {
        const { nonce } = (await pinned.inject({ method: 'POST', url: '/auth/nonce', ...json({}) })).json() as { nonce: string };
        const message = createAuthMessage({ domain: ORIGIN, nonce, address: USER, expirationTime: Math.floor(Date.now() / 1000) + 300, ...(chainId ? { chainId } : {}) });
        return pinned.inject({ method: 'POST', url: '/auth/verify', ...json({ message, signatureB64: 'valid-signature', publicKeyB64: PUB_USER, address: USER }) });
      };
      expect((await attempt()).statusCode).toBe(401);
      expect((await attempt('splendor-testnet')).statusCode).toBe(401);
      expect((await attempt('splendor-quantum-1')).statusCode).toBe(200);
    } finally {
      await pinned.close();
    }
  });

  it('sends security headers on every response', async () => {
    const res = await app.inject({ method: 'GET', url: '/health' });
    expect(res.headers['content-security-policy']).toBe("default-src 'none'; frame-ancestors 'none'");
    expect(res.headers['referrer-policy']).toBe('no-referrer');
    expect(res.headers['x-content-type-options']).toBe('nosniff');
    // Tests run without Secure cookies, so no HSTS.
    expect(res.headers['strict-transport-security']).toBeUndefined();
  });

  it('rate-limits public share reads more tightly than the global limit', async () => {
    let last = 0;
    for (let i = 0; i < 61; i++) last = (await app.inject({ method: 'GET', url: '/share/unknown-share-id' })).statusCode;
    expect(last).toBe(429);
    // Other routes still answer: the share limit has its own bucket.
    expect((await app.inject({ method: 'GET', url: '/health' })).statusCode).toBe(200);
  });

  it('refuses TRUST_PROXY=true in production unless explicitly allowed', () => {
    const base = { NODE_ENV: 'production', PLAYGROUND_ORIGINS: 'https://play.example.com', TRUST_PROXY: 'true' };
    expect(() => loadConfig(base)).toThrow(/TRUST_PROXY=true/);
    expect(loadConfig({ ...base, TRUST_PROXY_ALLOW_ALL: '1' }).trustProxy).toBe(true);
    expect(loadConfig({ ...base, TRUST_PROXY: 'loopback' }).trustProxy).toEqual(['loopback']);
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

describe('workspaces', () => {
  const spec = () => instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!);

  it('creates, lists, renames and deletes workspaces; projects can bind to one', async () => {
    const user = await signIn(PUB_USER, USER);
    expect((await app.inject({ method: 'GET', url: '/workspaces' })).statusCode).toBe(401);

    const created = await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Studio' }, user.cookie) });
    expect(created.statusCode).toBe(201);
    const wsId = created.json().id as string;

    const listed = await app.inject({ method: 'GET', url: '/workspaces', headers: { cookie: user.cookie! } });
    expect(listed.json().workspaces.some((w: { id: string }) => w.id === wsId)).toBe(true);

    const renamed = await app.inject({ method: 'PATCH', url: `/workspaces/${wsId}`, ...json({ name: 'Renamed WS' }, user.cookie) });
    expect(renamed.json().name).toBe('Renamed WS');

    const project = await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec(), workspaceId: wsId }, user.cookie) });
    expect(project.statusCode).toBe(201);
    expect(project.json().workspaceId).toBe(wsId);

    const inWs = await app.inject({ method: 'GET', url: `/workspaces/${wsId}/projects`, headers: { cookie: user.cookie! } });
    expect(inWs.json().projects).toHaveLength(1);

    const filtered = await app.inject({ method: 'GET', url: `/projects?workspaceId=${encodeURIComponent(wsId)}`, headers: { cookie: user.cookie! } });
    expect(filtered.json().projects).toHaveLength(1);

    const del = await app.inject({ method: 'DELETE', url: `/workspaces/${wsId}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect(del.statusCode).toBe(200);
    // The workspace and its project go to the trash together; nothing is left orphaned.
    const after = await app.inject({ method: 'GET', url: '/projects', headers: { cookie: user.cookie! } });
    expect(after.json().projects).toEqual([]);
    const trash = (await app.inject({ method: 'GET', url: '/trash', headers: { cookie: user.cookie! } })).json();
    expect(trash.workspaces.map((w: { id: string }) => w.id)).toEqual([wsId]);
    expect(trash.projects.map((p: { id: string }) => p.id)).toEqual([project.json().id]);
  });

  it('restores a trashed workspace with the projects deleted with it, and purges for good', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const get = (url: string, cookie = user.cookie) => app.inject({ method: 'GET', url, headers: { cookie: cookie! } });
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Team' }, user.cookie) })).json().id as string;
    const a = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec(), workspaceId: ws }, user.cookie) })).json().id as string;
    const b = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec(), workspaceId: ws }, user.cookie) })).json().id as string;
    // b was deleted on its own before the workspace: restoring the workspace leaves it in the trash.
    await app.inject({ method: 'DELETE', url: `/projects/${b}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    await new Promise((r) => setTimeout(r, 5));
    await app.inject({ method: 'DELETE', url: `/workspaces/${ws}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect((await get(`/projects/${a}`)).statusCode).toBe(404);
    expect((await get('/workspaces')).json().workspaces).toEqual([]);

    const restore = (url: string, cookie = user.cookie) => app.inject({ method: 'POST', url, ...json({}, cookie) });
    expect((await restore(`/trash/workspaces/${ws}/restore`, other.cookie)).statusCode).toBe(404);
    expect((await restore(`/trash/workspaces/${ws}/restore`)).statusCode).toBe(200);
    expect((await get(`/workspaces/${ws}/projects`)).json().projects.map((p: { id: string }) => p.id)).toEqual([a]);
    expect((await get('/trash')).json().projects.map((p: { id: string }) => p.id)).toEqual([b]);

    // A project restored on its own returns to its (live) workspace.
    expect((await restore(`/trash/projects/${b}/restore`)).statusCode).toBe(200);
    expect((await get(`/projects/${b}`)).json().workspaceId).toBe(ws);
    expect((await restore(`/trash/projects/${b}/restore`)).statusCode).toBe(404);

    // Purge: only from the trash, then gone with its revisions.
    const purge = (url: string) => app.inject({ method: 'DELETE', url, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect((await purge(`/trash/projects/${a}`)).statusCode).toBe(404);
    await app.inject({ method: 'DELETE', url: `/workspaces/${ws}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect((await purge(`/trash/workspaces/${ws}`)).statusCode).toBe(200);
    expect((await get('/trash')).json()).toEqual({ projects: [], workspaces: [], nextCursor: null });
    expect(await store.getProject(a)).toBeNull();
    expect(await store.listRevisions(a)).toEqual([]);
  });
});

describe('trash, revisions, search and audit', () => {
  const spec = (name = 'Doc') => ({ ...instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!), name });

  it('a deleted project leaves the list, its share link stops, and it can be restored', async () => {
    const user = await signIn(PUB_USER, USER);
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) })).json().id as string;
    const shareId = (await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) })).json().shareId as string;
    await app.inject({ method: 'DELETE', url: `/projects/${id}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    expect((await app.inject({ method: 'GET', url: '/projects', headers: { cookie: user.cookie! } })).json().projects).toEqual([]);
    expect((await app.inject({ method: 'GET', url: `/share/${shareId}` })).statusCode).toBe(404);
    expect((await app.inject({ method: 'PUT', url: `/projects/${id}`, ...json({ spec: spec() }, user.cookie) })).statusCode).toBe(404);
    expect((await app.inject({ method: 'POST', url: `/trash/projects/${id}/restore`, ...json({}, user.cookie) })).statusCode).toBe(200);
    expect((await app.inject({ method: 'GET', url: `/share/${shareId}` })).statusCode).toBe(200);
  });

  it('keeps a revision per saved spec, at most 20, and restores one as a new version', async () => {
    const user = await signIn(PUB_USER, USER);
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec('v1') }, user.cookie) })).json().id as string;
    for (let v = 2; v <= 23; v++) {
      const r = await app.inject({ method: 'PUT', url: `/projects/${id}`, ...json({ spec: spec(`v${v}`), baseVersion: v - 1 }, user.cookie) });
      expect(r.statusCode).toBe(200);
    }
    const list = (await app.inject({ method: 'GET', url: `/projects/${id}/revisions`, headers: { cookie: user.cookie! } })).json().revisions as { version: number }[];
    expect(list).toHaveLength(20);
    expect(list[0]!.version).toBe(23);
    expect(list[list.length - 1]!.version).toBe(4);
    const old = await app.inject({ method: 'GET', url: `/projects/${id}/revisions/10`, headers: { cookie: user.cookie! } });
    expect(old.json().spec.name).toBe('v10');
    expect((await app.inject({ method: 'GET', url: `/projects/${id}/revisions/2`, headers: { cookie: user.cookie! } })).statusCode).toBe(404);

    const restore = (baseVersion: number) => app.inject({ method: 'POST', url: `/projects/${id}/revisions/10/restore`, ...json({ baseVersion }, user.cookie) });
    expect((await restore(22)).statusCode).toBe(409);
    const restored = await restore(23);
    expect(restored.json()).toMatchObject({ name: 'v10', version: 24 });
    expect((await app.inject({ method: 'GET', url: `/projects/${id}/revisions`, headers: { cookie: user.cookie! } })).json().revisions[0]).toMatchObject({ version: 24 });

    // A move between workspaces saves no new content, so it adds no revision.
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'W' }, user.cookie) })).json().id as string;
    await app.inject({ method: 'PATCH', url: `/projects/${id}`, ...json({ workspaceId: ws }, user.cookie) });
    expect((await app.inject({ method: 'GET', url: `/projects/${id}/revisions`, headers: { cookie: user.cookie! } })).json().revisions[0].version).toBe(24);
  });

  it('searches, sorts and pages project lists', async () => {
    const user = await signIn(PUB_USER, USER);
    for (const name of ['Alpha shop', 'beta Shop', 'Gamma', 'delta 100%', 'Echo_1']) {
      await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec(name) }, user.cookie) });
      await new Promise((r) => setTimeout(r, 3));
    }
    const list = async (qs: string) => (await app.inject({ method: 'GET', url: `/projects?${qs}`, headers: { cookie: user.cookie! } })).json() as { projects: { name: string }[]; nextCursor: string | null };
    expect((await list('q=shop&sort=name')).projects.map((p) => p.name)).toEqual(['Alpha shop', 'beta Shop']);
    expect((await list('q=100%25')).projects.map((p) => p.name)).toEqual(['delta 100%']);
    expect((await list('q=o_')).projects.map((p) => p.name)).toEqual(['Echo_1']);

    const names: string[] = [];
    let cursor: string | null = null;
    do {
      const page = await list(`sort=name&limit=2${cursor ? `&cursor=${cursor}` : ''}`);
      expect(page.projects.length).toBeLessThanOrEqual(2);
      names.push(...page.projects.map((p) => p.name));
      cursor = page.nextCursor;
    } while (cursor);
    expect(names).toEqual(['Alpha shop', 'beta Shop', 'delta 100%', 'Echo_1', 'Gamma']);

    const newest = await list('limit=3');
    expect(newest.projects.map((p) => p.name)).toEqual(['Echo_1', 'delta 100%', 'Gamma']);
    const rest = await list(`limit=3&cursor=${newest.nextCursor}`);
    expect(rest.projects.map((p) => p.name)).toEqual(['beta Shop', 'Alpha shop']);
    expect(rest.nextCursor).toBeNull();
    expect((await app.inject({ method: 'GET', url: '/projects?limit=0', headers: { cookie: user.cookie! } })).statusCode).toBe(400);
  });

  it('audits project, workspace, share and logout events, once per window for autosaves', async () => {
    const user = await signIn(PUB_USER, USER);
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'W' }, user.cookie) })).json().id as string;
    await app.inject({ method: 'PATCH', url: `/workspaces/${ws}`, ...json({ name: 'W2' }, user.cookie) });
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) })).json().id as string;
    await app.inject({ method: 'PUT', url: `/projects/${id}`, ...json({ spec: spec('a') }, user.cookie) });
    await app.inject({ method: 'PUT', url: `/projects/${id}`, ...json({ spec: spec('b') }, user.cookie) });
    await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) });
    await app.inject({ method: 'DELETE', url: `/projects/${id}/share`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    await app.inject({ method: 'DELETE', url: `/projects/${id}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    await app.inject({ method: 'DELETE', url: `/workspaces/${ws}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    await app.inject({ method: 'POST', url: '/auth/logout', ...json({}, user.cookie) });
    const actions = (await store.listAudit(50)).filter((e) => e.actor === USER).map((e) => e.action).reverse();
    expect(actions).toEqual([
      'auth.signin',
      'workspace.create',
      'workspace.update',
      'project.create',
      'project.update',
      'project.share',
      'project.unshare',
      'project.delete',
      'workspace.delete',
      'auth.logout',
    ]);
  });

  it('share links can expire and count their views', async () => {
    const user = await signIn(PUB_USER, USER);
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) })).json().id as string;
    const shared = (await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({ expiresInDays: 7 }, user.cookie) })).json();
    expect(Date.parse(shared.expiresAt) - Date.now()).toBeGreaterThan(6.9 * 24 * 3600 * 1000);
    await app.inject({ method: 'GET', url: `/share/${shared.shareId}` });
    await app.inject({ method: 'GET', url: `/share/${shared.shareId}` });
    const listed = (await app.inject({ method: 'GET', url: '/projects', headers: { cookie: user.cookie! } })).json().projects[0];
    expect(listed).toMatchObject({ shareId: shared.shareId, shareViews: 2, shareExpiresAt: shared.expiresAt });
    expect((await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({ expiresInDays: 0 }, user.cookie) })).statusCode).toBe(400);

    // Past its expiry the link is gone, and sharing again issues a new one.
    await store.setShareId(id, shared.shareId, new Date(Date.now() - 1000).toISOString());
    const gone = await app.inject({ method: 'GET', url: `/share/${shared.shareId}` });
    expect(gone.statusCode).toBe(404);
    expect(gone.json().code).toBe('EXPIRED');
    const again = (await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) })).json();
    expect(again.shareId).not.toBe(shared.shareId);
    expect(again.expiresAt).toBeNull();
  });

  it('housekeeping purges trash older than 30 days', async () => {
    const user = await signIn(PUB_USER, USER);
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) })).json().id as string;
    await app.inject({ method: 'DELETE', url: `/projects/${id}`, headers: { origin: ORIGIN, cookie: user.cookie! } });
    const { runHousekeeping } = await import('./housekeeping.js');
    expect((await runHousekeeping(store, { auditRetentionDays: 365 })).trash).toBe(0);
    expect((await runHousekeeping(store, { auditRetentionDays: 365 }, Date.now() + 31 * 24 * 3600 * 1000)).trash).toBe(1);
    expect(await store.getProject(id)).toBeNull();
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

  it('revokes a share link, and sharing again issues a new one', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const id = (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec() }, user.cookie) })).json().id as string;
    const first = (await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) })).json().shareId as string;

    const unshare = (cookie: string | undefined) =>
      app.inject({ method: 'DELETE', url: `/projects/${id}/share`, headers: { origin: ORIGIN, cookie: cookie! } });
    expect((await unshare(other.cookie)).statusCode).toBe(404);
    expect((await app.inject({ method: 'GET', url: `/share/${first}` })).statusCode).toBe(200);

    expect((await unshare(user.cookie)).statusCode).toBe(200);
    expect((await app.inject({ method: 'GET', url: `/share/${first}` })).statusCode).toBe(404);
    expect((await app.inject({ method: 'GET', url: `/projects/${id}`, headers: { cookie: user.cookie! } })).json().shareId).toBeNull();

    const second = (await app.inject({ method: 'POST', url: `/projects/${id}/share`, ...json({}, user.cookie) })).json().shareId as string;
    expect(second).not.toBe(first);
    expect((await app.inject({ method: 'GET', url: `/share/${first}` })).statusCode).toBe(404);
  });
});

describe('project versions, rename and duplicate', () => {
  const spec = (name = 'Versioned') => ({ ...instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!), name });
  const create = async (cookie: string | undefined, body: Record<string, unknown> = { spec: spec() }) =>
    (await app.inject({ method: 'POST', url: '/projects', ...json(body, cookie) })).json() as { id: string; version: number; workspaceId: string | null };

  it('starts at version 1 and bumps it on every save', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    expect(p.version).toBe(1);
    const saved = await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec('Two'), baseVersion: 1 }, user.cookie) });
    expect(saved.statusCode).toBe(200);
    expect(saved.json()).toMatchObject({ name: 'Two', version: 2 });
    const got = await app.inject({ method: 'GET', url: `/projects/${p.id}`, headers: { cookie: user.cookie! } });
    expect(got.json().version).toBe(2);
    const listed = await app.inject({ method: 'GET', url: '/projects', headers: { cookie: user.cookie! } });
    expect(listed.json().projects[0].version).toBe(2);
  });

  it('refuses a save based on an older version with 409 CONFLICT, and keeps the newer copy', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec('Tab A'), baseVersion: 1 }, user.cookie) });

    const stale = await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec('Tab B'), baseVersion: 1 }, user.cookie) });
    expect(stale.statusCode).toBe(409);
    expect(stale.json()).toMatchObject({ code: 'CONFLICT', current: { id: p.id, name: 'Tab A', version: 2 } });
    expect(stale.json().current).not.toHaveProperty('spec');
    const got = await app.inject({ method: 'GET', url: `/projects/${p.id}`, headers: { cookie: user.cookie! } });
    expect(got.json()).toMatchObject({ name: 'Tab A', version: 2 });

    // "Keep my version": saving on top of the current version applies.
    const retry = await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec('Tab B'), baseVersion: 2 }, user.cookie) });
    expect(retry.json()).toMatchObject({ name: 'Tab B', version: 3 });
    expect(retry.headers.deprecation).toBeUndefined();
    // Without baseVersion the save always applies (older clients), and is flagged as deprecated.
    const blind = await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec('Blind') }, user.cookie) });
    expect(blind.json()).toMatchObject({ name: 'Blind', version: 4 });
    expect(blind.headers.deprecation).toBe('true');
    expect((await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec(), baseVersion: 0 }, user.cookie) })).statusCode).toBe(400);
  });

  it('PATCH renames (in the spec too) and moves a project between the owner’s workspaces', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Team' }, user.cookie) })).json().id as string;
    const foreignWs = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Theirs' }, other.cookie) })).json().id as string;
    const p = await create(user.cookie);

    const patch = (body: unknown, cookie = user.cookie) => app.inject({ method: 'PATCH', url: `/projects/${p.id}`, ...json(body, cookie) });
    const renamed = await patch({ name: '  New name  ' });
    expect(renamed.statusCode).toBe(200);
    expect(renamed.json()).toMatchObject({ name: 'New name', version: 2 });
    const got = await app.inject({ method: 'GET', url: `/projects/${p.id}`, headers: { cookie: user.cookie! } });
    expect(got.json().spec.name).toBe('New name');

    expect((await patch({ workspaceId: ws })).json()).toMatchObject({ workspaceId: ws, version: 3 });
    expect((await patch({ workspaceId: null })).json().workspaceId).toBeNull();
    expect((await patch({ workspaceId: foreignWs })).statusCode).toBe(400);
    expect((await patch({ name: '   ' })).statusCode).toBe(400);
    expect((await patch({})).statusCode).toBe(400);
    expect((await patch({ name: 'x', baseVersion: 1 })).statusCode).toBe(409);
    expect((await patch({ name: 'Mine' }, other.cookie)).statusCode).toBe(404);
  });

  it('PATCH without baseVersion cannot write an older spec over a save that landed after it read the row', async () => {
    // The rename reads version 1; a studio save moves the row to version 2 before the rename writes.
    let stale: Awaited<ReturnType<Store['getProject']>> = null;
    const t = await makeApp({ ALLOW_TEST_LOGIN: '1' }, (s) =>
      Object.assign(Object.create(s) as Store, { getProject: async (id: string) => stale ?? s.getProject(id) }),
    );
    const login = await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER }) });
    const cookie = cookieOf(login);
    const p = (await t.app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec('Before') }, cookie) })).json() as { id: string };
    stale = await t.store.getProject(p.id);
    await t.store.updateProject(p.id, { name: 'Studio save', spec: JSON.stringify(spec('Studio save')) }, 1);
    const rename = await t.app.inject({ method: 'PATCH', url: `/projects/${p.id}`, ...json({ name: 'Renamed' }, cookie) });
    expect(rename.statusCode).toBe(409);
    stale = null;
    expect((await t.store.getProject(p.id))?.name).toBe('Studio save');
    await t.close();
  });

  it('duplicates a project into the same workspace, as a separate copy', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Team' }, user.cookie) })).json().id as string;
    const p = await create(user.cookie, { spec: spec('Original'), workspaceId: ws });

    const dup = await app.inject({ method: 'POST', url: `/projects/${p.id}/duplicate`, ...json({}, user.cookie) });
    expect(dup.statusCode).toBe(201);
    expect(dup.json()).toMatchObject({ name: 'Original copy', workspaceId: ws, version: 1, shareId: null });
    expect(dup.json().id).not.toBe(p.id);
    expect(dup.json().spec.name).toBe('Original copy');
    expect((await app.inject({ method: 'POST', url: `/projects/${p.id}/duplicate`, ...json({}, other.cookie) })).statusCode).toBe(404);
  });

  it('duplicate respects the per-user project limit', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    for (let i = 1; i < 100; i++) await store.createProject({ id: `filler-project-${String(i).padStart(3, '0')}`, owner: USER, name: 'f', spec: '{}' });
    const dup = await app.inject({ method: 'POST', url: `/projects/${p.id}/duplicate`, ...json({}, user.cookie) });
    expect(dup.statusCode).toBe(429);
    expect(dup.json().code).toBe('PROJECT_LIMIT');
  });

  it('concurrent creates cannot take an owner past the project limit', async () => {
    const user = await signIn(PUB_USER, USER);
    for (let i = 0; i < 95; i++) await store.createProject({ id: `filler-project-${String(i).padStart(3, '0')}`, owner: USER, name: 'f', spec: '{}' });
    const spec = { ...instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!), name: 'Race' };
    const results = await Promise.all(
      Array.from({ length: 12 }, () => app.inject({ method: 'POST', url: '/projects', ...json({ spec }, user.cookie) })),
    );
    expect(results.filter((r) => r.statusCode === 201)).toHaveLength(5);
    expect(results.filter((r) => r.statusCode === 429)).toHaveLength(7);
    expect(await store.countProjects(USER)).toBe(100);
  });

  // SQLite file surgery; the Postgres run covers migrations through every fresh schema.
  it.skipIf(isPg)('migration 4 gives projects saved before versions a version of 1', async () => {
    const { mkdtempSync, rmSync } = await import('node:fs');
    const { tmpdir } = await import('node:os');
    const { join } = await import('node:path');
    const { DatabaseSync } = await import('node:sqlite');
    const dir = mkdtempSync(join(tmpdir(), 'pg-api-'));
    const file = join(dir, 'old.db');
    try {
      const fresh = await createStore(`sqlite:${file}`);
      await fresh.createProject({ id: 'old-project-0001', owner: USER, name: 'Old', spec: '{}' });
      await fresh.close();
      // Roll the file back to schema 3, as a database from before this migration.
      const raw = new DatabaseSync(file);
      raw.exec('ALTER TABLE projects DROP COLUMN version; DELETE FROM schema_migrations WHERE id = 4;');
      raw.close();

      const upgraded = await createStore(`sqlite:${file}`);
      expect((await upgraded.getProject('old-project-0001'))?.version).toBe(1);
      expect((await upgraded.updateProject('old-project-0001', { name: 'New' }, 1))?.version).toBe(2);
      await upgraded.close();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe('api hardening', () => {
  it('allows PATCH in the CORS preflight', async () => {
    const res = await app.inject({
      method: 'OPTIONS',
      url: '/workspaces/abcdefghij12',
      headers: { origin: ORIGIN, 'access-control-request-method': 'PATCH' },
    });
    expect(res.statusCode).toBe(204);
    expect(String(res.headers['access-control-allow-methods'])).toMatch(/PATCH/);
  });

  it('answers unknown routes and bad input with a code and a request id', async () => {
    const missing = await app.inject({ method: 'GET', url: '/nope', headers: { 'x-request-id': 'req-123' } });
    expect(missing.statusCode).toBe(404);
    expect(missing.json()).toEqual({ error: 'Not found', code: 'NOT_FOUND', requestId: 'req-123' });

    const user = await signIn(PUB_USER, USER);
    const bad = await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'x'.repeat(201) }, user.cookie) });
    expect(bad.statusCode).toBe(400);
    expect(bad.json()).toMatchObject({ code: 'INVALID_REQUEST', requestId: expect.any(String), issues: expect.any(Array) });
    expect((await app.inject({ method: 'GET', url: '/projects?workspaceId=../x', headers: { cookie: user.cookie! } })).statusCode).toBe(400);
    expect((await app.inject({ method: 'POST', url: '/projects', ...json({ spec: {}, workspaceId: 'short' }, user.cookie) })).statusCode).toBe(400);
  });

  it('keeps workspaces private to their owner', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const ws = (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name: 'Mine' }, user.cookie) })).json().id as string;
    expect((await app.inject({ method: 'PATCH', url: `/workspaces/${ws}`, ...json({ name: 'Stolen' }, other.cookie) })).statusCode).toBe(404);
    expect((await app.inject({ method: 'DELETE', url: `/workspaces/${ws}`, headers: { origin: ORIGIN, cookie: other.cookie! } })).statusCode).toBe(404);
    expect((await app.inject({ method: 'GET', url: `/workspaces/${ws}/projects`, headers: { cookie: other.cookie! } })).statusCode).toBe(404);
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
  const s = await newStore();
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
    expect(() => loadConfig({ NODE_ENV: 'test', ALLOW_TEST_LOGIN: '1', HOST: '0.0.0.0' })).toThrow(/loopback HOST/);
    expect(loadConfig({ NODE_ENV: 'test', ALLOW_TEST_LOGIN: '1', HOST: '::1' }).allowTestLogin).toBe(true);
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

  it('reports a taken subdomain as 409 and any other failure as 500', async () => {
    let fail: Error | null = null;
    const t = await makeApp({ HOSTING_ENABLED: '1', ALLOW_TEST_LOGIN: '1' }, (s) =>
      Object.assign(Object.create(s) as Store, { createDeployment: (row: Parameters<Store['createDeployment']>[0]) => (fail ? Promise.reject(fail) : s.createDeployment(row)) }),
    );
    const cookie = cookieOf(await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER }) }));
    const spec = instantiateTemplate(TEMPLATES.find((x) => x.id === 'wallet-dashboard')!);
    const id = (await t.app.inject({ method: 'POST', url: '/projects', ...json({ spec }, cookie) })).json().id as string;
    const deploy = () => t.app.inject({ method: 'POST', url: '/deployments', ...json({ projectId: id, subdomain: 'demo' }, cookie) });
    expect((await deploy()).statusCode).toBe(202);
    expect((await deploy()).statusCode).toBe(409);
    fail = new Error('connection reset');
    expect((await deploy()).statusCode).toBe(500);
    await t.close();
  });

  it('a failed audit write does not turn a created deployment into an error', async () => {
    const t = await makeApp({ HOSTING_ENABLED: '1', ALLOW_TEST_LOGIN: '1' }, (s) =>
      Object.assign(Object.create(s) as Store, {
        audit: (actor: string, action: string, target?: string | null, detail?: unknown) =>
          action === 'deployment.create' ? Promise.reject(new Error('audit table locked')) : s.audit(actor, action, target, detail),
      }),
    );
    const cookie = cookieOf(await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER }) }));
    const spec = instantiateTemplate(TEMPLATES.find((x) => x.id === 'wallet-dashboard')!);
    const id = (await t.app.inject({ method: 'POST', url: '/projects', ...json({ spec }, cookie) })).json().id as string;
    const res = await t.app.inject({ method: 'POST', url: '/deployments', ...json({ projectId: id, subdomain: 'demo' }, cookie) });
    expect(res.statusCode).toBe(202);
    await t.close();
  });
});

describe('review scenarios: ownership, limits, paging, revisions and purge', () => {
  const spec = (name = 'Doc') => ({ ...instantiateTemplate(TEMPLATES.find((t) => t.id === 'wallet-dashboard')!), name });
  const create = async (cookie: string | undefined, name = 'Doc', workspaceId?: string) =>
    (await app.inject({ method: 'POST', url: '/projects', ...json({ spec: spec(name), ...(workspaceId ? { workspaceId } : {}) }, cookie) })).json() as {
      id: string;
      version: number;
      workspaceId: string | null;
    };
  const newWorkspace = async (cookie: string | undefined, name = 'W') =>
    (await app.inject({ method: 'POST', url: '/workspaces', ...json({ name }, cookie) })).json().id as string;
  const get = (url: string, cookie: string | undefined) => app.inject({ method: 'GET', url, headers: { cookie: cookie! } });
  const del = (url: string, cookie: string | undefined) => app.inject({ method: 'DELETE', url, headers: { origin: ORIGIN, cookie: cookie! } });
  const post = (url: string, body: unknown, cookie: string | undefined) => app.inject({ method: 'POST', url, ...json(body, cookie) });

  it('keeps revisions and the trash private to their owner', async () => {
    const user = await signIn(PUB_USER, USER);
    const other = await signIn(PUB_ADMIN, ADMIN);
    const p = await create(user.cookie);
    const ws = await newWorkspace(user.cookie);
    expect((await get(`/projects/${p.id}/revisions`, other.cookie)).statusCode).toBe(404);
    expect((await get(`/projects/${p.id}/revisions/1`, other.cookie)).statusCode).toBe(404);
    expect((await post(`/projects/${p.id}/revisions/1/restore`, { baseVersion: 1 }, other.cookie)).statusCode).toBe(404);

    await del(`/projects/${p.id}`, user.cookie);
    await del(`/workspaces/${ws}`, user.cookie);
    expect((await post(`/trash/projects/${p.id}/restore`, {}, other.cookie)).statusCode).toBe(404);
    expect((await del(`/trash/projects/${p.id}`, other.cookie)).statusCode).toBe(404);
    expect((await post(`/trash/workspaces/${ws}/restore`, {}, other.cookie)).statusCode).toBe(404);
    expect((await del(`/trash/workspaces/${ws}`, other.cookie)).statusCode).toBe(404);
    expect((await get('/trash', other.cookie)).json()).toMatchObject({ projects: [], workspaces: [] });
    const mine = (await get('/trash', user.cookie)).json();
    expect(mine.projects.map((x: { id: string }) => x.id)).toEqual([p.id]);
    expect(mine.workspaces.map((x: { id: string }) => x.id)).toEqual([ws]);
  });

  it('a restore cannot take an owner past the project limit', async () => {
    const user = await signIn(PUB_USER, USER);
    const ws = await newWorkspace(user.cookie);
    await create(user.cookie, 'A', ws);
    await create(user.cookie, 'B', ws);
    await del(`/workspaces/${ws}`, user.cookie);
    for (let i = 0; i < 99; i++) await store.createProject({ id: `filler-project-${String(i).padStart(3, '0')}`, owner: USER, name: 'f', spec: '{}' });

    // 99 live + 2 coming back = 101.
    const refused = await post(`/trash/workspaces/${ws}/restore`, {}, user.cookie);
    expect(refused.statusCode).toBe(429);
    expect((await get('/workspaces', user.cookie)).json().workspaces).toEqual([]);

    await store.deleteProject('filler-project-000');
    expect((await post(`/trash/workspaces/${ws}/restore`, {}, user.cookie)).statusCode).toBe(200);
    expect(await store.countProjects(USER)).toBe(100);

    // At the limit, a single project cannot come back either.
    expect((await post('/trash/projects/filler-project-000/restore', {}, user.cookie)).statusCode).toBe(429);
  });

  it('a project restored while its workspace is trashed comes back unfiled, and stays put when the workspace returns', async () => {
    const user = await signIn(PUB_USER, USER);
    const ws = await newWorkspace(user.cookie);
    const p = await create(user.cookie, 'Lone', ws);
    const q = await create(user.cookie, 'Sibling', ws);
    await del(`/workspaces/${ws}`, user.cookie);
    expect((await post(`/trash/projects/${p.id}/restore`, {}, user.cookie)).statusCode).toBe(200);
    expect((await get(`/projects/${p.id}`, user.cookie)).json().workspaceId).toBeNull();

    expect((await post(`/trash/workspaces/${ws}/restore`, {}, user.cookie)).statusCode).toBe(200);
    expect((await get(`/projects/${p.id}`, user.cookie)).json().workspaceId).toBeNull();
    expect((await get(`/workspaces/${ws}/projects`, user.cookie)).json().projects.map((x: { id: string }) => x.id)).toEqual([q.id]);
  });

  it('hides the revisions of a trashed project, and a restore needs the version it starts from', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    expect((await post(`/projects/${p.id}/revisions/1/restore`, {}, user.cookie)).statusCode).toBe(400);
    await del(`/projects/${p.id}`, user.cookie);
    expect((await get(`/projects/${p.id}/revisions`, user.cookie)).statusCode).toBe(404);
    expect((await get(`/projects/${p.id}/revisions/1`, user.cookie)).statusCode).toBe(404);
  });

  it('keeps the newest 20 revisions even when moves skipped version numbers', async () => {
    const user = await signIn(PUB_USER, USER);
    const ws = await newWorkspace(user.cookie);
    const p = await create(user.cookie, 'v1');
    // 25 moves: versions 2..26 carry no new content, so no revisions.
    for (let i = 0; i < 25; i++) {
      const res = await app.inject({ method: 'PATCH', url: `/projects/${p.id}`, ...json({ workspaceId: i % 2 ? null : ws }, user.cookie) });
      expect(res.statusCode).toBe(200);
    }
    for (let v = 27; v <= 45; v++) {
      const res = await app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec(`v${v}`), baseVersion: v - 1 }, user.cookie) });
      expect(res.statusCode).toBe(200);
    }
    const versions = ((await get(`/projects/${p.id}/revisions`, user.cookie)).json().revisions as { version: number }[]).map((r) => r.version);
    expect(versions).toHaveLength(20);
    expect(versions[0]).toBe(45);
    expect(versions).toContain(1);
  });

  it('pages by name without skipping non-ASCII names, finds them by search, and rejects a bad cursor', async () => {
    const user = await signIn(PUB_USER, USER);
    const names = ['Проект', 'Работа', 'Сайт', 'Émile', 'émile', 'École', 'Zeta', 'alpha'];
    for (const n of names) await create(user.cookie, n);
    const seen: string[] = [];
    let cursor: string | null = null;
    for (let i = 0; i < 20; i++) {
      const page = (await get(`/projects?sort=name&limit=1${cursor ? `&cursor=${cursor}` : ''}`, user.cookie)).json() as { projects: { name: string }[]; nextCursor: string | null };
      seen.push(...page.projects.map((x) => x.name));
      cursor = page.nextCursor;
      if (!cursor) break;
    }
    expect([...seen].sort()).toEqual([...names].sort());

    const found = (await get(`/projects?q=${encodeURIComponent('École')}`, user.cookie)).json().projects.map((x: { name: string }) => x.name);
    expect(found).toContain('École');

    expect((await get('/projects?cursor=garbage', user.cookie)).statusCode).toBe(400);
    const byTime = (await get('/projects?limit=1', user.cookie)).json().nextCursor as string;
    expect((await get(`/projects?sort=name&cursor=${byTime}`, user.cookie)).statusCode).toBe(400);
    expect((await get('/trash?cursor=garbage', user.cookie)).statusCode).toBe(400);
  });

  it('two saves started from the same version: one is saved, the other is a conflict', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    const put = (name: string) => app.inject({ method: 'PUT', url: `/projects/${p.id}`, ...json({ spec: spec(name), baseVersion: 1 }, user.cookie) });
    const [a, b] = await Promise.all([put('Tab A'), put('Tab B')]);
    expect([a.statusCode, b.statusCode].sort()).toEqual([200, 409]);
    const revisions = (await get(`/projects/${p.id}/revisions`, user.cookie)).json().revisions as { version: number }[];
    expect(revisions.map((r) => r.version)).toEqual([2, 1]);
  });

  it('a HEAD request does not count as a share view', async () => {
    const user = await signIn(PUB_USER, USER);
    const p = await create(user.cookie);
    const { shareId } = (await post(`/projects/${p.id}/share`, {}, user.cookie)).json() as { shareId: string };
    expect((await app.inject({ method: 'HEAD', url: `/share/${shareId}` })).statusCode).toBe(200);
    await app.inject({ method: 'GET', url: `/share/${shareId}` });
    expect((await get('/projects', user.cookie)).json().projects[0].shareViews).toBe(1);
  });

  it('keeps at most 200 projects per owner in the trash, dropping the oldest', async () => {
    for (let i = 0; i < 201; i++) {
      const id = `trash-project-${String(i).padStart(3, '0')}`;
      await store.createProject({ id, owner: USER, name: 't', spec: '{}' });
      await store.deleteProject(id);
    }
    expect(await store.getProject('trash-project-000')).toBeNull();
    expect((await store.getProject('trash-project-200'))?.deletedAt).toBeTruthy();
    let total = 0;
    let cursor: string | undefined;
    do {
      const page = await store.listTrash(USER, { limit: 100, ...(cursor ? { cursor } : {}) });
      total += page.projects.length;
      cursor = page.nextCursor ?? undefined;
    } while (cursor);
    expect(total).toBe(200);
  });

  it('housekeeping purges old trash with its revisions and deployments; recent trash and live projects stay', async () => {
    const t = await makeApp({ HOSTING_ENABLED: '1', ALLOW_TEST_LOGIN: '1' });
    const cookie = cookieOf(await t.app.inject({ method: 'POST', url: '/auth/test-login', ...json({ address: USER }) }));
    const call = (method: 'POST' | 'DELETE', url: string, body?: unknown) =>
      method === 'POST' ? t.app.inject({ method, url, ...json(body ?? {}, cookie) }) : t.app.inject({ method, url, headers: { origin: ORIGIN, cookie } });
    const ws = (await call('POST', '/workspaces', { name: 'Old' })).json().id as string;
    const filed = (await call('POST', '/projects', { spec: spec('Filed'), workspaceId: ws })).json().id as string;
    const live = (await call('POST', '/projects', { spec: spec('Live') })).json().id as string;
    const loose = (await call('POST', '/projects', { spec: spec('Loose') })).json().id as string;
    const deployment = (await call('POST', '/deployments', { projectId: filed, subdomain: 'old-site' })).json().id as string;
    await call('DELETE', `/workspaces/${ws}`);
    await call('DELETE', `/projects/${loose}`);
    const { runHousekeeping } = await import('./housekeeping.js');
    const day = 24 * 3600 * 1000;

    expect((await runHousekeeping(t.store, { auditRetentionDays: 365 }, Date.now() + 29 * day)).trash).toBe(0);
    expect(await t.store.getProject(filed)).not.toBeNull();

    const removed = await runHousekeeping(t.store, { auditRetentionDays: 365 }, Date.now() + 31 * day);
    expect(removed.trash).toBe(3); // two projects and the workspace
    expect(await t.store.getProject(filed)).toBeNull();
    expect(await t.store.getProject(loose)).toBeNull();
    expect(await t.store.listRevisions(filed)).toEqual([]);
    expect(await t.store.getDeployment(deployment)).toBeNull();
    expect(await t.store.getWorkspace(ws, USER, true)).toBeNull();
    expect((await t.store.getProject(live))?.deletedAt).toBeNull();
    await t.close();
  });

  // SQLite file surgery: timestamps set by hand, and the schema rolled back to version 4.
  it.skipIf(isPg)('pages rows that share a timestamp once each', async () => {
    const { mkdtempSync, rmSync } = await import('node:fs');
    const { tmpdir } = await import('node:os');
    const { join } = await import('node:path');
    const { DatabaseSync } = await import('node:sqlite');
    const dir = mkdtempSync(join(tmpdir(), 'pg-api-'));
    try {
      const file = join(dir, 'ties.db');
      const s = await createStore(`sqlite:${file}`);
      for (let i = 0; i < 5; i++) await s.createProject({ id: `tied-project-${i}0000`, owner: USER, name: `T${i}`, spec: '{}' });
      const raw = new DatabaseSync(file);
      raw.exec("UPDATE projects SET updated_at = '2026-01-01T00:00:00.000Z'");
      raw.close();
      const ids: string[] = [];
      let cursor: string | undefined;
      do {
        const page = await s.listProjects(USER, { limit: 1, ...(cursor ? { cursor } : {}) });
        ids.push(...page.projects.map((p) => p.id));
        cursor = page.nextCursor ?? undefined;
      } while (cursor);
      expect(ids).toHaveLength(5);
      expect(new Set(ids).size).toBe(5);
      await s.close();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it.skipIf(isPg)('migration 5 seeds one revision per existing project at its current version', async () => {
    const { mkdtempSync, rmSync } = await import('node:fs');
    const { tmpdir } = await import('node:os');
    const { join } = await import('node:path');
    const { DatabaseSync } = await import('node:sqlite');
    const dir = mkdtempSync(join(tmpdir(), 'pg-api-'));
    try {
      const file = join(dir, 'v4.db');
      const fresh = await createStore(`sqlite:${file}`);
      await fresh.createProject({ id: 'old-project-0001', owner: USER, name: 'One', spec: '{"n":1}' });
      await fresh.createProject({ id: 'old-project-0003', owner: USER, name: 'Three', spec: '{"n":1}' });
      await fresh.updateProject('old-project-0003', { spec: '{"n":2}' });
      await fresh.updateProject('old-project-0003', { spec: '{"n":3}' });
      await fresh.close();
      // Roll the file back to schema 4: no revisions, no trash or share-expiry columns.
      const raw = new DatabaseSync(file);
      raw.exec(`
        DROP TABLE project_revisions;
        DROP INDEX projects_owner_updated; DROP INDEX projects_deleted; DROP INDEX workspaces_deleted;
        ALTER TABLE projects DROP COLUMN deleted_at; ALTER TABLE projects DROP COLUMN share_expires_at;
        ALTER TABLE projects DROP COLUMN share_views; ALTER TABLE workspaces DROP COLUMN deleted_at;
        DELETE FROM schema_migrations WHERE id = 5;`);
      raw.close();

      const upgraded = await createStore(`sqlite:${file}`);
      expect(await upgraded.listRevisions('old-project-0001')).toEqual([{ version: 1, savedAt: expect.any(String) }]);
      expect((await upgraded.listRevisions('old-project-0003')).map((r) => r.version)).toEqual([3]);
      expect(await upgraded.getRevision('old-project-0003', 3)).toBe('{"n":3}');
      const p = await upgraded.getProject('old-project-0003');
      expect(p).toMatchObject({ deletedAt: null, shareViews: 0, shareExpiresAt: null });
      await upgraded.updateProject('old-project-0003', { spec: '{"n":4}' }, 3);
      expect((await upgraded.listRevisions('old-project-0003')).map((r) => r.version)).toEqual([4, 3]);
      await upgraded.close();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
