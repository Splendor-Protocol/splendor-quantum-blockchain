export type CatalogFlag = {
  hidden?: boolean;
  featured?: boolean;
  status?: 'stable' | 'beta' | 'experimental';
  minVersion?: string;
  updatedAt?: string;
};

export type ProjectRow = {
  id: string;
  owner: string;
  name: string;
  /** JSON text of a validated DappSpec. */
  spec: string;
  shareId: string | null;
  createdAt: string;
  updatedAt: string;
};

export type AuditEntry = { id: number; at: string; actor: string; action: string; target: string | null; detail: string | null };

export type DeploymentRow = {
  id: string;
  projectId: string;
  owner: string;
  subdomain: string;
  status: 'queued' | 'building' | 'live' | 'failed';
  url: string | null;
  createdAt: string;
};

/** Persistence used by the API. One SQL implementation runs on SQLite (dev) and Postgres (prod). */
export interface Store {
  /** Applies pending schema migrations in order; safe to run on every start. */
  migrate(): Promise<void>;
  close(): Promise<void>;
  /** Cheap round trip for readiness checks. */
  ping(): Promise<void>;
  /** Deletes expired nonces and sessions, and audit rows older than `auditBeforeIso`. Returns rows removed. */
  purgeExpired(nowSec: number, auditBeforeIso: string): Promise<{ nonces: number; sessions: number; audit: number }>;

  putNonce(nonce: string, expiresAtSec: number): Promise<void>;
  /** Atomic: true only for the first caller with an unexpired nonce. */
  consumeNonce(nonce: string, nowSec: number): Promise<boolean>;

  /** `idHash` is sha256(session id); the raw id only lives in the cookie. */
  createSession(idHash: string, address: string, expiresAtSec: number): Promise<void>;
  getSession(idHash: string, nowSec: number): Promise<{ address: string; expiresAt: number } | null>;
  deleteSession(idHash: string): Promise<void>;

  listAdmins(): Promise<{ address: string; addedBy: string; addedAt: string }[]>;
  isAdmin(address: string): Promise<boolean>;
  addAdmin(address: string, addedBy: string): Promise<void>;
  removeAdmin(address: string): Promise<boolean>;

  listProjects(owner: string): Promise<Omit<ProjectRow, 'spec'>[]>;
  countProjects(owner: string): Promise<number>;
  getProject(id: string): Promise<ProjectRow | null>;
  getProjectByShareId(shareId: string): Promise<ProjectRow | null>;
  createProject(row: Pick<ProjectRow, 'id' | 'owner' | 'name' | 'spec'>): Promise<ProjectRow>;
  updateProject(id: string, patch: Pick<ProjectRow, 'name' | 'spec'>): Promise<ProjectRow | null>;
  setShareId(id: string, shareId: string): Promise<void>;
  deleteProject(id: string): Promise<boolean>;

  getFlags(): Promise<Record<string, CatalogFlag>>;
  setFlag(id: string, flag: CatalogFlag | null): Promise<void>;

  audit(actor: string, action: string, target?: string | null, detail?: unknown): Promise<void>;
  listAudit(limit: number): Promise<AuditEntry[]>;
  /** Rows with this action written at or after `sinceIso` (used to cap noisy entries). */
  countAuditSince(action: string, sinceIso: string): Promise<number>;

  createDeployment(row: Omit<DeploymentRow, 'createdAt' | 'url'>): Promise<DeploymentRow>;
  getDeployment(id: string): Promise<DeploymentRow | null>;
}
