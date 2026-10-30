import type { FastifyReply } from 'fastify';
import type { DappSpec } from '@splendor/dapp-spec';
import { ProjectLimitError, type ListProjectsOptions, type ProjectPatch } from '../store/types.js';
import { ID, MAX_PROJECTS_PER_USER, idParams, newId, obj, parseStoredSpec, str, summary, type RouteContext } from './context.js';

/** `baseVersion`: the version the edit started from; a stale one is refused with 409 instead of overwriting. */
const specBody = obj({ spec: { type: 'object' }, baseVersion: { type: 'integer', minimum: 1 } }, ['spec']);
const createProjectBody = obj({ spec: { type: 'object' }, workspaceId: str(40, ID) }, ['spec']);
const patchProjectBody = obj({ name: str(200), workspaceId: { anyOf: [str(40, ID), { type: 'null' }] }, baseVersion: { type: 'integer', minimum: 1 } });
const revisionParams = obj({ id: str(40, ID), version: str(9, '^[1-9][0-9]{0,8}$') }, ['id', 'version']);

export type ListQuery = { workspaceId?: string; q?: string; sort?: 'updated' | 'name'; limit?: string; cursor?: string };
export const listQuery = obj({
  workspaceId: str(40, ID),
  q: str(80),
  sort: { type: 'string', enum: ['updated', 'name'] },
  limit: str(3, '^[1-9][0-9]{0,2}$'),
  cursor: str(1000, '^[A-Za-z0-9_-]+$'),
});

export function toListOptions(q: ListQuery): ListProjectsOptions {
  return {
    ...(q.workspaceId ? { workspaceId: q.workspaceId } : {}),
    ...(q.q ? { q: q.q } : {}),
    ...(q.sort ? { sort: q.sort } : {}),
    ...(q.limit ? { limit: Number(q.limit) } : {}),
    ...(q.cursor ? { cursor: q.cursor } : {}),
  };
}

/** Autosave writes every few seconds; the audit log keeps one `project.update` per project per window. */
const UPDATE_AUDIT_WINDOW_MS = 10 * 60_000;

export function registerProjectRoutes(ctx: RouteContext) {
  const { app, store, requireUser, ownedProject, ownsWorkspace, checkSpec, audit } = ctx;
  const lastUpdateAudit = new Map<string, number>();
  const auditUpdate = async (req: Parameters<typeof audit>[0], actor: string, id: string, detail?: unknown) => {
    const t = Date.now();
    if (!detail && t - (lastUpdateAudit.get(id) ?? 0) < UPDATE_AUDIT_WINDOW_MS) return;
    lastUpdateAudit.set(id, t);
    if (lastUpdateAudit.size > 10_000) lastUpdateAudit.clear();
    await audit(req, actor, 'project.update', id, detail);
  };

  /** Runs a write that counts toward the project cap; on `ProjectLimitError` replies 429 and returns null. */
  const withinLimit = async <T>(reply: FastifyReply, write: () => Promise<T>): Promise<T | null> => {
    try {
      return await write();
    } catch (e) {
      if (!(e instanceof ProjectLimitError)) throw e;
      reply.code(429).send({ error: `Project limit reached (${MAX_PROJECTS_PER_USER})`, code: 'PROJECT_LIMIT' });
      return null;
    }
  };

  const conflict = async (reply: FastifyReply, id: string) => {
    const current = await store.getProject(id);
    if (!current || current.deletedAt) return reply.code(404).send({ error: 'Not found' });
    return reply.code(409).send({
      error: 'This project was changed somewhere else since you opened it',
      code: 'CONFLICT',
      current: { ...summary(current), createdAt: current.createdAt },
    });
  };

  app.get<{ Querystring: ListQuery }>('/projects', { preHandler: requireUser, schema: { querystring: listQuery } }, async (req) => {
    const page = await store.listProjects(req.session!.address, toListOptions(req.query));
    return { projects: page.projects.map(summary), nextCursor: page.nextCursor };
  });

  app.post<{ Body: { spec: unknown; workspaceId?: string } }>('/projects', { preHandler: requireUser, schema: { body: createProjectBody } }, async (req, reply) => {
    const r = checkSpec(req.body.spec);
    if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
    const workspaceId = req.body.workspaceId ?? null;
    if (workspaceId && !(await ownsWorkspace(req.session!.address, workspaceId))) return reply.code(400).send({ error: 'Unknown workspace' });
    const row = await withinLimit(reply, () =>
      store.createProject(
        { id: newId(), owner: req.session!.address, name: r.spec.name, spec: JSON.stringify(r.spec), workspaceId },
        { maxProjects: MAX_PROJECTS_PER_USER },
      ),
    );
    if (!row) return reply;
    await audit(req, row.owner, 'project.create', row.id, { name: row.name });
    return reply.code(201).send({ ...summary(row), createdAt: row.createdAt, spec: r.spec });
  });

  app.get<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    return { ...summary(p), createdAt: p.createdAt, spec: parseStoredSpec(p) };
  });

  app.put<{ Params: { id: string }; Body: { spec: unknown; baseVersion?: number } }>(
    '/projects/:id',
    { preHandler: requireUser, schema: { params: idParams, body: specBody } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      const r = checkSpec(req.body.spec);
      if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
      // Without `baseVersion` the write lands on whatever is stored (last write wins); that path is
      // kept for older clients only and is flagged so it can be removed.
      if (req.body.baseVersion === undefined) reply.header('deprecation', 'true');
      const row = await store.updateProject(p.id, { name: r.spec.name, spec: JSON.stringify(r.spec) }, req.body.baseVersion);
      if (!row) return conflict(reply, p.id);
      await auditUpdate(req, p.owner, p.id);
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
        const spec = { ...parseStoredSpec(p), name };
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
      // The stored spec is rewritten from the row read above, so the write must land on that row's
      // version even when the client did not send one.
      const row = await store.updateProject(p.id, patch, req.body.baseVersion ?? p.version);
      if (!row) return conflict(reply, p.id);
      await auditUpdate(req, p.owner, p.id, { ...(patch.name ? { name: patch.name } : {}), ...(patch.workspaceId !== undefined ? { workspaceId: patch.workspaceId } : {}) });
      return { ...summary(row), createdAt: row.createdAt };
    },
  );

  app.post<{ Params: { id: string } }>('/projects/:id/duplicate', { preHandler: requireUser, schema: { params: idParams, body: obj() } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    const name = `${p.name} copy`.slice(0, 80);
    const spec = { ...parseStoredSpec(p), name };
    const row = await withinLimit(reply, () =>
      store.createProject({ id: newId(), owner: p.owner, name, spec: JSON.stringify(spec), workspaceId: p.workspaceId }, { maxProjects: MAX_PROJECTS_PER_USER }),
    );
    if (!row) return reply;
    await audit(req, p.owner, 'project.create', row.id, { name, from: p.id });
    return reply.code(201).send({ ...summary(row), createdAt: row.createdAt, spec });
  });

  // To the trash; restorable for 30 days. Its share link stops working right away.
  app.delete<{ Params: { id: string } }>('/projects/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    await store.deleteProject(p.id);
    await audit(req, p.owner, 'project.delete', p.id);
    return { ok: true };
  });

  app.post<{ Params: { id: string } }>(
    '/trash/projects/:id/restore',
    { preHandler: requireUser, schema: { params: idParams, body: obj() } },
    async (req, reply) => {
      const p = await ownedProject(req, reply, { includeDeleted: true });
      if (!p) return reply;
      if (!p.deletedAt) return reply.code(404).send({ error: 'Not found' });
      const restored = await withinLimit(reply, () => store.restoreProject(p.id, { maxProjects: MAX_PROJECTS_PER_USER }));
      if (restored === null) return reply;
      if (!restored) return reply.code(404).send({ error: 'Not found' });
      await audit(req, p.owner, 'project.restore', p.id);
      return { ok: true };
    },
  );

  app.delete<{ Params: { id: string } }>('/trash/projects/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply, { includeDeleted: true });
    if (!p) return reply;
    if (!p.deletedAt) return reply.code(404).send({ error: 'Not found' });
    await store.purgeProject(p.id);
    await audit(req, p.owner, 'project.purge', p.id);
    return { ok: true };
  });

  // ---- revisions ----
  app.get<{ Params: { id: string } }>('/projects/:id/revisions', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    const p = await ownedProject(req, reply);
    if (!p) return reply;
    return { revisions: await store.listRevisions(p.id) };
  });

  app.get<{ Params: { id: string; version: string } }>(
    '/projects/:id/revisions/:version',
    { preHandler: requireUser, schema: { params: revisionParams } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      const version = Number(req.params.version);
      const spec = await store.getRevision(p.id, version);
      if (spec === null) return reply.code(404).send({ error: 'Not found' });
      return { version, spec: parseStoredSpec({ id: p.id, spec }) };
    },
  );

  // A restore is a normal save of the old spec on top of the current version: history only grows.
  app.post<{ Params: { id: string; version: string }; Body: { baseVersion: number } }>(
    '/projects/:id/revisions/:version/restore',
    { preHandler: requireUser, schema: { params: revisionParams, body: obj({ baseVersion: { type: 'integer', minimum: 1 } }, ['baseVersion']) } },
    async (req, reply) => {
      const p = await ownedProject(req, reply);
      if (!p) return reply;
      const version = Number(req.params.version);
      const old = await store.getRevision(p.id, version);
      if (old === null) return reply.code(404).send({ error: 'Not found' });
      const r = checkSpec(parseStoredSpec({ id: p.id, spec: old }));
      if (!r.ok) return reply.code(422).send({ error: 'That version no longer passes validation', issues: r.issues });
      const row = await store.updateProject(p.id, { name: r.spec.name, spec: JSON.stringify(r.spec) }, req.body.baseVersion);
      if (!row) return conflict(reply, p.id);
      await auditUpdate(req, p.owner, p.id, { restoredFrom: version });
      return { ...summary(row), createdAt: row.createdAt, spec: r.spec as DappSpec };
    },
  );
}
