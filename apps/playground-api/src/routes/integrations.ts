import { randomBytes } from 'node:crypto';
import type { FastifyReply } from 'fastify';
import { exchangeCode, GitHubError, pushToGitHub, revokeToken, type PushFile } from '../integrations/github.js';
import { openToken, sealToken } from '../integrations/tokenBox.js';
import type { FetchLike } from '../releases.js';
import { sha256 } from './auth.js';
import { obj, str, type RouteContext } from './context.js';

const STATE_TTL_SEC = 600;
/** Upper bounds for one push: a vendored export is ~7 MB and ~320 files. */
export const MAX_PUSH_FILES = 2000;
export const MAX_PUSH_BYTES = 20 * 1024 * 1024;
const REPO_NAME = '^[A-Za-z0-9._-]{1,100}$';
const BASE64_RE = /^[A-Za-z0-9+/]*={0,2}$/;

/** Relative path inside the repo: no traversal, absolute paths, drive letters, backslashes or `.git`. */
export function isSafeRepoPath(path: string): boolean {
  if (!path || path.length > 300 || path.startsWith('/') || /[\\:\0]/.test(path)) return false;
  return path.split('/').every((seg) => seg !== '' && seg !== '.' && seg !== '..' && seg.toLowerCase() !== '.git');
}

/**
 * Export files as they go into the repo. `*.wasm.b64` (how exports carry the signer) becomes the
 * binary `.wasm`, like the zip download.
 */
export function toPushFiles(files: Record<string, string>): { files: PushFile[] } | { error: string } {
  const out: PushFile[] = [];
  let bytes = 0;
  for (const [path, content] of Object.entries(files)) {
    if (!isSafeRepoPath(path)) return { error: `Unsafe file path: ${path.slice(0, 80)}` };
    bytes += path.length + content.length;
    if (path.endsWith('.wasm.b64')) {
      if (!BASE64_RE.test(content)) return { error: `${path} is not base64` };
      out.push({ path: path.slice(0, -'.b64'.length), content, encoding: 'base64' });
    } else out.push({ path, content, encoding: 'utf-8' });
  }
  if (bytes > MAX_PUSH_BYTES) return { error: `The project is larger than ${MAX_PUSH_BYTES / 1024 / 1024} MB` };
  if (!out.length) return { error: 'No files to push' };
  return { files: out };
}

/**
 * "Push to GitHub" with the user's own GitHub account (OAuth web flow). The user's token is sealed
 * in the database and only ever used for that user's pushes; the server's release token is not.
 */
export function registerIntegrationRoutes(ctx: RouteContext, fetchImpl: FetchLike) {
  const { app, config, store, requireUser } = ctx;
  const oauth = config.githubOAuth;
  const key = config.integrationKey;
  const enabled = !!oauth && !!key;
  const apiUrl = config.github.apiUrl;
  const stateCookie = config.cookieSecure ? '__Host-sqc_pg_gh_state' : 'sqc_pg_gh_state';
  const stateCookieOptions = { httpOnly: true, secure: config.cookieSecure, sameSite: 'lax' as const, path: '/' };
  const limited = (max: number) => ({ rateLimit: { max, timeWindow: '1 minute' } });
  const off = (reply: FastifyReply) => reply.code(404).send({ error: 'Push to GitHub is not configured on this server', code: 'GITHUB_DISABLED' });

  /** Back to the studio's small "connected" page, which tells the opener and closes itself. */
  const finish = (reply: FastifyReply, origin: string, status: 'ok' | 'denied' | 'error') => {
    reply.clearCookie(stateCookie, stateCookieOptions);
    return reply.code(302).header('location', `${origin}/github-connected.html?status=${status}`).send();
  };

  app.get('/integrations/github/status', { preHandler: requireUser }, async (req) => {
    if (!enabled) return { enabled: false, connected: false };
    const row = await store.getIntegration(req.session!.address, 'github');
    return { enabled: true, connected: !!row, account: row?.account ?? null, scope: row?.scope ?? null };
  });

  app.post('/integrations/github/start', { preHandler: requireUser, config: limited(10), schema: { body: obj() } }, async (req, reply) => {
    if (!enabled) return off(reply);
    // The CSRF hook has already refused an Origin that is not allowed.
    const origin = req.headers.origin;
    const returnOrigin = origin && config.origins.includes(origin) ? origin : config.origins[0]!;
    const state = randomBytes(32).toString('base64url');
    await store.putOAuthState(sha256(state), { owner: req.session!.address, provider: 'github', returnOrigin }, Math.floor(Date.now() / 1000) + STATE_TTL_SEC);
    // Binds the round trip to this browser: a state link sent to someone else does not work for them.
    reply.setCookie(stateCookie, state, { ...stateCookieOptions, maxAge: STATE_TTL_SEC });
    const q = new URLSearchParams({ client_id: oauth!.clientId, scope: oauth!.scope, state, allow_signup: 'true' });
    if (oauth!.callbackUrl) q.set('redirect_uri', oauth!.callbackUrl);
    return { url: `${oauth!.webUrl}/login/oauth/authorize?${q}` };
  });

  app.get<{ Querystring: { code?: string; state?: string; error?: string } }>(
    '/integrations/github/callback',
    {
      config: limited(20),
      schema: {
        querystring: {
          type: 'object',
          properties: { code: str(200), state: str(200), error: str(100) },
        },
      },
    },
    async (req, reply) => {
      const fallback = config.origins[0]!;
      if (!enabled) return finish(reply, fallback, 'error');
      const { code, state, error } = req.query;
      const cookieState = req.cookies[stateCookie];
      if (!state || !cookieState || cookieState !== state) return finish(reply, fallback, 'error');
      const pending = await store.consumeOAuthState(sha256(state), Math.floor(Date.now() / 1000));
      if (!pending) return finish(reply, fallback, 'error');
      // The session cookie may be missing here (SameSite=strict, or another site); the state row says who started.
      if (req.session && req.session.address !== pending.owner) return finish(reply, pending.returnOrigin, 'error');
      if (error || !code) return finish(reply, pending.returnOrigin, 'denied');
      try {
        const got = await exchangeCode(fetchImpl, oauth!, apiUrl, code);
        await store.putIntegration({ owner: pending.owner, provider: 'github', account: got.login, token: sealToken(key!, got.token, pending.owner, 'github'), scope: got.scope });
        await ctx.audit(req, pending.owner, 'integration.github.connect', got.login, { scope: got.scope });
        return finish(reply, pending.returnOrigin, 'ok');
      } catch (err) {
        req.log.warn({ err: err instanceof Error ? err.message : String(err) }, 'github oauth callback failed');
        return finish(reply, pending.returnOrigin, 'error');
      }
    },
  );

  app.post('/integrations/github/disconnect', { preHandler: requireUser, config: limited(10), schema: { body: obj() } }, async (req, reply) => {
    if (!enabled) return off(reply);
    const owner = req.session!.address;
    const row = await store.getIntegration(owner, 'github');
    if (!row) return { ok: true };
    await store.deleteIntegration(owner, 'github');
    const token = openToken(key!, row.token, owner, 'github');
    if (token) {
      try {
        await revokeToken(fetchImpl, oauth!, apiUrl, token);
      } catch (err) {
        req.log.warn({ err: err instanceof Error ? err.message : String(err) }, 'github revoke failed');
      }
    }
    await ctx.audit(req, owner, 'integration.github.disconnect', row.account);
    return { ok: true };
  });

  app.post<{ Body: { repo: string; private?: boolean; description?: string; message?: string; files: Record<string, string> } }>(
    '/integrations/github/push',
    {
      preHandler: requireUser,
      config: limited(10),
      bodyLimit: MAX_PUSH_BYTES + 512 * 1024,
      schema: {
        body: obj(
          {
            repo: str(100, REPO_NAME),
            private: { type: 'boolean' },
            description: str(350),
            message: str(200),
            files: { type: 'object', maxProperties: MAX_PUSH_FILES, additionalProperties: { type: 'string' } },
          },
          ['repo', 'files'],
        ),
      },
    },
    async (req, reply) => {
      if (!enabled) return off(reply);
      const owner = req.session!.address;
      const { repo, files } = req.body;
      if (repo === '.' || repo === '..' || /\.git$/i.test(repo)) return reply.code(400).send({ error: 'Pick another repository name' });
      const prepared = toPushFiles(files);
      if ('error' in prepared) return reply.code(400).send({ error: prepared.error });
      const row = await store.getIntegration(owner, 'github');
      const token = row ? openToken(key!, row.token, owner, 'github') : null;
      if (!row || !token) {
        if (row) await store.deleteIntegration(owner, 'github');
        return reply.code(401).send({ error: 'Connect your GitHub account first', code: 'GITHUB_RECONNECT' });
      }
      try {
        const result = await pushToGitHub(fetchImpl, apiUrl, token, {
          login: row.account,
          repo,
          private: req.body.private === true,
          description: (req.body.description ?? '').replace(/[\r\n]+/g, ' ').trim(),
          message: req.body.message?.trim() || 'Update from Splendor dApp Studio',
          files: prepared.files,
        });
        await ctx.audit(req, owner, 'integration.github.push', result.fullName, { files: prepared.files.length, created: result.created, commit: result.commitSha });
        return result;
      } catch (err) {
        if (err instanceof GitHubError) {
          if (err.status === 401) {
            await store.deleteIntegration(owner, 'github');
            return reply.code(401).send({ error: 'GitHub no longer accepts the connection. Connect again.', code: 'GITHUB_RECONNECT' });
          }
          if (err.status === 403 || err.status === 404 || err.status === 409 || err.status === 422) {
            return reply.code(err.status === 404 ? 403 : err.status).send({ error: err.message, code: 'GITHUB_REFUSED' });
          }
          req.log.warn({ err: err.message }, 'github push failed');
          return reply.code(502).send({ error: err.message, code: 'GITHUB_UNAVAILABLE' });
        }
        throw err;
      }
    },
  );
}
