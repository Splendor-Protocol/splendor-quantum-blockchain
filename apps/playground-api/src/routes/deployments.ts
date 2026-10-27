import { SubdomainTakenError } from '../store/types.js';
import { ID, idParams, newId, obj, parseStoredSpec, str, type RouteContext } from './context.js';

/** Hosted publishing (phase 2, behind HOSTING_ENABLED). Records a queued deployment; no worker yet. */
export function registerDeploymentRoutes(ctx: RouteContext) {
  const { app, config, store, requireUser, checkSpec } = ctx;

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
      if (!p || p.deletedAt || p.owner !== req.session!.address) return reply.code(404).send({ error: 'Not found' });
      const r = checkSpec(parseStoredSpec(p));
      if (!r.ok) return reply.code(422).send({ error: 'Invalid spec', issues: r.issues });
      try {
        const d = await store.createDeployment({ id: newId(), projectId, owner: p.owner, subdomain, status: 'queued' });
        await store.audit(p.owner, 'deployment.create', d.id, { projectId, subdomain });
        return reply.code(202).send(d);
      } catch (e) {
        // Only a taken subdomain is the caller's problem; anything else is ours (500).
        if (e instanceof SubdomainTakenError) return reply.code(409).send({ error: 'Subdomain is taken' });
        throw e;
      }
    },
  );

  app.get<{ Params: { id: string } }>('/deployments/:id', { preHandler: requireUser, schema: { params: idParams } }, async (req, reply) => {
    if (!config.hostingEnabled) return reply.code(404).send({ error: 'Hosting is not enabled' });
    const d = await store.getDeployment(req.params.id);
    if (!d || d.owner !== req.session!.address) return reply.code(404).send({ error: 'Not found' });
    return d;
  });
}
