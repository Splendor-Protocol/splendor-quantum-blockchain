import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { SqlStore, type SqlDriver, type SqlTx } from './sql.js';
import type { Store } from './types.js';

export type { Store } from './types.js';

export type StoreOptions = {
  /** Errors the driver reports outside a query (e.g. a Postgres idle client dropping). */
  onError?: (err: Error) => void;
};

async function sqliteDriver(file: string): Promise<SqlDriver> {
  const { DatabaseSync } = await import('node:sqlite');
  if (file !== ':memory:') mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;');
  const norm = (params: unknown[]) => params.map((p) => (typeof p === 'boolean' ? Number(p) : p)) as (string | number | null)[];
  const run = async (sql: string, params: unknown[] = []) => ({ changes: Number(db.prepare(sql).run(...norm(params)).changes) });
  const all = async (sql: string, params: unknown[] = []) => db.prepare(sql).all(...norm(params)) as never[];
  // One connection: transactions take turns, so two of them never share a BEGIN.
  let queue: Promise<unknown> = Promise.resolve();
  return {
    dialect: 'sqlite',
    transaction<T>(fn: (tx: SqlTx) => Promise<T>): Promise<T> {
      const next = queue.then(async () => {
        db.exec('BEGIN');
        try {
          const out = await fn({ run, all });
          db.exec('COMMIT');
          return out;
        } catch (e) {
          db.exec('ROLLBACK');
          throw e;
        }
      });
      queue = next.catch(() => undefined);
      return next;
    },
    async exec(sql) {
      db.exec(sql);
    },
    async execAtomic(sql) {
      db.exec('BEGIN');
      try {
        db.exec(sql);
        db.exec('COMMIT');
      } catch (e) {
        db.exec('ROLLBACK');
        throw e;
      }
    },
    run,
    all,
    async close() {
      db.close();
    },
  };
}

async function postgresDriver(url: string, opts: StoreOptions): Promise<SqlDriver> {
  const { default: pg } = await import('pg');
  const pool = new pg.Pool({
    connectionString: url,
    max: 10,
    connectionTimeoutMillis: 5_000,
    idleTimeoutMillis: 30_000,
    statement_timeout: 10_000,
    query_timeout: 15_000,
  });
  // Without a listener, an idle client's network error is an unhandled 'error' event and kills the process.
  pool.on('error', (err) => (opts.onError ?? ((e) => console.error('[db]', e)))(err));
  const toPg = (sql: string) => {
    let i = 0;
    return sql.replace(/\?/g, () => `$${++i}`);
  };
  return {
    dialect: 'postgres',
    async transaction<T>(fn: (tx: SqlTx) => Promise<T>): Promise<T> {
      const client = await pool.connect();
      try {
        await client.query('BEGIN');
        const out = await fn({
          run: async (sql, params = []) => ({ changes: (await client.query(toPg(sql), params)).rowCount ?? 0 }),
          all: async (sql, params = []) => (await client.query(toPg(sql), params)).rows as never[],
        });
        await client.query('COMMIT');
        return out;
      } catch (e) {
        await client.query('ROLLBACK').catch(() => undefined);
        throw e;
      } finally {
        client.release();
      }
    },
    async exec(sql) {
      await pool.query(sql);
    },
    async execAtomic(sql) {
      // A parameterless multi-statement query runs as one implicit transaction in Postgres.
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
export async function createStore(databaseUrl: string, opts: StoreOptions = {}): Promise<Store> {
  let driver: SqlDriver;
  if (databaseUrl.startsWith('sqlite:')) driver = await sqliteDriver(databaseUrl.slice('sqlite:'.length) || ':memory:');
  else if (/^postgres(ql)?:\/\//.test(databaseUrl)) driver = await postgresDriver(databaseUrl, opts);
  else throw new Error('DATABASE_URL must start with sqlite: or postgres://');
  const store = new SqlStore(driver);
  await store.migrate();
  return store;
}
