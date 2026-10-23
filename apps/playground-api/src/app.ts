import { createHash, randomBytes, randomUUID } from 'node:crypto';
import type { IncomingMessage } from 'node:http';
import Fastify, { type FastifyReply, type FastifyRequest } from 'fastify';
import cookie, { type CookieSerializeOptions } from '@fastify/cookie';
import cors from '@fastify/cors';
import rateLimit from '@fastify/rate-limit';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, migrateSpec, validateSpec, type DappSpec } from '@splendor/dapp-spec';
import { issueNonce, parseAuthMessage, verifySignIn, type NonceStore } from '@splendor/sqc-sdk/auth';
import { canonicalAddress, type ApiConfig } from './config.js';
import { startHousekeeping } from './housekeeping.js';
import { createReleaseService, type FetchLike, type ReleaseService } from './releases.js';
import type { CatalogFlag, ProjectPatch, Store } from './store/types.js';

export type AppDeps = {
  config: ApiConfig;
  store: Store;
  /** Signature verifier override (tests). Defaults to the SDK's WASM ML-DSA verifier. */
  verifier?: (publicKeyB64: string, message: string, signatureB64: string) => Promise<boolean>;
  fetch?: FetchLike;
  releases?: ReleaseService;
  logger?: boolean;
  /** Run expired-row cleanup on this interval (the server passes one; tests call runHousekeeping). */
  housekeepingIntervalMs?: number;
};

const ADDRESS = '^SQ:[0-9a-fA-F]{64}$';
const ID_RE = /^[A-Za-z0-9_-]{10,40}$/;
const ID = '^[A-Za-z0-9_-]{10,40}$';
const FLAG_ID = '^(template:|recipe:)?[a-z0-9][a-z0-9-]{0,63}$';
const REQUEST_ID_RE = /^[A-Za-z0-9_.:-]{1,64}$/;
const MAX_PROJECTS_PER_USER = 100;
const NONCE_TTL_SEC = 300;
/** Failed sign-ins are audited, but an attacker must not be able to grow the table without bound. */
const MAX_FAILED_AUTH_AUDITS_PER_MINUTE = 60;

const sha256 = (s: string) => createHash('sha256').update(s).digest('hex');
const newId = (bytes = 12) => randomBytes(bytes).toString('base64url');

type Session = { address: string; isAdmin: boolean };

declare module 'fastify' {
  interface FastifyRequest {
    session: Session | null;
  }
}

// ---- request schemas: shape and size only; spec content goes through validateSpec ----
const obj = (properties: Record<string, object> = {}, required: string[] = []) => ({ type: 'object', additionalProperties: false, properties, required });
const str = (maxLength: number, pattern?: string) => ({ type: 'string', maxLength, ...(pattern ? { pattern } : {}) });
const idParams = obj({ id: str(40, ID) }, ['id']);
/** `baseVersion`: the version the edit started from; a stale one is refused with 409 instead of overwriting. */
const specBody = obj({ spec: { type: 'object' }, baseVersion: { type: 'integer', minimum: 1 } }, ['spec']);
const createProjectBody = obj({ spec: { type: 'object' }, workspaceId: str(40, ID) }, ['spec']);
const workspaceNameBody = obj({ name: str(200) });
const patchProjectBody = obj({ name: str(200), workspaceId: { anyOf: [str(40, ID), { type: 'null' }] }, baseVersion: { type: 'integer', minimum: 1 } });
const listProjectsQuery = obj({ workspaceId: str(40, ID) });

export async function buildApp(deps: AppDeps) {
  const { config, store } = deps;
  const catalog = createCatalog(WIDGET_MANIFESTS);
  const releases = deps.releases ?? createReleaseService(config.github, deps.fetch);
  const app = Fastify({
    logger: deps.logger ?? false,
    bodyLimit: 512 * 1024,
    trustProxy: config.trustProxy,
    genReqId: (req: IncomingMessage) => {
      const given = req.headers['x-request-id'];
      return typeof given === 'string' && REQUEST_ID_RE.test(given) ? given : randomUUID();
    },
    // Reject unknown fields instead of silently dropping them, and never coerce body types.
    ajv: { customOptions: { removeAdditional: false, coerceTypes: false, useDefaults: false } },
  });

  await app.register(cookie);
  await app.register(cors, { origin: config.origins, credentials: true, methods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] });
  await app.register(rateLimit, { max: 300, timeWindow: '1 minute' });

  // CSRF: state changes need JSON (a plain HTML form cannot send it) and an allowed Origin. A
  // request that carries the session cookie but no Origin is only accepted when the browser says
  // it is same-origin; cookie-less clients (curl, server-to-server) are unaffected.
  app.addHook('onRequest', async (req, reply) => {
    if (req.method === 'GET' || req.method === 'HEAD' || req.method === 'OPTIONS') return;
    const origin = req.headers.origin;
    if (origin) {
      if (!config.origins.includes(origin)) return reply.code(403).send({ error: 'Origin not allowed' });
    } else if (req.cookies[config.cookieName] && req.headers['sec-fetch-site'] !== 'same-origin') {
      return reply.code(403).send({ error: 'Origin header required' });
    }
    if (req.method !== 'DELETE' && !String(req.headers['content-type'] ?? '').startsWith('application/json')) {
      return reply.code(415).send({ error: 'Expected application/json' });
    }
  });

  app.addHook('onSend', async (req, reply, payload) => {
    reply.header('x-request-id', req.id);
    reply.header('x-content-type-options', 'nosniff');
    reply.header('x-frame-options', 'DENY');
    reply.header('referrer-policy', 'no-referrer');
    // Responses are per-user unless a route says otherwise.
    if (!reply.hasHeader('cache-control')) reply.header('cache-control', 'no-store');
    return payload;
  });

  app.setErrorHandler((err: Error & { statusCode?: number; validation?: { instancePath?: string; message?: string }[] }, req, reply) => {
    const status = err.statusCode && err.statusCode >= 400 && err.statusCode < 600 ? err.statusCode : 500;
    if (status >= 500) {
      req.log.error({ err }, 'request failed');
      return reply.code(status).send({ error: 'Internal error', requestId: req.id });
    }
    if (err.validation) {
      return reply.code(400).send({
        error: 'Invalid request',
        code: 'INVALID_REQUEST',
        requestId: req.id,
        issues: err.validation.map((v) => ({ path: v.instancePath ?? '', message: v.message ?? 'invalid' })),
      });
    }
    return reply.code(status).send({ error: err.message, requestId: req.id });
  });

  app.setNotFoundHandler((req, reply) => reply.code(404).send({ error: 'Not found', code: 'NOT_FOUND', requestId: req.id }));

  const isAdmin = async (address: string) => config.adminAddresses.includes(address) || (await store.isAdmin(address));

  app.decorateRequest('session', null);
  app.addHook('preHandler', async (req) => {
    const raw = req.cookies[config.cookieName];
    if (!raw) return;
    const s = await store.getSession(sha256(raw), Math.floor(Date.now() / 1000));
    const address = s ? canonicalAddress(s.address) : null;
    if (address) req.session = { address, isAdmin: await isAdmin(address) };
  });

  const requireUser = async (req: FastifyRequest, reply: FastifyReply) => {
    if (!req.session) return reply.code(401).send({ error: 'Sign in first' });
  };
  const requireAdmin = async (req: FastifyRequest, reply: FastifyReply) => {
    if (!req.session) return reply.code(401).send({ error: 'Sign in first' });
    if (!req.session.isAdmin) return reply.code(403).send({ error: 'Admins only' });
  };

  const nonceStore: NonceStore = {
    put: (n, exp) => store.putNonce(n, exp),
    consume: (n, nowSec) => store.consumeNonce(n, nowSec),
  };

  const cookieOptions = (): CookieSerializeOptions => ({
    httpOnly: true,
    secure: config.cookieSecure,
    sameSite: config.cookieSameSite,
    path: '/',
  });

  async function startSession(req: FastifyRequest, reply: FastifyReply, address: string): Promise<Session> {
    // Signing in again replaces the session this browser had, rather than leaving it valid.
    const previous = req.cookies[config.cookieName];
    if (previous) await store.deleteSession(sha256(previous));
    const id = randomBytes(32).toString('base64url');
    await store.createSession(sha256(id), address, Math.floor(Date.now() / 1000) + config.sessionTtlSec);
    reply.setCookie(config.cookieName, id, { ...cookieOptions(), maxAge: config.sessionTtlSec });
    return { address, isAdmin: await isAdmin(address) };
  }

  function checkSpec(input: unknown): { ok: true; spec: DappSpec } | { ok: false; issues: { path: string; message: string }[] } {
    try {
      return validateSpec(migrateSpec(input), { catalog });
    } catch (e) {
      return { ok: false, issues: [{ path: '', message: e instanceof Error ? e.message : String(e) }] };
    }
  }

  // ---- health ----
  app.get('/health', async () => ({ ok: true, hosting: config.hostingEnabled }));
  app.get('/health/ready', async (req, reply) => {
    try {
      await store.ping();
      return { ok: true };
    } catch (err) {
      req.log.error({ err }, 'readiness: database unreachable');
      return reply.code(503).send({ ok: false, error: 'Database unreachable' });
    }
  });

  // ---- auth ----
  const authLimit = { rateLimit: { max: 30, timeWindow: '1 minute' } };

  app.post('/auth/nonce', { config: authLimit, schema: { body: obj() } }, async () => {
    const nonce = issueNonce();
    await nonceStore.put(nonce, Math.floor(Date.now() / 1000) + NONCE_TTL_SEC);
    return { nonce };
  });

  app.post<{ Body: { message: string; signatureB64: string; publicKeyB64: string; address: string } }>(
    '/auth/verify',
    {
      config: authLimit,
      schema: {
        body: obj(
          { message: str(4000), signatureB64: str(10_000), publicKeyB64: str(5000), address: str(80) },
          ['message', 'signatureB64', 'publicKeyB64', 'address'],
        ),
      },
    },
    async (req, reply) => {
      const { message, signatureB64, publicKeyB64, address } = req.body;
      const domain = parseAuthMessage(message)?.domain;
      if (!domain || !config.origins.includes(domain)) return reply.code(401).send({ error: 'Sign-in message is for another site' });
      const result = await verifySignIn({
        message,
        signatureB64,
        publicKeyB64,
        address,
        domain,
        nonceStore,
        chainId: config.chainId,
        ...(deps.verifier ? { verifier: deps.verifier } : {}),
      });
      if (!result.ok) {
        const since = new Date(Date.now() - 60_000).toISOString();
        if ((await store.countAuditSince('auth.failed', since)) < MAX_FAILED_AUTH_AUDITS_PER_MINUTE) {
          await store.audit(canonicalAddress(address) ?? 'invalid-address', 'auth.failed', null, { reason: result.reason, ip: req.ip });
        }
        return reply.code(401).send({ error: `Sign-in failed (${result.reason})` });
      }
      const signedIn = canonicalAddress(result.address);
      if (!signedIn) return reply.code(401).send({ error: 'Sign-in failed (bad-address)' });
      const session = await startSession(req, reply, signedIn);
      await store.audit(signedIn, 'auth.signin', null, { admin: session.isAdmin });
      return session;
    },
  );

  if (config.allowTestLogin) {
    app.post<{ Body: { address: string } }>(
      '/auth/test-login',
      { schema: { body: obj({ address: str(80, ADDRESS) }, ['address']) } },
      async (req, reply) => startSession(req, reply, canonicalAddress(req.body.address)!),
    );
  }

  app.get('/auth/me', async (req, reply) => {
    if (!req.session) return reply.code(401).send({ error: 'Not signed in' });
    return req.session;
  });

  app.post('/auth/logout', { schema: { body: obj() } }, async (req, reply) => {
    const raw = req.cookies[config.cookieName];
    if (raw) await store.deleteSession(sha256(raw));
    reply.clearCookie(config.cookieName, cookieOptions());
    return { ok: true };
  });

  const summary = (p: { id: string; name: string; updatedAt: string; shareId: string | null; workspaceId?: string | null; version: number }) => ({
    id: p.id,
    name: p.name,
    updatedAt: p.updatedAt,
    shareId: p.shareId,
    workspaceId: p.workspaceId ?? null,
    version: p.version,
  });
  const conflict = async (reply: FastifyReply, id: string) => {
    const current = await store.getProject(id);
    if (!current) return reply.code(404).send({ error: 'Not found' });
    return reply.code(409).send({
      error: 'This project was changed somewhere else since you opened it',
      code: 'CONFLICT',
      current: { ...summary(current), createdAt: current.createdAt },
    });
  };
  const ownsWorkspace = async (owner: string, id: string) => (await store.getWorkspace(id, owner)) !== null;

  // ---- workspaces ----
  app.get('/workspaces', { preHandler: requireUser }, async (req) => ({
    workspaces: await store.listWorkspaces(req.session!.address),
  }));

  app.post<{ Body: { name?: string } }>('/workspaces', { preHandler: requireUser, schema: { body: workspaceNameBody } }, async (req, reply) => {
    const name = (typeof req.body.name === 'string' && req.body.name.trim() ? req.body.name.trim() : 'My workspace').slice(0, 80);
    const row = await store.createWorkspace({ id: newId(), owner: req.session!.address, name });
    return reply.code(201).send(row);
  });

  app.patch<{ Params: { id: string }; Body: { name: string } }>(
    '/workspaces/:id',
    { preHandler: requireUser, schema: { params: idParams, body: workspaceNameBody } },
    async (req, reply) => {
      if (!(await ownsWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
      const name = String(req.body.name ?? '').trim().slice(0, 80);
      if (!name) return reply.code(400).send({ error: 'name required' });
      const row = await store.renameWorkspace(req.params.id, name);
      if (!row) return reply.code(404).send({ error: 'Not found' });
      return row;
    },
  );

  app.delete<{ Params: { id: string } }>('/workspaces/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!(await ownsWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
    await store.deleteWorkspace(req.params.id);
    return { ok: true };
  });

  app.get<{ Params: { id: string } }>('/workspaces/:id/projects', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!(await ownsWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
    return {
      projects: (await store.listProjects(req.session!.address, req.params.id)).map(summary),
    };
  });

  // ---- projects ----
  app.get<{ Querystring: { workspaceId?: string } }>('/projects', { preHandler: requireUser, schema: { querystring: listProjectsQuery } }, async (req) => ({
    projects: (await store.listProjects(req.session!.address, req.query.workspaceId)).map(summary),
  }));

  app.post<{ Body: { spec: unknown; workspaceId?: string } }>('/projects', { preHandler: requireUser, schema: { body: createProjectBody } }, async (req, reply) => {
    const r = checkSpec(req.body.spec);
    if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
    if ((await store.countProjects(req.session!.address)) >= MAX_PROJECTS_PER_USER) {
      return reply.code(429).send({ error: `Project limit reached (${MAX_PROJECTS_PER_USER})` });
    }
    const workspaceId = req.body.workspaceId ?? null;
    if (workspaceId && !(await ownsWorkspace(req.session!.address, workspaceId))) return reply.code(400).send({ error: 'Unknown workspace' });
    const row = await store.createProject({
      id: newId(),
      owner: req.session!.address,
      name: r.spec.name,
      spec: JSON.stringify(r.spec),
      workspaceId,
    });
    return reply.code(201).send({ ...summary(row), createdAt: row.createdAt, spec: r.spec });
  });

  const ownedProject = async (req: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const p = ID_RE.test(req.params.id) ? await store.getProject(req.params.id) : null;
    if (!p || p.owner !== req.session!.address) {
      reply.code(404).send({ error: 'Not found' });
      return null;
    }
    return p;
  };

  app.get<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    return { ...summary(p), createdAt: p.createdAt, spec: JSON.parse(p.spec) as DappSpec };
  });

  app.put<{ Params: { id: string }; Body: { spec: unknown; baseVersion?: number } }>(
    '/projects/:id',
    { preHandler: requireUser, schema: { params: idParams, body: specBody } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      const r = checkSpec(req.body.spec);
      if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
      const row = await store.updateProject(p.id, { name: r.spec.name, spec: JSON.stringify(r.spec) }, req.body.baseVersion);
      if (!row) return conflict(reply, p.id);
      return { ...summary(row), createdAt: row.createdAt, spec: r.spec };
    },
  );

  // Rename or move without resending the spec. The name is kept in the stored spec too.
  app.patch<{ Params: { id: string }; Body: { name?: string; workspaceId?: string | null; baseVersion?: number } }>(
    '/projects/:id',
    { preHandler: requireUser, schema: { params: idParams, body: patchProjectBody } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      const patch: ProjectPatch = {};
      if (req.body.name !== undefined) {
        const name = req.body.name.trim().slice(0, 80);
        if (!name) return reply.code(400).send({ error: 'name required' });
        const spec = { ...(JSON.parse(p.spec) as DappSpec), name };
        patch.name = name;
        patch.spec = JSON.stringify(spec);
      }
      if (req.body.workspaceId !== undefined) {
        if (req.body.workspaceId !== null && !(await ownsWorkspace(req.session!.address, req.body.workspaceId))) {
          return reply.code(400).send({ error: 'Unknown workspace' });
        }
        patch.workspaceId = req.body.workspaceId;
      }
      if (!Object.keys(patch).length) return reply.code(400).send({ error: 'Nothing to change' });
      const row = await store.updateProject(p.id, patch, req.body.baseVersion);
      if (!row) return conflict(reply, p.id);
      return { ...summary(row), createdAt: row.createdAt };
    },
  );

  app.post<{ Params: { id: string } }>('/projects/:id/duplicate', { preHandler: requireUser, schema: { params: idParams, body: obj() } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    if ((await store.countProjects(req.session!.address)) >= MAX_PROJECTS_PER_USER) {
      return reply.code(429).send({ error: `Project limit reached (${MAX_PROJECTS_PER_USER})` });
    }
    const name = `${p.name} copy`.slice(0, 80);
    const spec = { ...(JSON.parse(p.spec) as DappSpec), name };
    const row = await store.createProject({ id: newId(), owner: p.owner, name, spec: JSON.stringify(spec), workspaceId: p.workspaceId });
    return reply.code(201).send({ ...summary(row), createdAt: row.createdAt, spec });
  });

  app.delete<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    await store.deleteProject(p.id);
    return { ok: true };
  });

  app.post<{ Params: { id: string } }>('/projects/:id/share', { preHandler: requireUser, schema: { params: idParams, body: obj() } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    const shareId = p.shareId ?? newId(9);
    if (!p.shareId) await store.setShareId(p.id, shareId);
    return { shareId };
  });

  // Sharing again later issues a new id, so a revoked link never comes back to life.
  app.delete<{ Params: { id: string } }>('/projects/:id/share', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    if (p.shareId) await store.setShareId(p.id, null);
    return { ok: true };
  });

  app.get<{ Params: { shareId: string } }>('/share/:shareId', { schema: { params: obj({ shareId: str(40, ID) }, ['shareId']) } }, async (req, reply) => {
    const p = await store.getProjectByShareId(req.params.shareId);
    if (!p) return reply.code(404).send({ error: 'Not found' });
    reply.header('cache-control', 'no-cache');
    return { name: p.name, spec: JSON.parse(p.spec) as DappSpec };
  });

  // ---- catalog flags ----
  app.get('/catalog', async (_req, reply) => {
    // A cached copy would keep showing a widget for a while after an admin hides it.
    reply.header('cache-control', 'no-cache');
    return { flags: await store.getFlags() };
  });

  app.put<{ Params: { id: string }; Body: CatalogFlag }>(
    '/admin/catalog/:id',
    {
      preHandler: requireAdmin,
      schema: {
        params: obj({ id: str(80, FLAG_ID) }, ['id']),
        body: obj({
          hidden: { type: 'boolean' },
          featured: { type: 'boolean' },
          status: { type: 'string', enum: ['stable', 'beta', 'experimental'] },
          minVersion: str(20, '^\\d+\\.\\d+\\.\\d+$'),
        }),
      },
    },
    async (req) => {
      const b = req.body;
      const flag: CatalogFlag = {};
      if (b.hidden) flag.hidden = true;
      if (b.featured) flag.featured = true;
      if (b.status) flag.status = b.status;
      if (b.minVersion) flag.minVersion = b.minVersion;
      await store.setFlag(req.params.id, Object.keys(flag).length ? flag : null);
      await store.audit(req.session!.address, 'catalog.flag', req.params.id, flag);
      return { flags: await store.getFlags() };
    },
  );

  // ---- releases ----
  app.get<{ Querystring: { refresh?: string } }>(
    '/admin/releases',
    { preHandler: requireAdmin, schema: { querystring: obj({ refresh: { type: 'string', enum: ['0', '1'] } }) } },
    async (req) => releases.status(req.query.refresh === '1'),
  );

  app.post<{ Body: { channel: 'latest' | 'next'; dryRun: boolean } }>(
    '/admin/releases',
    {
      preHandler: requireAdmin,
      config: { rateLimit: { max: 5, timeWindow: '1 minute' } },
      schema: { body: obj({ channel: { type: 'string', enum: ['latest', 'next'] }, dryRun: { type: 'boolean' } }, ['channel', 'dryRun']) },
    },
    async (req, reply) => {
      const { channel, dryRun } = req.body;
      try {
        await releases.dispatch({ channel, dryRun });
      } catch (e) {
        await store.audit(req.session!.address, 'release.dispatch_failed', channel, { dryRun, error: String((e as Error).message) });
        return reply.code(502).send({ error: (e as Error).message });
      }
      await store.audit(req.session!.address, 'release.dispatch', channel, { dryRun });
      return { dispatched: true, dryRun };
    },
  );

  // ---- audit + admins ----
  app.get<{ Querystring: { limit?: string } }>(
    '/admin/audit',
    { preHandler: requireAdmin, schema: { querystring: obj({ limit: str(3, '^[0-9]{1,3}$') }) } },
    async (req) => ({
      entries: await store.listAudit(Math.min(500, Math.max(1, Number(req.query.limit ?? 200) || 200))),
    }),
  );

  app.get('/admin/admins', { preHandler: requireAdmin }, async () => {
    const db = await store.listAdmins();
    const env = config.adminAddresses.map((address) => ({ address, source: 'env' as const, addedAt: null }));
    return {
      admins: [...env, ...db.filter((a) => !config.adminAddresses.includes(a.address)).map((a) => ({ address: a.address, source: 'db' as const, addedAt: a.addedAt }))],
    };
  });

  app.post<{ Body: { address: string } }>(
    '/admin/admins',
    { preHandler: requireAdmin, schema: { body: obj({ address: str(80, ADDRESS) }, ['address']) } },
    async (req) => {
      const address = canonicalAddress(req.body.address)!;
      await store.addAdmin(address, req.session!.address);
      await store.audit(req.session!.address, 'admin.add', address);
      return { ok: true };
    },
  );

  app.delete<{ Params: { address: string } }>(
    '/admin/admins/:address',
    { preHandler: requireAdmin, schema: { params: obj({ address: str(80, ADDRESS) }, ['address']) } },
    async (req, reply) => {
      const address = canonicalAddress(req.params.address)!;
      if (address === req.session!.address) return reply.code(400).send({ error: 'You cannot remove yourself' });
      if (!(await store.removeAdmin(address))) return reply.code(404).send({ error: 'Not an admin added here' });
      await store.audit(req.session!.address, 'admin.remove', address);
      return { ok: true };
    },
  );

  // ---- hosted publishing (phase 2, behind HOSTING_ENABLED) ----
  app.post<{ Body: { projectId: string; subdomain: string } }>(
    '/deployments',
    {
      preHandler: requireUser,
      schema: { body: obj({ projectId: str(40, ID), subdomain: str(32) }, ['projectId', 'subdomain']) },
    },
    async (req, reply) => {
      if (!config.hostingEnabled) return reply.code(404).send({ error: 'Hosting is not enabled' });
      const { projectId, subdomain } = req.body;
      if (!/^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$/.test(subdomain)) {
        return reply.code(400).send({ error: 'subdomain must be 3-32 lowercase letters, digits or dashes' });
      }
      const p = await store.getProject(projectId);
      if (!p || p.owner !== req.session!.address) return reply.code(404).send({ error: 'Not found' });
      const r = checkSpec(JSON.parse(p.spec));
      if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
      try {
        const d = await store.createDeployment({ id: newId(), projectId, owner: p.owner, subdomain, status: 'queued' });
        await store.audit(p.owner, 'deployment.create', d.id, { projectId, subdomain });
        return reply.code(202).send(d);
      } catch {
        return reply.code(409).send({ error: 'Subdomain is taken' });
      }
    },
  );

  app.get<{ Params: { id: string } }>('/deployments/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!config.hostingEnabled) return reply.code(404).send({ error: 'Hosting is not enabled' });
    const d = await store.getDeployment(req.params.id);
    if (!d || d.owner !== req.session!.address) return reply.code(404).send({ error: 'Not found' });
    return d;
  });

  if (deps.housekeepingIntervalMs) {
    const stop = startHousekeeping(store, config, app.log, deps.housekeepingIntervalMs);
    app.addHook('onClose', async () => stop());
  }

  return app;
}
