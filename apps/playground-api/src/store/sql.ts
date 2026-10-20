import type { AuditEntry, CatalogFlag, DeploymentRow, ProjectRow, Store } from './types.js';

type Row = Record<string, unknown>;

/** Minimal driver surface. SQL uses `?` placeholders; drivers translate as needed. */
export interface SqlDriver {
  dialect: 'sqlite' | 'postgres';
  exec(sql: string): Promise<void>;
  /** Runs a multi-statement script all-or-nothing. */
  execAtomic(sql: string): Promise<void>;
  run(sql: string, params?: unknown[]): Promise<{ changes: number }>;
  all<T = Row>(sql: string, params?: unknown[]): Promise<T[]>;
  close(): Promise<void>;
}

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
];

export const SCHEMA_VERSION = MIGRATIONS[MIGRATIONS.length - 1]!.id;

function project(r: Row): ProjectRow {
  return {
    id: String(r.id),
    owner: String(r.owner),
    name: String(r.name),
    spec: String(r.spec ?? ''),
    shareId: r.share_id == null ? null : String(r.share_id),
    createdAt: String(r.created_at),
    updatedAt: String(r.updated_at),
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
  async purgeExpired(nowSec: number, auditBeforeIso: string) {
    const nonces = (await this.db.run('DELETE FROM nonces WHERE expires_at <= ?', [nowSec])).changes;
    const sessions = (await this.db.run('DELETE FROM sessions WHERE expires_at <= ?', [nowSec])).changes;
    const audit = (await this.db.run('DELETE FROM audit_log WHERE at < ?', [auditBeforeIso])).changes;
    return { nonces, sessions, audit };
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

  async listProjects(owner: string) {
    const rows = await this.db.all('SELECT id, owner, name, share_id, created_at, updated_at FROM projects WHERE owner = ? ORDER BY updated_at DESC', [owner]);
    return rows.map((r) => {
      const { spec: _s, ...rest } = project(r);
      return rest;
    });
  }
  async countProjects(owner: string) {
    const [r] = await this.db.all('SELECT COUNT(*) AS n FROM projects WHERE owner = ?', [owner]);
    return Number(r?.n ?? 0);
  }
  async getProject(id: string) {
    const [r] = await this.db.all('SELECT * FROM projects WHERE id = ?', [id]);
    return r ? project(r) : null;
  }
  async getProjectByShareId(shareId: string) {
    const [r] = await this.db.all('SELECT * FROM projects WHERE share_id = ?', [shareId]);
    return r ? project(r) : null;
  }
  async createProject(row: Pick<ProjectRow, 'id' | 'owner' | 'name' | 'spec'>) {
    const t = now();
    await this.db.run('INSERT INTO projects (id, owner, name, spec, share_id, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, ?, ?)', [row.id, row.owner, row.name, row.spec, t, t]);
    return { ...row, shareId: null, createdAt: t, updatedAt: t };
  }
  async updateProject(id: string, patch: Pick<ProjectRow, 'name' | 'spec'>) {
    const res = await this.db.run('UPDATE projects SET name = ?, spec = ?, updated_at = ? WHERE id = ?', [patch.name, patch.spec, now(), id]);
    return res.changes ? this.getProject(id) : null;
  }
  async setShareId(id: string, shareId: string | null) {
    await this.db.run('UPDATE projects SET share_id = ? WHERE id = ?', [shareId, id]);
  }
  async deleteProject(id: string) {
    return (await this.db.run('DELETE FROM projects WHERE id = ?', [id])).changes > 0;
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
    await this.db.run('INSERT INTO deployments (id, project_id, owner, subdomain, status, url, created_at) VALUES (?, ?, ?, ?, ?, NULL, ?)', [
      row.id,
      row.projectId,
      row.owner,
      row.subdomain,
      row.status,
      t,
    ]);
    return { ...row, url: null, createdAt: t };
  }
  async getDeployment(id: string) {
    const [r] = await this.db.all('SELECT * FROM deployments WHERE id = ?', [id]);
    return r ? deployment(r) : null;
  }
}
