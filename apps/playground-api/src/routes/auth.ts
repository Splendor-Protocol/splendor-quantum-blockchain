import { createHash, randomBytes } from 'node:crypto';
import type { FastifyReply, FastifyRequest } from 'fastify';
import { issueNonce, parseAuthMessage, verifySignIn } from '@splendor/sqc-sdk/auth';
import { canonicalAddress } from '../config.js';
import { ADDRESS, obj, str, type RouteContext, type Session } from './context.js';

const NONCE_TTL_SEC = 300;
/** Failed sign-ins are audited, but an attacker must not be able to grow the table without bound. */
const MAX_FAILED_AUTH_AUDITS_PER_MINUTE = 60;

export const sha256 = (s: string) => createHash('sha256').update(s).digest('hex');

export function registerAuthRoutes(ctx: RouteContext) {
  const { app, config, store, nonceStore, cookieOptions, isAdmin } = ctx;

  async function startSession(req: FastifyRequest, reply: FastifyReply, address: string): Promise<Session> {
    // Signing in again replaces the session this browser had, rather than leaving it valid.
    const previous = req.cookies[config.cookieName];
    if (previous) await store.deleteSession(sha256(previous));
    const id = randomBytes(32).toString('base64url');
    await store.createSession(sha256(id), address, Math.floor(Date.now() / 1000) + config.sessionTtlSec);
    reply.setCookie(config.cookieName, id, { ...cookieOptions(), maxAge: config.sessionTtlSec });
    return { address, isAdmin: await isAdmin(address) };
  }

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
        ...(ctx.verifier ? { verifier: ctx.verifier } : {}),
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
    if (req.session) await ctx.audit(req, req.session.address, 'auth.logout');
    reply.clearCookie(config.cookieName, cookieOptions());
    return { ok: true };
  });
}
