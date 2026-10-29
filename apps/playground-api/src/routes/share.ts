import { ID, idParams, newId, obj, parseStoredSpec, str, type RouteContext } from './context.js';

const shareBody = obj({ expiresInDays: { type: 'integer', minimum: 1, maximum: 365 } });
const DAY_MS = 24 * 3600 * 1000;

const expired = (expiresAt: string | null | undefined, now = Date.now()) => Boolean(expiresAt && Date.parse(expiresAt) <= now);

/** Read-only share links: always the project's current spec, until revoked or expired. */
export function registerShareRoutes(ctx: RouteContext) {
  const { app, store, requireUser, ownedProject, audit } = ctx;

  app.post<{ Params: { id: string }; Body: { expiresInDays?: number } }>(
    '/projects/:id/share',
    { preHandler: requireUser, schema: { params: idParams, body: shareBody } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      if (p.shareId && !expired(p.shareExpiresAt)) return { shareId: p.shareId, expiresAt: p.shareExpiresAt ?? null, views: p.shareViews ?? 0 };
      // No link, or an expired one: a new id, so an old link never comes back to life.
      const shareId = newId(9);
      const expiresAt = req.body.expiresInDays ? new Date(Date.now() + req.body.expiresInDays * DAY_MS).toISOString() : null;
      await store.setShareId(p.id, shareId, expiresAt);
      await audit(req, p.owner, 'project.share', p.id, { expiresAt });
      return { shareId, expiresAt, views: 0 };
    },
  );

  // Sharing again later issues a new id, so a revoked link never comes back to life.
  app.delete<{ Params: { id: string } }>('/projects/:id/share', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    if (p.shareId) {
      await store.setShareId(p.id, null);
      await audit(req, p.owner, 'project.unshare', p.id);
    }
    return { ok: true };
  });

  app.get<{ Params: { shareId: string } }>('/share/:shareId', { schema: { params: obj({ shareId: str(40, ID) }, ['shareId']) } }, async (req, reply) => {
    const p = await store.getProjectByShareId(req.params.shareId);
    if (!p) return reply.code(404).send({ error: 'Not found' });
    if (expired(p.shareExpiresAt)) return reply.code(404).send({ error: 'This share link has expired', code: 'EXPIRED' });
    // Fastify answers HEAD with this handler too; only a real GET is a view.
    if (req.method === 'GET') await store.recordShareView(p.id);
    reply.header('cache-control', 'no-cache');
    return { name: p.name, spec: parseStoredSpec(p) };
  });
}
