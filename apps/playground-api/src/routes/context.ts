import { randomBytes } from 'node:crypto';
import type { FastifyInstance, FastifyReply, FastifyRequest } from 'fastify';
import type { CookieSerializeOptions } from '@fastify/cookie';
import type { createCatalog, DappSpec } from '@splendor/dapp-spec';
import type { NonceStore } from '@splendor/sqc-sdk/auth';
import type { ApiConfig } from '../config.js';
import type { ReleaseService } from '../releases.js';
import type { ProjectRow, ProjectSummaryRow, Store } from '../store/types.js';

export type Session = { address: string; isAdmin: boolean };

declare module 'fastify' {
  interface FastifyRequest {
    session: Session | null;
  }
}

export const ADDRESS = '^SQ:[0-9a-fA-F]{64}$';
export const ID_RE = /^[A-Za-z0-9_-]{10,40}$/;
export const ID = '^[A-Za-z0-9_-]{10,40}$';
export const MAX_PROJECTS_PER_USER = 100;

export const newId = (bytes = 12) => randomBytes(bytes).toString('base64url');

// ---- request schemas: shape and size only; spec content goes through validateSpec ----
export const obj = (properties: Record<string, object> = {}, required: string[] = []) => ({ type: 'object', additionalProperties: false, properties, required });
export const str = (maxLength: number, pattern?: string) => ({ type: 'string', maxLength, ...(pattern ? { pattern } : {}) });
export const idParams = obj({ id: str(40, ID) }, ['id']);

/** Everything the route modules share; built once per app in `buildApp`. */
export type RouteContext = {
  app: FastifyInstance;
  config: ApiConfig;
  store: Store;
  catalog: ReturnType<typeof createCatalog>;
  releases: ReleaseService;
  verifier?: (publicKeyB64: string, message: string, signatureB64: string) => Promise<boolean>;
  nonceStore: NonceStore;
  requireUser: (req: FastifyRequest, reply: FastifyReply) => Promise<unknown>;
  requireAdmin: (req: FastifyRequest, reply: FastifyReply) => Promise<unknown>;
  isAdmin: (address: string) => Promise<boolean>;
  cookieOptions: () => CookieSerializeOptions;
  checkSpec: (input: unknown) => { ok: true; spec: DappSpec } | { ok: false; issues: { path: string; message: string }[] };
  /** The signed-in user's live project, or null after sending 404. */
  ownedProject: (req: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply, opts?: { includeDeleted?: boolean }) => Promise<ProjectRow | null>;
  ownsWorkspace: (owner: string, id: string) => Promise<boolean>;
  /** Audit, best effort: a failed audit write never fails the request. */
  audit: (req: FastifyRequest, actor: string, action: string, target?: string | null, detail?: unknown) => Promise<void>;
};

/** The public shape of a project in lists and responses (no spec). */
export function summary(p: ProjectSummaryRow | ProjectRow) {
  return {
    id: p.id,
    name: p.name,
    updatedAt: p.updatedAt,
    shareId: p.shareId,
    workspaceId: p.workspaceId ?? null,
    version: p.version,
    ...(p.shareId ? { shareExpiresAt: p.shareExpiresAt ?? null, shareViews: p.shareViews ?? 0 } : {}),
    ...(p.deletedAt ? { deletedAt: p.deletedAt } : {}),
  };
}

/** A stored spec that no longer parses is a server fault, reported without its text. */
export function parseStoredSpec(p: Pick<ProjectRow, 'id' | 'spec'>): DappSpec {
  try {
    return JSON.parse(p.spec) as DappSpec;
  } catch {
    throw Object.assign(new Error(`stored spec of ${p.id} is not valid JSON`), { statusCode: 500 });
  }
}
