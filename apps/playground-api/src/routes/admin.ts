import { canonicalAddress } from '../config.js';
import type { CatalogFlag } from '../store/types.js';
import { ADDRESS, obj, str, type RouteContext } from './context.js';

const FLAG_ID = '^(template:|recipe:)?[a-z0-9][a-z0-9-]{0,63}$';

/** Catalog flags (public read, admin write), release dispatch, audit log and admin list. */
export function registerAdminRoutes(ctx: RouteContext) {
  const { app, config, store, releases, requireAdmin } = ctx;

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
}
