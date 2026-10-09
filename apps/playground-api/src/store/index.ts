import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { SqlStore, type SqlDriver } from './sql.js';
import type { Store } from './types.js';

export type { Store } from './types.js';

async function sqliteDriver(file: string): Promise<SqlDriver> {
  const { DatabaseSync } = await import('node:sqlite');
  if (file !== ':memory:') mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;');
  const norm = (params: unknown[]) => params.map((p) => (typeof p === 'boolean' ? Number(p) : p)) as (string | number | null)[];
  return {
    dialect: 'sqlite',
    async exec(sql) {
      db.exec(sql);
    },
    async run(sql, params = []) {
      const r = db.prepare(sql).run(...norm(params));
      return { changes: Number(r.changes) };
    },
    async all(sql, params = []) {
      return db.prepare(sql).all(...norm(params)) as never[];
    },
    async close() {
      db.close();
    },
  };
}

async function postgresDriver(url: string): Promise<SqlDriver> {
  const { default: pg } = await import('pg');
  const pool = new pg.Pool({ connectionString: url, max: 10 });
  const toPg = (sql: string) => {
    let i = 0;
    return sql.replace(/\?/g, () => `$${++i}`);
  };
  return {
    dialect: 'postgres',
    async exec(sql) {
      await pool.query(sql);
    },
    async run(sql, params = []) {
      const r = await pool.query(toPg(sql), params);
      return { changes: r.rowCount ?? 0 };
    },
    async all(sql, params = []) {
      return (await pool.query(toPg(sql), params)).rows as never[];
    },
    async close() {
      await pool.end();
    },
  };
}

/** `sqlite:<file>` / `sqlite::memory:` for development and tests, `postgres://…` for production. */
export async function createStore(databaseUrl: string): Promise<Store> {
  let driver: SqlDriver;
  if (databaseUrl.startsWith('sqlite:')) driver = await sqliteDriver(databaseUrl.slice('sqlite:'.length) || ':memory:');
  else if (/^postgres(ql)?:\/\//.test(databaseUrl)) driver = await postgresDriver(databaseUrl);
  else throw new Error('DATABASE_URL must start with sqlite: or postgres://');
  const store = new SqlStore(driver);
  await store.migrate();
  return store;
}
