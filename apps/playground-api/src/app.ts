import { createHash, randomBytes } from 'node:crypto';
import Fastify, { type FastifyReply, type FastifyRequest } from 'fastify';
import cookie from '@fastify/cookie';
import cors from '@fastify/cors';
import rateLimit from '@fastify/rate-limit';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, migrateSpec, validateSpec, type DappSpec } from '@splendor/dapp-spec';
import { issueNonce, parseAuthMessage, verifySignIn, type NonceStore } from '@splendor/sqc-sdk/auth';
import type { ApiConfig } from './config.js';
import { createReleaseService, type FetchLike, type ReleaseService } from './releases.js';
import type { CatalogFlag, Store } from './store/types.js';

export type AppDeps = {
  config: ApiConfig;
  store: Store;
  /** Signature verifier override (tests). Defaults to the SDK's WASM ML-DSA verifier. */
  verifier?: (publicKeyB64: string, message: string, signatureB64: string) => Promise<boolean>;
  fetch?: FetchLike;
  releases?: ReleaseService;
  logger?: boolean;
};

const COOKIE = 'sqc_pg_session';
const ADDRESS_RE = /^SQ:[0-9a-fA-F]{64}$/;
const ID_RE = /^[A-Za-z0-9_-]{10,40}$/;
const FLAG_ID_RE = /^(template:|recipe:)?[a-z0-9][a-z0-9-]{0,63}$/;
const MAX_PROJECTS_PER_USER = 100;
const NONCE_TTL_SEC = 300;

const sha256 = (s: string) => createHash('sha256').update(s).digest('hex');
const newId = (bytes = 12) => randomBytes(bytes).toString('base64url');

type Session = { address: string; isAdmin: boolean };

declare module 'fastify' {
  interface FastifyRequest {
    session: Session | null;
  }
}

export async function buildApp(deps: AppDeps) {
  const { config, store } = deps;
  const catalog = createCatalog(WIDGET_MANIFESTS);
  const releases = deps.releases ?? createReleaseService(config.github, deps.fetch);
  const app = Fastify({ logger: deps.logger ?? false, bodyLimit: 512 * 1024, trustProxy: config.production });

  await app.register(cookie);
  await app.register(cors, { origin: config.origins, credentials: true, methods: ['GET', 'POST', 'PUT', 'DELETE'] });
  await app.register(rateLimit, { max: 300, timeWindow: '1 minute' });

  // CSRF: state-changing requests must come from an allowed origin (browsers always send Origin
  // on cross-site POST/PUT/DELETE) and carry JSON, which a plain HTML form cannot send.
  app.addHook('onRequest', async (req, reply) => {
    if (req.method === 'GET' || req.method === 'HEAD' || req.method === 'OPTIONS') return;
    const origin = req.headers.origin;
    if (origin && !config.origins.includes(origin)) return reply.code(403).send({ error: 'Origin not allowed' });
    if (req.method !== 'DELETE' && !String(req.headers['content-type'] ?? '').startsWith('application/json')) {
      return reply.code(415).send({ error: 'Expected application/json' });
    }
  });

  const isAdmin = async (address: string) => config.adminAddresses.includes(address.toLowerCase()) || (await store.isAdmin(address));

  app.decorateRequest('session', null);
  app.addHook('preHandler', async (req) => {
    const raw = req.cookies[COOKIE];
    if (!raw) return;
    const s = await store.getSession(sha256(raw), Math.floor(Date.now() / 1000));
    if (s) req.session = { address: s.address, isAdmin: await isAdmin(s.address) };
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

  async function startSession(reply: FastifyReply, address: string): Promise<Session> {
    const id = randomBytes(32).toString('base64url');
    await store.createSession(sha256(id), address, Math.floor(Date.now() / 1000) + config.sessionTtlSec);
    reply.setCookie(COOKIE, id, {
      httpOnly: true,
      secure: config.cookieSecure,
      sameSite: config.cookieSameSite,
      path: '/',
      maxAge: config.sessionTtlSec,
    });
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

  // ---- auth ----
  const authLimit = { config: { rateLimit: { max: 30, timeWindow: '1 minute' } } };

  app.post('/auth/nonce', authLimit, async () => {
    const nonce = issueNonce();
    await nonceStore.put(nonce, Math.floor(Date.now() / 1000) + NONCE_TTL_SEC);
    return { nonce };
  });

  app.post<{ Body: { message?: unknown; signatureB64?: unknown; publicKeyB64?: unknown; address?: unknown } }>('/auth/verify', authLimit, async (req, reply) => {
    const { message, signatureB64, publicKeyB64, address } = req.body ?? {};
    if (typeof message !== 'string' || typeof signatureB64 !== 'string' || typeof publicKeyB64 !== 'string' || typeof address !== 'string') {
      return reply.code(400).send({ error: 'message, signatureB64, publicKeyB64 and address are required' });
    }
    if (message.length > 4000 || signatureB64.length > 10_000 || publicKeyB64.length > 5000) return reply.code(400).send({ error: 'Payload too large' });
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
      await store.audit(address.slice(0, 80), 'auth.failed', null, { reason: result.reason });
      return reply.code(401).send({ error: `Sign-in failed (${result.reason})` });
    }
    const session = await startSession(reply, result.address);
    await store.audit(result.address, 'auth.signin', null, { admin: session.isAdmin });
    return session;
  });

  if (config.allowTestLogin) {
    app.post<{ Body: { address?: string } }>('/auth/test-login', async (req, reply) => {
      const address = req.body?.address ?? '';
      if (!ADDRESS_RE.test(address)) return reply.code(400).send({ error: 'address must be an SQ: address' });
      return startSession(reply, address);
    });
  }

  app.get('/auth/me', async (req, reply) => {
    if (!req.session) return reply.code(401).send({ error: 'Not signed in' });
    return req.session;
  });

  app.post('/auth/logout', async (req, reply) => {
    const raw = req.cookies[COOKIE];
    if (raw) await store.deleteSession(sha256(raw));
    reply.clearCookie(COOKIE, { path: '/' });
    return { ok: true };
  });

  // ---- projects ----
  const summary = (p: { id: string; name: string; updatedAt: string; shareId: string | null }) => ({
    id: p.id,
    name: p.name,
    updatedAt: p.updatedAt,
    shareId: p.shareId,
  });

  app.get('/projects', { preHandler: requireUser }, async (req) => ({
    projects: (await store.listProjects(req.session!.address)).map(summary),
  }));

  app.post<{ Body: { spec?: unknown } }>('/projects', { preHandler: requireUser }, async (req, reply) => {
    const r = checkSpec(req.body?.spec);
    if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
    if ((await store.countProjects(req.session!.address)) >= MAX_PROJECTS_PER_USER) {
      return reply.code(429).send({ error: `Project limit reached (${MAX_PROJECTS_PER_USER})` });
    }
    const row = await store.createProject({ id: newId(), owner: req.session!.address, name: r.spec.name, spec: JSON.stringify(r.spec) });
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

  app.get<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    return { ...summary(p), createdAt: p.createdAt, spec: JSON.parse(p.spec) as DappSpec };
  });

  app.put<{ Params: { id: string }; Body: { spec?: unknown } }>('/projects/:id', { preHandler: requireUser }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    const r = checkSpec(req.body?.spec);
    if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
    const row = await store.updateProject(p.id, { name: r.spec.name, spec: JSON.stringify(r.spec) });
    return { ...summary(row!), createdAt: row!.createdAt, spec: r.spec };
  });

  app.delete<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    await store.deleteProject(p.id);
    return { ok: true };
  });

  app.post<{ Params: { id: string } }>('/projects/:id/share', { preHandler: requireUser }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    const shareId = p.shareId ?? newId(9);
    if (!p.shareId) await store.setShareId(p.id, shareId);
    return { shareId };
  });

  app.get<{ Params: { shareId: string } }>('/share/:shareId', async (req, reply) => {
    if (!ID_RE.test(req.params.shareId)) return reply.code(404).send({ error: 'Not found' });
    const p = await store.getProjectByShareId(req.params.shareId);
    if (!p) return reply.code(404).send({ error: 'Not found' });
    return { name: p.name, spec: JSON.parse(p.spec) as DappSpec };
  });

  // ---- catalog flags ----
  app.get('/catalog', async (_req, reply) => {
    // A cached copy would keep showing a widget for a while after an admin hides it.
    reply.header('cache-control', 'no-cache');
    return { flags: await store.getFlags() };
  });

  app.put<{ Params: { id: string }; Body: CatalogFlag }>('/admin/catalog/:id', { preHandler: requireAdmin }, async (req, reply) => {
    const id = req.params.id;
    if (!FLAG_ID_RE.test(id)) return reply.code(400).send({ error: 'Bad id' });
    const b = req.body ?? {};
    const flag: CatalogFlag = {};
    if (b.hidden !== undefined) {
      if (typeof b.hidden !== 'boolean') return reply.code(400).send({ error: 'hidden must be boolean' });
      if (b.hidden) flag.hidden = true;
    }
    if (b.featured !== undefined) {
      if (typeof b.featured !== 'boolean') return reply.code(400).send({ error: 'featured must be boolean' });
      if (b.featured) flag.featured = true;
    }
    if (b.status !== undefined) {
      if (!['stable', 'beta', 'experimental'].includes(b.status)) return reply.code(400).send({ error: 'bad status' });
      flag.status = b.status;
    }
    if (b.minVersion !== undefined) {
      if (typeof b.minVersion !== 'string' || !/^\d+\.\d+\.\d+$/.test(b.minVersion)) return reply.code(400).send({ error: 'minVersion must be x.y.z' });
      flag.minVersion = b.minVersion;
    }
    await store.setFlag(id, Object.keys(flag).length ? flag : null);
    await store.audit(req.session!.address, 'catalog.flag', id, flag);
    return { flags: await store.getFlags() };
  });

  // ---- releases ----
  app.get<{ Querystring: { refresh?: string } }>('/admin/releases', { preHandler: requireAdmin }, async (req) => releases.status(req.query.refresh === '1'));

  app.post<{ Body: { channel?: unknown; dryRun?: unknown } }>(
    '/admin/releases',
    { preHandler: requireAdmin, config: { rateLimit: { max: 5, timeWindow: '1 minute' } } },
    async (req, reply) => {
      const { channel, dryRun } = req.body ?? {};
      if (channel !== 'latest' && channel !== 'next') return reply.code(400).send({ error: 'channel must be latest or next' });
      if (typeof dryRun !== 'boolean') return reply.code(400).send({ error: 'dryRun must be boolean' });
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
  app.get<{ Querystring: { limit?: string } }>('/admin/audit', { preHandler: requireAdmin }, async (req) => ({
    entries: await store.listAudit(Math.min(500, Math.max(1, Number(req.query.limit ?? 200) || 200))),
  }));

  app.get('/admin/admins', { preHandler: requireAdmin }, async () => {
    const db = await store.listAdmins();
    const env = config.adminAddresses.map((address) => ({ address, source: 'env' as const, addedAt: null }));
    return {
      admins: [...env, ...db.filter((a) => !config.adminAddresses.includes(a.address)).map((a) => ({ address: a.address, source: 'db' as const, addedAt: a.addedAt }))],
    };
  });

  app.post<{ Body: { address?: unknown } }>('/admin/admins', { preHandler: requireAdmin }, async (req, reply) => {
    const address = req.body?.address;
    if (typeof address !== 'string' || !ADDRESS_RE.test(address)) return reply.code(400).send({ error: 'address must be an SQ: address' });
    await store.addAdmin(address, req.session!.address);
    await store.audit(req.session!.address, 'admin.add', address.toLowerCase());
    return { ok: true };
  });

  app.delete<{ Params: { address: string } }>('/admin/admins/:address', { preHandler: requireAdmin }, async (req, reply) => {
    const address = req.params.address;
    if (address.toLowerCase() === req.session!.address.toLowerCase()) return reply.code(400).send({ error: 'You cannot remove yourself' });
    if (!(await store.removeAdmin(address))) return reply.code(404).send({ error: 'Not an admin added here' });
    await store.audit(req.session!.address, 'admin.remove', address.toLowerCase());
    return { ok: true };
  });

  // ---- hosted publishing (phase 2, behind HOSTING_ENABLED) ----
  app.post<{ Body: { projectId?: unknown; subdomain?: unknown } }>('/deployments', { preHandler: requireUser }, async (req, reply) => {
    if (!config.hostingEnabled) return reply.code(404).send({ error: 'Hosting is not enabled' });
    const { projectId, subdomain } = req.body ?? {};
    if (typeof projectId !== 'string' || !ID_RE.test(projectId)) return reply.code(400).send({ error: 'projectId required' });
    if (typeof subdomain !== 'string' || !/^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$/.test(subdomain)) {
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
  });

  app.get<{ Params: { id: string } }>('/deployments/:id', { preHandler: requireUser }, async (req, reply) => {
    if (!config.hostingEnabled) return reply.code(404).send({ error: 'Hosting is not enabled' });
    const d = ID_RE.test(req.params.id) ? await store.getDeployment(req.params.id) : null;
    if (!d || d.owner !== req.session!.address) return reply.code(404).send({ error: 'Not found' });
    return d;
  });

  return app;
}
