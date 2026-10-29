import { MAX_PROJECTS_PER_USER, idParams, newId, obj, str, summary, type RouteContext } from './context.js';
import { listQuery, toListOptions, type ListQuery } from './projects.js';

const workspaceNameBody = obj({ name: str(200) });

export function registerWorkspaceRoutes(ctx: RouteContext) {
  const { app, store, requireUser, ownsWorkspace, audit } = ctx;

  app.get('/workspaces', { preHandler: requireUser }, async (req) => ({
    workspaces: await store.listWorkspaces(req.session!.address),
  }));

  app.post<{ Body: { name?: string } }>('/workspaces', { preHandler: requireUser, schema: { body: workspaceNameBody } }, async (req, reply) => {
    const name = (typeof req.body.name === 'string' && req.body.name.trim() ? req.body.name.trim() : 'My workspace').slice(0, 80);
    const row = await store.createWorkspace({ id: newId(), owner: req.session!.address, name });
    await audit(req, req.session!.address, 'workspace.create', row.id, { name });
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
      await audit(req, req.session!.address, 'workspace.update', row.id, { name });
      return row;
    },
  );

  // To the trash, with its projects; restorable for 30 days.
  app.delete<{ Params: { id: string } }>('/workspaces/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!(await ownsWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
    await store.deleteWorkspace(req.params.id);
    await audit(req, req.session!.address, 'workspace.delete', req.params.id);
    return { ok: true };
  });

  app.get<{ Params: { id: string }; Querystring: ListQuery }>(
    '/workspaces/:id/projects',
    { preHandler: requireUser, schema: { params: idParams, querystring: listQuery } },
    async (req, reply) => {
      if (!(await ownsWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
      const page = await store.listProjects(req.session!.address, { ...toListOptions(req.query), workspaceId: req.params.id });
      return { projects: page.projects.map(summary), nextCursor: page.nextCursor };
    },
  );

  // ---- trash ----
  app.get<{ Querystring: { limit?: string; cursor?: string } }>(
    '/trash',
    { preHandler: requireUser, schema: { querystring: obj({ limit: str(3, '^[1-9][0-9]{0,2}$'), cursor: str(1000, '^[A-Za-z0-9_-]+$') }) } },
    async (req) => {
      const trash = await store.listTrash(req.session!.address, {
        ...(req.query.limit ? { limit: Number(req.query.limit) } : {}),
        ...(req.query.cursor ? { cursor: req.query.cursor } : {}),
      });
      return { projects: trash.projects.map(summary), workspaces: trash.workspaces, nextCursor: trash.nextCursor };
    },
  );

  const trashedWorkspace = async (owner: string, id: string) => {
    const w = await store.getWorkspace(id, owner, true);
    return w?.deletedAt ? w : null;
  };

  app.post<{ Params: { id: string } }>(
    '/trash/workspaces/:id/restore',
    { preHandler: requireUser, schema: { params: idParams, body: obj() } },
    async (req, reply) => {
      if (!(await trashedWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
      const result = await store.restoreWorkspace(req.params.id, { maxProjects: MAX_PROJECTS_PER_USER });
      if (result === 'not-found') return reply.code(404).send({ error: 'Not found' });
      if (result === 'limit') {
        return reply.code(429).send({ error: `Restoring this workspace would exceed the project limit (${MAX_PROJECTS_PER_USER}). Delete some projects first.` });
      }
      await audit(req, req.session!.address, 'workspace.restore', req.params.id);
      return { ok: true };
    },
  );

  app.delete<{ Params: { id: string } }>('/trash/workspaces/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!(await trashedWorkspace(req.session!.address, req.params.id))) return reply.code(404).send({ error: 'Not found' });
    await store.purgeWorkspace(req.params.id);
    await audit(req, req.session!.address, 'workspace.purge', req.params.id);
    return { ok: true };
  });
}
