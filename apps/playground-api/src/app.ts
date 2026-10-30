import { randomUUID } from 'node:crypto';
import type { IncomingMessage } from 'node:http';
import Fastify, { type FastifyReply, type FastifyRequest } from 'fastify';
import cookie, { type CookieSerializeOptions } from '@fastify/cookie';
import cors from '@fastify/cors';
import rateLimit from '@fastify/rate-limit';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, migrateSpec, validateSpec } from '@splendor/dapp-spec';
import type { NonceStore } from '@splendor/sqc-sdk/auth';
import { canonicalAddress, type ApiConfig } from './config.js';
import { startHousekeeping } from './housekeeping.js';
import { createReleaseService, type FetchLike, type ReleaseService } from './releases.js';
import { registerAdminRoutes } from './routes/admin.js';
import { registerAuthRoutes, sha256 } from './routes/auth.js';
import { ID_RE, type RouteContext } from './routes/context.js';
import { registerDeploymentRoutes } from './routes/deployments.js';
import { registerProjectRoutes } from './routes/projects.js';
import { registerShareRoutes } from './routes/share.js';
import { registerWorkspaceRoutes } from './routes/workspaces.js';
import type { Store } from './store/types.js';

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

const REQUEST_ID_RE = /^[A-Za-z0-9_.:-]{1,64}$/;

/**
 * Wires the store, plugins, hooks and limits, then registers the route modules in ./routes
 * (auth, workspaces and trash, projects and revisions, share links, admin, deployments).
 */
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
    // JSON only: nothing here should ever run as a document, load subresources or be framed.
    reply.header('content-security-policy', "default-src 'none'; frame-ancestors 'none'");
    if (config.cookieSecure) reply.header('strict-transport-security', 'max-age=31536000; includeSubDomains');
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

  const nonceStore: NonceStore = {
    put: (n, exp) => store.putNonce(n, exp),
    consume: (n, nowSec) => store.consumeNonce(n, nowSec),
  };

  const ctx: RouteContext = {
    app,
    config,
    store,
    catalog,
    releases,
    ...(deps.verifier ? { verifier: deps.verifier } : {}),
    nonceStore,
    isAdmin,
    requireUser: async (req: FastifyRequest, reply: FastifyReply) => {
      if (!req.session) return reply.code(401).send({ error: 'Sign in first' });
    },
    requireAdmin: async (req: FastifyRequest, reply: FastifyReply) => {
      if (!req.session) return reply.code(401).send({ error: 'Sign in first' });
      if (!req.session.isAdmin) return reply.code(403).send({ error: 'Admins only' });
    },
    cookieOptions: (): CookieSerializeOptions => ({
      httpOnly: true,
      secure: config.cookieSecure,
      sameSite: config.cookieSameSite,
      path: '/',
    }),
    checkSpec(input) {
      try {
        return validateSpec(migrateSpec(input), { catalog });
      } catch (e) {
        return { ok: false, issues: [{ path: '', message: e instanceof Error ? e.message : String(e) }] };
      }
    },
    async ownedProject(req, reply, opts = {}) {
      const p = ID_RE.test(req.params.id) ? await store.getProject(req.params.id) : null;
      if (!p || p.owner !== req.session!.address || (p.deletedAt && !opts.includeDeleted)) {
        reply.code(404).send({ error: 'Not found' });
        return null;
      }
      return p;
    },
    ownsWorkspace: async (owner, id) => (await store.getWorkspace(id, owner)) !== null,
    async audit(req, actor, action, target = null, detail) {
      try {
        await store.audit(actor, action, target, detail);
      } catch (err) {
        req.log.error({ err, action }, 'audit write failed');
      }
    },
  };

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

  registerAuthRoutes(ctx);
  registerWorkspaceRoutes(ctx);
  registerProjectRoutes(ctx);
  registerShareRoutes(ctx);
  registerAdminRoutes(ctx);
  registerDeploymentRoutes(ctx);

  if (deps.housekeepingIntervalMs) {
    const stop = startHousekeeping(store, config, app.log, deps.housekeepingIntervalMs);
    app.addHook('onClose', async () => stop());
  }

  return app;
}
