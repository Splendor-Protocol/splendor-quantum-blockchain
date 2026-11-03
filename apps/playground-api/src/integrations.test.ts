import { randomBytes } from 'node:crypto';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { addressFromPublicKey, createAuthMessage } from '@splendor/sqc-sdk/auth';
import { buildApp } from './app.js';
import { loadConfig } from './config.js';
import { openToken, parseTokenKey, sealToken } from './integrations/tokenBox.js';
import type { FetchLike } from './releases.js';
import { isSafeRepoPath, toPushFiles } from './routes/integrations.js';
import { createStore, type Store } from './store/index.js';

const ORIGIN = 'http://localhost:5180';
const PUB_USER = Buffer.from('user-public-key').toString('base64');
const PUB_OTHER = Buffer.from('other-public-key').toString('base64');
const USER = addressFromPublicKey(PUB_USER)!;
const OTHER = addressFromPublicKey(PUB_OTHER)!;
const KEY = randomBytes(32).toString('base64');
const TOKEN = 'gho_user_token';

/** GitHub's OAuth endpoints and the REST calls a push makes, kept in memory. */
function fakeGitHub() {
  const calls: { method: string; url: string; body?: Record<string, unknown>; auth?: string }[] = [];
  const repos = new Map<string, { head: string; trees: unknown[] }>();
  let n = 0;
  const fetchImpl: FetchLike = async (url, init) => {
    const method = init?.method ?? 'GET';
    const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
    const auth = (init?.headers as Record<string, string> | undefined)?.authorization;
    calls.push({ method, url, body, auth });
    const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'content-type': 'application/json' } });
    const path = new URL(url).pathname;
    if (path === '/login/oauth/access_token') {
      return body?.code === 'good-code' ? json({ access_token: TOKEN, scope: 'public_repo', token_type: 'bearer' }) : json({ error: 'bad_verification_code' });
    }
    if (path.startsWith('/applications/')) return new Response(null, { status: 204 });
    if (auth !== `Bearer ${TOKEN}`) return json({ message: 'Bad credentials' }, 401);
    if (path === '/user') return json({ login: 'octo' });
    if (path === '/user/repos' && method === 'POST') {
      const name = String(body!.name);
      repos.set(name, { head: 'init-sha', trees: [] });
      return json({ name, full_name: `octo/${name}`, html_url: `https://github.com/octo/${name}`, default_branch: 'main', owner: { login: 'octo' } }, 201);
    }
    const m = /^\/repos\/octo\/([^/]+)(\/.*)?$/.exec(path);
    if (!m) return json({ message: 'Not Found' }, 404);
    const repo = repos.get(m[1]!);
    if (!repo) return json({ message: 'Not Found' }, 404);
    const rest = m[2] ?? '';
    if (!rest) return json({ name: m[1], full_name: `octo/${m[1]}`, html_url: `https://github.com/octo/${m[1]}`, default_branch: 'main', owner: { login: 'octo' }, permissions: { push: true } });
    if (rest === '/git/ref/heads/main') return json({ object: { sha: repo.head } });
    if (rest === '/git/blobs') return json({ sha: `blob-${++n}` }, 201);
    if (rest === '/git/trees') {
      repo.trees.push(body!.tree);
      return json({ sha: `tree-${++n}` }, 201);
    }
    if (rest === '/git/commits') return json({ sha: `commit-${++n}` }, 201);
    if (rest === '/git/refs/heads/main' && method === 'PATCH') {
      repo.head = String(body!.sha);
      return json({ object: { sha: repo.head } });
    }
    return json({ message: 'Not Found' }, 404);
  };
  return { calls, repos, fetchImpl };
}

type App = Awaited<ReturnType<typeof buildApp>>;
let store: Store;
let app: App;
let gh: ReturnType<typeof fakeGitHub>;

const ENV = {
  NODE_ENV: 'test',
  PLAYGROUND_ORIGINS: ORIGIN,
  GITHUB_OAUTH_CLIENT_ID: 'client-id',
  GITHUB_OAUTH_CLIENT_SECRET: 'client-secret',
  GITHUB_OAUTH_WEB_URL: 'https://gh.test',
  GITHUB_API_URL: 'https://api.gh.test',
  GITHUB_OAUTH_CALLBACK_URL: `${ORIGIN}/api/integrations/github/callback`,
  INTEGRATION_TOKEN_KEY: KEY,
};

async function start(env: Record<string, string> = ENV) {
  store = await createStore('sqlite::memory:');
  gh = fakeGitHub();
  app = await buildApp({ config: loadConfig(env), store, verifier: async () => true, fetch: gh.fetchImpl });
}

beforeEach(async () => start());
afterEach(async () => {
  await app.close();
  await store.close();
});

const json = (body: unknown, cookie?: string) => ({
  headers: { 'content-type': 'application/json', origin: ORIGIN, ...(cookie ? { cookie } : {}) },
  payload: JSON.stringify(body),
});

async function signIn(pub: string, address: string): Promise<string> {
  const { nonce } = (await app.inject({ method: 'POST', url: '/auth/nonce', ...json({}) })).json() as { nonce: string };
  const message = createAuthMessage({ domain: ORIGIN, nonce, address, statement: 'Sign in', expirationTime: Math.floor(Date.now() / 1000) + 300 });
  const res = await app.inject({ method: 'POST', url: '/auth/verify', ...json({ message, signatureB64: 'x', publicKeyB64: pub, address }) });
  const set = res.headers['set-cookie'];
  return String(Array.isArray(set) ? set[0] : set).split(';')[0]!;
}

/** Start the flow and return the state from the authorize URL plus the state cookie it set. */
async function startFlow(session: string) {
  const res = await app.inject({ method: 'POST', url: '/integrations/github/start', ...json({}, session) });
  expect(res.statusCode).toBe(200);
  const url = new URL((res.json() as { url: string }).url);
  const set = [res.headers['set-cookie']].flat().map(String);
  const stateCookie = set.find((c) => c.startsWith('sqc_pg_gh_state='))!.split(';')[0]!;
  return { url, state: url.searchParams.get('state')!, stateCookie };
}

async function connect(session: string) {
  const { state, stateCookie } = await startFlow(session);
  return app.inject({ method: 'GET', url: `/integrations/github/callback?code=good-code&state=${state}`, headers: { cookie: `${session}; ${stateCookie}` } });
}

describe('token box', () => {
  it('seals per owner and provider; a copied value does not open for someone else', () => {
    const key = parseTokenKey(KEY)!;
    const sealed = sealToken(key, 'secret', USER, 'github');
    expect(sealed).not.toContain('secret');
    expect(openToken(key, sealed, USER, 'github')).toBe('secret');
    expect(openToken(key, sealed, OTHER, 'github')).toBeNull();
    expect(openToken(parseTokenKey(randomBytes(32).toString('hex'))!, sealed, USER, 'github')).toBeNull();
    expect(openToken(key, 'garbage', USER, 'github')).toBeNull();
    expect(parseTokenKey('short')).toBeNull();
  });
});

describe('config', () => {
  it('needs both OAuth values, and a token key in production', () => {
    expect(() => loadConfig({ ...ENV, GITHUB_OAUTH_CLIENT_SECRET: '' })).toThrow(/both/);
    expect(() => loadConfig({ ...ENV, INTEGRATION_TOKEN_KEY: 'nope' })).toThrow(/32 random bytes/);
    expect(() => loadConfig({ ...ENV, NODE_ENV: 'production', INTEGRATION_TOKEN_KEY: '' })).toThrow(/INTEGRATION_TOKEN_KEY/);
    const dev = loadConfig({ ...ENV, INTEGRATION_TOKEN_KEY: '' });
    expect(dev.githubOAuth).toBeNull();
    expect(dev.warnings.join(' ')).toMatch(/Push to GitHub stays off/);
  });
});

describe('push files', () => {
  it('refuses traversal, absolute paths and .git, and turns the base64 signer into a binary file', () => {
    for (const bad of ['../x', '/etc/passwd', 'a/../b', '.git/config', 'src/.GIT/hooks', 'C:/x', 'a\\b', '']) expect(isSafeRepoPath(bad)).toBe(false);
    expect(isSafeRepoPath('.github/workflows/ci.yml')).toBe(true);
    const out = toPushFiles({ 'README.md': '# hi', 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64': 'AGFzbQ==' });
    expect(out).toEqual({
      files: [
        { path: 'README.md', content: '# hi', encoding: 'utf-8' },
        { path: 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm', content: 'AGFzbQ==', encoding: 'base64' },
      ],
    });
    expect(toPushFiles({ 'x.wasm.b64': 'not base64!' })).toEqual({ error: expect.stringContaining('base64') });
  });
});

describe('GitHub integration', () => {
  it('is off (404) without OAuth settings, and status says so', async () => {
    await app.close();
    await store.close();
    await start({ NODE_ENV: 'test', PLAYGROUND_ORIGINS: ORIGIN });
    const session = await signIn(PUB_USER, USER);
    expect((await app.inject({ method: 'GET', url: '/integrations/github/status', headers: { cookie: session } })).json()).toEqual({ enabled: false, connected: false });
    const res = await app.inject({ method: 'POST', url: '/integrations/github/start', ...json({}, session) });
    expect(res.statusCode).toBe(404);
  });

  it('needs a session', async () => {
    expect((await app.inject({ method: 'GET', url: '/integrations/github/status' })).statusCode).toBe(401);
    expect((await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'x', files: { a: 'b' } }) })).statusCode).toBe(401);
  });

  it('connects through the OAuth web flow and stores the token sealed', async () => {
    const session = await signIn(PUB_USER, USER);
    const { url } = await startFlow(session);
    expect(url.origin + url.pathname).toBe('https://gh.test/login/oauth/authorize');
    expect(url.searchParams.get('client_id')).toBe('client-id');
    expect(url.searchParams.get('scope')).toBe('public_repo');
    expect(url.searchParams.get('redirect_uri')).toBe(`${ORIGIN}/api/integrations/github/callback`);

    const res = await connect(session);
    expect(res.statusCode).toBe(302);
    expect(res.headers.location).toBe(`${ORIGIN}/github-connected.html?status=ok`);
    const row = await store.getIntegration(USER, 'github');
    expect(row?.account).toBe('octo');
    expect(row?.token).not.toContain(TOKEN);
    const status = await app.inject({ method: 'GET', url: '/integrations/github/status', headers: { cookie: session } });
    expect(status.json()).toEqual({ enabled: true, connected: true, account: 'octo', scope: 'public_repo' });
    const audit = await store.listAudit(10);
    expect(audit.map((a) => a.action)).toContain('integration.github.connect');
    expect(JSON.stringify(audit)).not.toContain(TOKEN);
  });

  it('refuses a callback without the matching state cookie, a replayed state, and another user', async () => {
    const session = await signIn(PUB_USER, USER);
    const { state, stateCookie } = await startFlow(session);
    const noCookie = await app.inject({ method: 'GET', url: `/integrations/github/callback?code=good-code&state=${state}`, headers: { cookie: session } });
    expect(noCookie.headers.location).toBe(`${ORIGIN}/github-connected.html?status=error`);

    const other = await signIn(PUB_OTHER, OTHER);
    const asOther = await app.inject({ method: 'GET', url: `/integrations/github/callback?code=good-code&state=${state}`, headers: { cookie: `${other}; ${stateCookie}` } });
    expect(asOther.headers.location).toMatch(/status=error/);
    expect(await store.getIntegration(OTHER, 'github')).toBeNull();
    expect(await store.getIntegration(USER, 'github')).toBeNull();

    // The state was used up by the attempt above.
    const replay = await app.inject({ method: 'GET', url: `/integrations/github/callback?code=good-code&state=${state}`, headers: { cookie: `${session}; ${stateCookie}` } });
    expect(replay.headers.location).toMatch(/status=error/);
  });

  it('reports a denied grant and a bad code without storing anything', async () => {
    const session = await signIn(PUB_USER, USER);
    let flow = await startFlow(session);
    const denied = await app.inject({ method: 'GET', url: `/integrations/github/callback?error=access_denied&state=${flow.state}`, headers: { cookie: `${session}; ${flow.stateCookie}` } });
    expect(denied.headers.location).toMatch(/status=denied/);
    flow = await startFlow(session);
    const bad = await app.inject({ method: 'GET', url: `/integrations/github/callback?code=bad&state=${flow.state}`, headers: { cookie: `${session}; ${flow.stateCookie}` } });
    expect(bad.headers.location).toMatch(/status=error/);
    expect(await store.getIntegration(USER, 'github')).toBeNull();
  });

  it('pushes an export: creates the repo, commits every file in one tree, and reuses the repo next time', async () => {
    const session = await signIn(PUB_USER, USER);
    await connect(session);
    const files = { 'README.md': '# My dApp', 'src/App.tsx': 'export {}', 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64': 'AGFzbQ==' };
    const res = await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'my-dapp', private: false, description: 'Demo', files }, session) });
    expect(res.statusCode).toBe(200);
    expect(res.json()).toMatchObject({ repoUrl: 'https://github.com/octo/my-dapp', fullName: 'octo/my-dapp', branch: 'main', created: true });

    const create = gh.calls.find((c) => c.url.endsWith('/user/repos'))!;
    expect(create.body).toMatchObject({ name: 'my-dapp', private: false, auto_init: true });
    expect(gh.calls.filter((c) => c.url.endsWith('/git/blobs')).map((c) => c.body)).toEqual([{ content: 'AGFzbQ==', encoding: 'base64' }]);
    const tree = gh.repos.get('my-dapp')!.trees[0] as { path: string; content?: string; sha?: string }[];
    expect(tree.map((e) => e.path).sort()).toEqual(['README.md', 'src/App.tsx', 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm']);
    expect(tree.find((e) => e.path === 'README.md')?.content).toBe('# My dApp');
    const commit = gh.calls.find((c) => c.url.endsWith('/git/commits'))!;
    expect(commit.body).toMatchObject({ parents: ['init-sha'] });
    expect(gh.calls.every((c) => !c.auth || c.auth === `Bearer ${TOKEN}` || c.auth.startsWith('Basic'))).toBe(true);

    const again = await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'my-dapp', files }, session) });
    expect(again.json()).toMatchObject({ created: false });
    expect(gh.calls.filter((c) => c.url.endsWith('/user/repos'))).toHaveLength(1);
    expect((await store.listAudit(20)).filter((a) => a.action === 'integration.github.push')).toHaveLength(2);
  });

  it('validates the push body and asks to reconnect when GitHub drops the token', async () => {
    const session = await signIn(PUB_USER, USER);
    const notConnected = await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'x', files: { a: 'b' } }, session) });
    expect(notConnected.json()).toMatchObject({ code: 'GITHUB_RECONNECT' });
    await connect(session);
    for (const repo of ['..', 'bad name', 'x.git']) {
      expect((await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo, files: { a: 'b' } }, session) })).statusCode).toBe(400);
    }
    expect((await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'ok', files: { '../evil': 'x' } }, session) })).statusCode).toBe(400);

    // Revoked on GitHub's side: the stored token is dropped and the user is told to reconnect.
    const row = (await store.getIntegration(USER, 'github'))!;
    await store.putIntegration({ ...row, token: sealToken(parseTokenKey(KEY)!, 'revoked', USER, 'github') });
    const res = await app.inject({ method: 'POST', url: '/integrations/github/push', ...json({ repo: 'ok', files: { a: 'b' } }, session) });
    expect(res.statusCode).toBe(401);
    expect(res.json()).toMatchObject({ code: 'GITHUB_RECONNECT' });
    expect(await store.getIntegration(USER, 'github')).toBeNull();
  });

  it('disconnect removes the token and revokes the grant', async () => {
    const session = await signIn(PUB_USER, USER);
    await connect(session);
    const res = await app.inject({ method: 'POST', url: '/integrations/github/disconnect', ...json({}, session) });
    expect(res.json()).toEqual({ ok: true });
    expect(await store.getIntegration(USER, 'github')).toBeNull();
    const revoke = gh.calls.find((c) => c.url.includes('/applications/client-id/grant'))!;
    expect(revoke.method).toBe('DELETE');
    expect(revoke.body).toEqual({ access_token: TOKEN });
  });
});
