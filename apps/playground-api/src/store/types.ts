export type CatalogFlag = {
  hidden?: boolean;
  featured?: boolean;
  status?: 'stable' | 'beta' | 'experimental';
  minVersion?: string;
  updatedAt?: string;
};

export type WorkspaceRow = {
  id: string;
  owner: string;
  name: string;
  createdAt: string;
  /** Set while the workspace is in the trash. */
  deletedAt?: string | null;
};

export type ProjectRow = {
  id: string;
  owner: string;
  name: string;
  /** JSON text of a validated DappSpec. */
  spec: string;
  shareId: string | null;
  workspaceId: string | null;
  createdAt: string;
  updatedAt: string;
  /** Starts at 1 and goes up on every update; saves can require the version they started from. */
  version: number;
  /** Set while the project is in the trash. */
  deletedAt?: string | null;
  /** The share link stops working after this time; null: until revoked. */
  shareExpiresAt?: string | null;
  /** Times the current share link was opened. */
  shareViews?: number;
};

export type ProjectPatch = Partial<Pick<ProjectRow, 'name' | 'spec' | 'workspaceId'>>;

export type ProjectSort = 'updated' | 'name';

export type ListProjectsOptions = {
  /** `undefined`: every workspace; `null`: projects in no workspace. */
  workspaceId?: string | null;
  /** Case-insensitive name filter. */
  q?: string;
  sort?: ProjectSort;
  limit?: number;
  /** Opaque value from the previous page's `nextCursor`. */
  cursor?: string;
};

export type ProjectSummaryRow = Omit<ProjectRow, 'spec'>;

export type RevisionRow = { version: number; savedAt: string };

/** Revisions kept per project; older ones are dropped as new saves come in. */
export const MAX_REVISIONS = 20;

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

export class SubdomainTakenError extends Error {
  constructor() {
    super('Subdomain is taken');
    this.name = 'SubdomainTakenError';
  }
}

/** Thrown when a write would leave the owner with more live projects than `maxProjects`. */
export class ProjectLimitError extends Error {
  constructor(readonly maxProjects: number) {
    super(`Project limit reached (${maxProjects})`);
    this.name = 'ProjectLimitError';
  }
}

/** Persistence used by the API. One SQL implementation runs on SQLite (dev) and Postgres (prod). */
export interface Store {
  /** Applies pending schema migrations in order; safe to run on every start. */
  migrate(): Promise<void>;
  close(): Promise<void>;
  /** Cheap round trip for readiness checks. */
  ping(): Promise<void>;
  /**
   * Deletes expired nonces and sessions, audit rows older than `auditBeforeIso`, and trash older
   * than `trashBeforeIso` (when given). Returns rows removed.
   */
  purgeExpired(
    nowSec: number,
    auditBeforeIso: string,
    trashBeforeIso?: string,
  ): Promise<{ nonces: number; sessions: number; audit: number; trash: number }>;

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

  /** Live workspaces (not in the trash). */
  listWorkspaces(owner: string): Promise<WorkspaceRow[]>;
  createWorkspace(row: Pick<WorkspaceRow, 'id' | 'owner' | 'name'>): Promise<WorkspaceRow>;
  renameWorkspace(id: string, name: string): Promise<WorkspaceRow | null>;
  /** Moves the workspace and its live projects to the trash, together (the owner's oldest trash past the cap is purged). */
  deleteWorkspace(id: string): Promise<boolean>;
  /**
   * Brings a trashed workspace back with the projects that went to the trash with it. With
   * `maxProjects`, refuses (`limit`) when that would leave the owner with more live projects.
   */
  restoreWorkspace(id: string, opts?: { maxProjects?: number }): Promise<'ok' | 'not-found' | 'limit'>;
  /** Deletes a trashed workspace and its trashed projects for good. */
  purgeWorkspace(id: string): Promise<boolean>;
  /** Live projects, newest first unless `sort` says otherwise. */
  listProjects(owner: string, opts?: ListProjectsOptions): Promise<{ projects: ProjectSummaryRow[]; nextCursor: string | null }>;
  /**
   * Trashed projects (a page, most recently deleted first) and, on the first page, trashed
   * workspaces. A bad cursor throws a 400 error.
   */
  listTrash(
    owner: string,
    opts?: { limit?: number; cursor?: string },
  ): Promise<{ projects: ProjectSummaryRow[]; workspaces: WorkspaceRow[]; nextCursor: string | null }>;
  /** A live workspace owned by `owner`; with `includeDeleted`, one in the trash too. */
  getWorkspace(id: string, owner: string, includeDeleted?: boolean): Promise<WorkspaceRow | null>;
  /** Live projects only: the per-user cap does not count the trash. */
  countProjects(owner: string): Promise<number>;
  /** Live or trashed; callers check `deletedAt`. */
  getProject(id: string): Promise<ProjectRow | null>;
  /** A live project with this share id. */
  getProjectByShareId(shareId: string): Promise<ProjectRow | null>;
  /**
   * With `maxProjects`, the count and the insert are one atomic step: throws `ProjectLimitError`
   * when the owner already has that many live projects, even under concurrent requests.
   */
  createProject(
    row: Pick<ProjectRow, 'id' | 'owner' | 'name' | 'spec'> & { workspaceId?: string | null },
    opts?: { maxProjects?: number },
  ): Promise<ProjectRow>;
  /**
   * Applies `patch` and bumps `version`. With `expectedVersion`, only updates a row still at that
   * version. `null`: no such project, or it moved past `expectedVersion`. A new spec is kept as a
   * revision.
   */
  updateProject(id: string, patch: ProjectPatch, expectedVersion?: number): Promise<ProjectRow | null>;
  /** `null` revokes the share link. A new link starts with no views. */
  setShareId(id: string, shareId: string | null, expiresAt?: string | null): Promise<void>;
  recordShareView(id: string): Promise<void>;
  /** Moves a project to the trash; past `MAX_TRASHED_PROJECTS` the owner's oldest trash is purged. */
  deleteProject(id: string): Promise<boolean>;
  /**
   * Brings a trashed project back (out of its workspace if that is still in the trash). With
   * `maxProjects`, throws `ProjectLimitError` atomically like `createProject`.
   */
  restoreProject(id: string, opts?: { maxProjects?: number }): Promise<boolean>;
  /** Deletes a project, its revisions and deployments for good. */
  purgeProject(id: string): Promise<boolean>;
  /** Saved versions, newest first. */
  listRevisions(projectId: string): Promise<RevisionRow[]>;
  /** The spec JSON saved as `version`, or null. */
  getRevision(projectId: string, version: number): Promise<string | null>;

  getFlags(): Promise<Record<string, CatalogFlag>>;
  setFlag(id: string, flag: CatalogFlag | null): Promise<void>;

  audit(actor: string, action: string, target?: string | null, detail?: unknown): Promise<void>;
  listAudit(limit: number): Promise<AuditEntry[]>;
  /** Rows with this action written at or after `sinceIso` (used to cap noisy entries). */
  countAuditSince(action: string, sinceIso: string): Promise<number>;

  /** Throws `SubdomainTakenError` when the subdomain is already used. */
  createDeployment(row: Omit<DeploymentRow, 'createdAt' | 'url'>): Promise<DeploymentRow>;
  getDeployment(id: string): Promise<DeploymentRow | null>;
}
