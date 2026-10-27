import {
  MAX_REVISIONS,
  SubdomainTakenError,
  type AuditEntry,
  type CatalogFlag,
  type DeploymentRow,
  type ListProjectsOptions,
  type ProjectPatch,
  type ProjectRow,
  type ProjectSummaryRow,
  type Store,
  type WorkspaceRow,
} from './types.js';

type Row = Record<string, unknown>;

/** Statements inside a transaction. */
export type SqlTx = {
  run(sql: string, params?: unknown[]): Promise<{ changes: number }>;
  all<T = Row>(sql: string, params?: unknown[]): Promise<T[]>;
};

/** Minimal driver surface. SQL uses `?` placeholders; drivers translate as needed. */
export interface SqlDriver extends SqlTx {
  dialect: 'sqlite' | 'postgres';
  exec(sql: string): Promise<void>;
  /** Runs a multi-statement script all-or-nothing. */
  execAtomic(sql: string): Promise<void>;
  /** Runs `fn` in one transaction: it commits when `fn` resolves and rolls back when it throws. */
  transaction<T>(fn: (tx: SqlTx) => Promise<T>): Promise<T>;
  close(): Promise<void>;
}

const MAX_PAGE = 100;
/** Columns of a project list row (no spec). */
const SUMMARY_COLS = 'id, owner, name, share_id, workspace_id, created_at, updated_at, version, deleted_at, share_expires_at, share_views';

/** Cursor for keyset pages: the sort key and id of the last row, base64url JSON. */
function encodeCursor(key: string, id: string): string {
  return Buffer.from(JSON.stringify([key, id])).toString('base64url');
}
function decodeCursor(cursor: string | undefined): [string, string] | null {
  if (!cursor) return null;
  try {
    const v = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8')) as unknown;
    return Array.isArray(v) && v.length === 2 && typeof v[0] === 'string' && typeof v[1] === 'string' ? [v[0], v[1]] : null;
  } catch {
    return null;
  }
}

const isUniqueViolation = (e: unknown) =>
  (typeof e === 'object' && e !== null && (e as { code?: unknown }).code === '23505') || /UNIQUE constraint failed/i.test(String((e as Error)?.message ?? e));

const now = () => new Date().toISOString();

const INITIAL_SCHEMA = (d: SqlDriver['dialect']) => `
CREATE TABLE IF NOT EXISTS nonces (
  nonce TEXT PRIMARY KEY,
  expires_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id_hash TEXT PRIMARY KEY,
  address TEXT NOT NULL,
  expires_at BIGINT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admins (
  address TEXT PRIMARY KEY,
  added_by TEXT NOT NULL,
  added_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  name TEXT NOT NULL,
  spec TEXT NOT NULL,
  share_id TEXT UNIQUE,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS projects_owner ON projects (owner);
CREATE TABLE IF NOT EXISTS catalog_flags (
  id TEXT PRIMARY KEY,
  flag TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_log (
  id ${d === 'postgres' ? 'BIGSERIAL PRIMARY KEY' : 'INTEGER PRIMARY KEY AUTOINCREMENT'},
  at TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT,
  detail TEXT
);
CREATE TABLE IF NOT EXISTS deployments (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  owner TEXT NOT NULL,
  subdomain TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  url TEXT,
  created_at TEXT NOT NULL
);
`;

const canonical = (col: string) => `'SQ:' || lower(substr(${col}, 4))`;

/**
 * Append-only: never edit a migration that has shipped, add a new one. Each runs in one
 * transaction together with its schema_migrations row.
 */
const MIGRATIONS: { id: number; name: string; sql: (d: SqlDriver['dialect']) => string }[] = [
  { id: 1, name: 'initial', sql: INITIAL_SCHEMA },
  {
    id: 2,
    name: 'indexes-and-canonical-addresses',
    sql: () => `
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS sessions_address ON sessions (address);
CREATE INDEX IF NOT EXISTS nonces_expires ON nonces (expires_at);
CREATE INDEX IF NOT EXISTS audit_log_at ON audit_log (at);
CREATE INDEX IF NOT EXISTS audit_log_action_at ON audit_log (action, at);
UPDATE admins SET address = ${canonical('address')} WHERE address <> ${canonical('address')};
UPDATE sessions SET address = ${canonical('address')} WHERE address <> ${canonical('address')};
UPDATE projects SET owner = ${canonical('owner')} WHERE owner <> ${canonical('owner')};
UPDATE deployments SET owner = ${canonical('owner')} WHERE owner <> ${canonical('owner')};
`,
  },
  {
    id: 3,
    name: 'workspaces',
    sql: () => `
CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS workspaces_owner ON workspaces (owner);
ALTER TABLE projects ADD COLUMN workspace_id TEXT;
CREATE INDEX IF NOT EXISTS projects_workspace ON projects (workspace_id);
`,
  },
  {
    id: 4,
    name: 'project-versions',
    sql: () => `
ALTER TABLE projects ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
`,
  },
  {
    id: 5,
    name: 'revisions-trash-share-expiry',
    sql: () => `
CREATE TABLE IF NOT EXISTS project_revisions (
  project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  spec TEXT NOT NULL,
  saved_at TEXT NOT NULL,
  PRIMARY KEY (project_id, version)
);
INSERT INTO project_revisions (project_id, version, spec, saved_at) SELECT id, version, spec, updated_at FROM projects;
ALTER TABLE projects ADD COLUMN deleted_at TEXT;
ALTER TABLE workspaces ADD COLUMN deleted_at TEXT;
ALTER TABLE projects ADD COLUMN share_expires_at TEXT;
ALTER TABLE projects ADD COLUMN share_views INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS projects_owner_updated ON projects (owner, updated_at);
CREATE INDEX IF NOT EXISTS projects_deleted ON projects (deleted_at);
CREATE INDEX IF NOT EXISTS workspaces_deleted ON workspaces (deleted_at);
`,
  },
];

export const SCHEMA_VERSION = MIGRATIONS[MIGRATIONS.length - 1]!.id;

function project(r: Row): ProjectRow {
  return {
    id: String(r.id),
    owner: String(r.owner),
    name: String(r.name),
    spec: String(r.spec ?? ''),
    shareId: r.share_id == null ? null : String(r.share_id),
    workspaceId: r.workspace_id == null ? null : String(r.workspace_id),
    createdAt: String(r.created_at),
    updatedAt: String(r.updated_at),
    version: r.version == null ? 1 : Number(r.version),
    deletedAt: r.deleted_at == null ? null : String(r.deleted_at),
    shareExpiresAt: r.share_expires_at == null ? null : String(r.share_expires_at),
    shareViews: r.share_views == null ? 0 : Number(r.share_views),
  };
}

function summaryRow(r: Row): ProjectSummaryRow {
  const { spec: _s, ...rest } = project(r);
  return rest;
}

function workspace(r: Row): WorkspaceRow {
  return {
    id: String(r.id),
    owner: String(r.owner),
    name: String(r.name),
    createdAt: String(r.created_at),
    deletedAt: r.deleted_at == null ? null : String(r.deleted_at),
  };
}

function deployment(r: Row): DeploymentRow {
  return {
    id: String(r.id),
    projectId: String(r.project_id),
    owner: String(r.owner),
    subdomain: String(r.subdomain),
    status: String(r.status) as DeploymentRow['status'],
    url: r.url == null ? null : String(r.url),
    createdAt: String(r.created_at),
  };
}

export class SqlStore implements Store {
  constructor(private readonly db: SqlDriver) {}

  async migrate() {
    await this.db.exec('CREATE TABLE IF NOT EXISTS schema_migrations (id INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)');
    const done = new Set((await this.db.all('SELECT id FROM schema_migrations')).map((r) => Number(r.id)));
    for (const m of MIGRATIONS) {
      if (done.has(m.id)) continue;
      // Literals only (no user input), so the row can ride in the same script as the migration.
      await this.db.execAtomic(`${m.sql(this.db.dialect)}\nINSERT INTO schema_migrations (id, name, applied_at) VALUES (${m.id}, '${m.name}', '${now()}');`);
    }
  }
  close() {
    return this.db.close();
  }
  async ping() {
    await this.db.all('SELECT 1 AS ok');
  }
  async purgeExpired(nowSec: number, auditBeforeIso: string, trashBeforeIso?: string) {
    const nonces = (await this.db.run('DELETE FROM nonces WHERE expires_at <= ?', [nowSec])).changes;
    const sessions = (await this.db.run('DELETE FROM sessions WHERE expires_at <= ?', [nowSec])).changes;
    const audit = (await this.db.run('DELETE FROM audit_log WHERE at < ?', [auditBeforeIso])).changes;
    let trash = 0;
    if (trashBeforeIso) {
      trash = await this.db.transaction(async (tx) => {
        const old = 'deleted_at IS NOT NULL AND deleted_at < ?';
        await tx.run(`DELETE FROM deployments WHERE project_id IN (SELECT id FROM projects WHERE ${old})`, [trashBeforeIso]);
        await tx.run(`DELETE FROM project_revisions WHERE project_id IN (SELECT id FROM projects WHERE ${old})`, [trashBeforeIso]);
        const p = (await tx.run(`DELETE FROM projects WHERE ${old}`, [trashBeforeIso])).changes;
        const w = (await tx.run(`DELETE FROM workspaces WHERE ${old}`, [trashBeforeIso])).changes;
        return p + w;
      });
    }
    return { nonces, sessions, audit, trash };
  }

  async putNonce(nonce: string, expiresAtSec: number) {
    await this.db.run('DELETE FROM nonces WHERE expires_at <= ?', [Math.floor(Date.now() / 1000)]);
    await this.db.run('INSERT INTO nonces (nonce, expires_at) VALUES (?, ?)', [nonce, expiresAtSec]);
  }
  async consumeNonce(nonce: string, nowSec: number) {
    const rows = await this.db.all('DELETE FROM nonces WHERE nonce = ? AND expires_at > ? RETURNING nonce', [nonce, nowSec]);
    return rows.length === 1;
  }

  async createSession(idHash: string, address: string, expiresAtSec: number) {
    await this.db.run('DELETE FROM sessions WHERE expires_at <= ?', [Math.floor(Date.now() / 1000)]);
    await this.db.run('INSERT INTO sessions (id_hash, address, expires_at, created_at) VALUES (?, ?, ?, ?)', [idHash, address, expiresAtSec, now()]);
  }
  async getSession(idHash: string, nowSec: number) {
    const [r] = await this.db.all('SELECT address, expires_at FROM sessions WHERE id_hash = ? AND expires_at > ?', [idHash, nowSec]);
    return r ? { address: String(r.address), expiresAt: Number(r.expires_at) } : null;
  }
  async deleteSession(idHash: string) {
    await this.db.run('DELETE FROM sessions WHERE id_hash = ?', [idHash]);
  }

  async listAdmins() {
    const rows = await this.db.all('SELECT address, added_by, added_at FROM admins ORDER BY added_at');
    return rows.map((r) => ({ address: String(r.address), addedBy: String(r.added_by), addedAt: String(r.added_at) }));
  }
  // Addresses arrive canonical (`SQ:` + lowercase hex) from the API layer.
  async isAdmin(address: string) {
    return (await this.db.all('SELECT 1 FROM admins WHERE address = ?', [address])).length > 0;
  }
  async addAdmin(address: string, addedBy: string) {
    await this.db.run('INSERT INTO admins (address, added_by, added_at) VALUES (?, ?, ?) ON CONFLICT (address) DO NOTHING', [address, addedBy, now()]);
  }
  async removeAdmin(address: string) {
    return (await this.db.run('DELETE FROM admins WHERE address = ?', [address])).changes > 0;
  }

  async listWorkspaces(owner: string) {
    const rows = await this.db.all('SELECT id, owner, name, created_at, deleted_at FROM workspaces WHERE owner = ? AND deleted_at IS NULL ORDER BY created_at DESC', [owner]);
    return rows.map(workspace);
  }
  async createWorkspace(row: Pick<WorkspaceRow, 'id' | 'owner' | 'name'>) {
    const t = now();
    await this.db.run('INSERT INTO workspaces (id, owner, name, created_at) VALUES (?, ?, ?, ?)', [row.id, row.owner, row.name, t]);
    return { ...row, createdAt: t };
  }
  async renameWorkspace(id: string, name: string) {
    const res = await this.db.run('UPDATE workspaces SET name = ? WHERE id = ? AND deleted_at IS NULL', [name, id]);
    if (!res.changes) return null;
    const [r] = await this.db.all('SELECT id, owner, name, created_at, deleted_at FROM workspaces WHERE id = ?', [id]);
    return r ? workspace(r) : null;
  }
  async deleteWorkspace(id: string) {
    const t = now();
    // The projects share the workspace's timestamp, which is how restore finds them again.
    return this.db.transaction(async (tx) => {
      const res = await tx.run('UPDATE workspaces SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL', [t, id]);
      if (!res.changes) return false;
      await tx.run('UPDATE projects SET deleted_at = ? WHERE workspace_id = ? AND deleted_at IS NULL', [t, id]);
      return true;
    });
  }
  async restoreWorkspace(id: string) {
    return this.db.transaction(async (tx) => {
      const [r] = await tx.all('SELECT deleted_at FROM workspaces WHERE id = ? AND deleted_at IS NOT NULL', [id]);
      if (!r) return false;
      await tx.run('UPDATE projects SET deleted_at = NULL WHERE workspace_id = ? AND deleted_at = ?', [id, r.deleted_at]);
      await tx.run('UPDATE workspaces SET deleted_at = NULL WHERE id = ?', [id]);
      return true;
    });
  }
  async purgeWorkspace(id: string) {
    return this.db.transaction(async (tx) => {
      const [r] = await tx.all('SELECT 1 AS ok FROM workspaces WHERE id = ? AND deleted_at IS NOT NULL', [id]);
      if (!r) return false;
      const trashed = 'SELECT id FROM projects WHERE workspace_id = ? AND deleted_at IS NOT NULL';
      await tx.run(`DELETE FROM deployments WHERE project_id IN (${trashed})`, [id]);
      await tx.run(`DELETE FROM project_revisions WHERE project_id IN (${trashed})`, [id]);
      await tx.run('DELETE FROM projects WHERE workspace_id = ? AND deleted_at IS NOT NULL', [id]);
      await tx.run('DELETE FROM workspaces WHERE id = ?', [id]);
      return true;
    });
  }
  async getWorkspace(id: string, owner: string, includeDeleted = false) {
    const [r] = await this.db.all(
      `SELECT id, owner, name, created_at, deleted_at FROM workspaces WHERE id = ? AND owner = ?${includeDeleted ? '' : ' AND deleted_at IS NULL'}`,
      [id, owner],
    );
    return r ? workspace(r) : null;
  }
  async listProjects(owner: string, opts: ListProjectsOptions = {}) {
    const where = ['owner = ?', 'deleted_at IS NULL'];
    const args: unknown[] = [owner];
    if (opts.workspaceId === null) where.push('workspace_id IS NULL');
    else if (opts.workspaceId !== undefined) {
      where.push('workspace_id = ?');
      args.push(opts.workspaceId);
    }
    const q = opts.q?.trim().toLowerCase();
    if (q) {
      where.push("lower(name) LIKE ? ESCAPE '\\'");
      args.push(`%${q.replace(/[\\%_]/g, (c) => `\\${c}`)}%`);
    }
    // Keyset pages: a stable order (sort key, then id) and "after the last row seen".
    const byName = opts.sort === 'name';
    const key = byName ? 'lower(name)' : 'updated_at';
    const after = decodeCursor(opts.cursor);
    if (after) {
      where.push(byName ? `(${key} > ? OR (${key} = ? AND id > ?))` : `(${key} < ? OR (${key} = ? AND id < ?))`);
      args.push(after[0], after[0], after[1]);
    }
    const limit = Math.min(MAX_PAGE, Math.max(1, opts.limit ?? MAX_PAGE));
    const order = byName ? `${key} ASC, id ASC` : `${key} DESC, id DESC`;
    const rows = await this.db.all(`SELECT ${SUMMARY_COLS} FROM projects WHERE ${where.join(' AND ')} ORDER BY ${order} LIMIT ?`, [...args, limit + 1]);
    const page = rows.slice(0, limit).map(summaryRow);
    const last = page[page.length - 1];
    const nextCursor = rows.length > limit && last ? encodeCursor(byName ? last.name.toLowerCase() : last.updatedAt, last.id) : null;
    return { projects: page, nextCursor };
  }
  async listTrash(owner: string) {
    const projects = await this.db.all(
      `SELECT ${SUMMARY_COLS} FROM projects WHERE owner = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC, id DESC LIMIT ?`,
      [owner, MAX_PAGE * 2],
    );
    const workspaces = await this.db.all(
      'SELECT id, owner, name, created_at, deleted_at FROM workspaces WHERE owner = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC',
      [owner],
    );
    return { projects: projects.map(summaryRow), workspaces: workspaces.map(workspace) };
  }
  async countProjects(owner: string) {
    const [r] = await this.db.all('SELECT COUNT(*) AS n FROM projects WHERE owner = ? AND deleted_at IS NULL', [owner]);
    return Number(r?.n ?? 0);
  }
  async getProject(id: string) {
    const [r] = await this.db.all('SELECT * FROM projects WHERE id = ?', [id]);
    return r ? project(r) : null;
  }
  async getProjectByShareId(shareId: string) {
    const [r] = await this.db.all('SELECT * FROM projects WHERE share_id = ? AND deleted_at IS NULL', [shareId]);
    return r ? project(r) : null;
  }
  async createProject(row: Pick<ProjectRow, 'id' | 'owner' | 'name' | 'spec'> & { workspaceId?: string | null }) {
    const t = now();
    const workspaceId = row.workspaceId ?? null;
    await this.db.transaction(async (tx) => {
      await tx.run('INSERT INTO projects (id, owner, name, spec, share_id, workspace_id, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, ?, ?, ?)', [
        row.id,
        row.owner,
        row.name,
        row.spec,
        workspaceId,
        t,
        t,
      ]);
      await tx.run('INSERT INTO project_revisions (project_id, version, spec, saved_at) VALUES (?, 1, ?, ?)', [row.id, row.spec, t]);
    });
    return {
      id: row.id,
      owner: row.owner,
      name: row.name,
      spec: row.spec,
      shareId: null,
      workspaceId,
      createdAt: t,
      updatedAt: t,
      version: 1,
      deletedAt: null,
      shareExpiresAt: null,
      shareViews: 0,
    };
  }
  async updateProject(id: string, patch: ProjectPatch, expectedVersion?: number) {
    const sets: string[] = [];
    const args: unknown[] = [];
    const set = (column: string, value: unknown) => {
      sets.push(`${column} = ?`);
      args.push(value);
    };
    if (patch.name !== undefined) set('name', patch.name);
    if (patch.spec !== undefined) set('spec', patch.spec);
    if (patch.workspaceId !== undefined) set('workspace_id', patch.workspaceId);
    const t = now();
    sets.push('updated_at = ?', 'version = version + 1');
    args.push(t, id);
    let where = 'id = ? AND deleted_at IS NULL';
    if (expectedVersion !== undefined) {
      where += ' AND version = ?';
      args.push(expectedVersion);
    }
    // The version check, the new row and its revision are one write.
    const row = await this.db.transaction(async (tx) => {
      const res = await tx.run(`UPDATE projects SET ${sets.join(', ')} WHERE ${where}`, args);
      if (!res.changes) return null;
      const [r] = await tx.all('SELECT * FROM projects WHERE id = ?', [id]);
      if (!r) return null;
      const updated = project(r);
      // A move between workspaces bumps the version but saves no new content.
      if (patch.spec !== undefined) {
        await tx.run('INSERT INTO project_revisions (project_id, version, spec, saved_at) VALUES (?, ?, ?, ?)', [id, updated.version, updated.spec, t]);
        await tx.run('DELETE FROM project_revisions WHERE project_id = ? AND version <= ?', [id, updated.version - MAX_REVISIONS]);
      }
      return updated;
    });
    return row;
  }
  async setShareId(id: string, shareId: string | null, expiresAt: string | null = null) {
    await this.db.run('UPDATE projects SET share_id = ?, share_expires_at = ?, share_views = 0 WHERE id = ?', [shareId, shareId ? expiresAt : null, id]);
  }
  async recordShareView(id: string) {
    await this.db.run('UPDATE projects SET share_views = share_views + 1 WHERE id = ?', [id]);
  }
  async deleteProject(id: string) {
    return (await this.db.run('UPDATE projects SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL', [now(), id])).changes > 0;
  }
  async restoreProject(id: string) {
    return this.db.transaction(async (tx) => {
      const res = await tx.run('UPDATE projects SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL', [id]);
      if (!res.changes) return false;
      // Its workspace may still be in the trash: then it comes back without one.
      await tx.run(
        'UPDATE projects SET workspace_id = NULL WHERE id = ? AND workspace_id IN (SELECT id FROM workspaces WHERE deleted_at IS NOT NULL)',
        [id],
      );
      return true;
    });
  }
  async purgeProject(id: string) {
    return this.db.transaction(async (tx) => {
      await tx.run('DELETE FROM deployments WHERE project_id = ?', [id]);
      await tx.run('DELETE FROM project_revisions WHERE project_id = ?', [id]);
      return (await tx.run('DELETE FROM projects WHERE id = ?', [id])).changes > 0;
    });
  }
  async listRevisions(projectId: string) {
    const rows = await this.db.all('SELECT version, saved_at FROM project_revisions WHERE project_id = ? ORDER BY version DESC', [projectId]);
    return rows.map((r) => ({ version: Number(r.version), savedAt: String(r.saved_at) }));
  }
  async getRevision(projectId: string, version: number) {
    const [r] = await this.db.all('SELECT spec FROM project_revisions WHERE project_id = ? AND version = ?', [projectId, version]);
    return r ? String(r.spec) : null;
  }

  async getFlags() {
    const rows = await this.db.all('SELECT id, flag, updated_at FROM catalog_flags');
    const out: Record<string, CatalogFlag> = {};
    for (const r of rows) {
      try {
        out[String(r.id)] = { ...(JSON.parse(String(r.flag)) as CatalogFlag), updatedAt: String(r.updated_at) };
      } catch {
        /* skip corrupt row */
      }
    }
    return out;
  }
  async setFlag(id: string, flag: CatalogFlag | null) {
    if (!flag) {
      await this.db.run('DELETE FROM catalog_flags WHERE id = ?', [id]);
      return;
    }
    const { updatedAt: _u, ...data } = flag;
    await this.db.run(
      'INSERT INTO catalog_flags (id, flag, updated_at) VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET flag = excluded.flag, updated_at = excluded.updated_at',
      [id, JSON.stringify(data), now()],
    );
  }

  async audit(actor: string, action: string, target: string | null = null, detail?: unknown) {
    await this.db.run('INSERT INTO audit_log (at, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)', [
      now(),
      actor,
      action,
      target,
      detail === undefined ? null : JSON.stringify(detail),
    ]);
  }
  async listAudit(limit: number) {
    const rows = await this.db.all('SELECT id, at, actor, action, target, detail FROM audit_log ORDER BY id DESC LIMIT ?', [limit]);
    return rows.map(
      (r): AuditEntry => ({
        id: Number(r.id),
        at: String(r.at),
        actor: String(r.actor),
        action: String(r.action),
        target: r.target == null ? null : String(r.target),
        detail: r.detail == null ? null : String(r.detail),
      }),
    );
  }

  async countAuditSince(action: string, sinceIso: string) {
    const [r] = await this.db.all('SELECT COUNT(*) AS n FROM audit_log WHERE action = ? AND at >= ?', [action, sinceIso]);
    return Number(r?.n ?? 0);
  }

  async createDeployment(row: Omit<DeploymentRow, 'createdAt' | 'url'>) {
    const t = now();
    try {
      await this.db.run('INSERT INTO deployments (id, project_id, owner, subdomain, status, url, created_at) VALUES (?, ?, ?, ?, ?, NULL, ?)', [
        row.id,
        row.projectId,
        row.owner,
        row.subdomain,
        row.status,
        t,
      ]);
    } catch (e) {
      if (isUniqueViolation(e)) throw new SubdomainTakenError();
      throw e;
    }
    return { ...row, url: null, createdAt: t };
  }
  async getDeployment(id: string) {
    const [r] = await this.db.all('SELECT * FROM deployments WHERE id = ?', [id]);
    return r ? deployment(r) : null;
  }
}
