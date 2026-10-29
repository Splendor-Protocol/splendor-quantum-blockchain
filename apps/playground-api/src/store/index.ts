import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { SqlStore, type SqlDriver, type SqlTx } from './sql.js';
import type { Store } from './types.js';

export type { Store } from './types.js';

export type StoreOptions = {
  /** Errors the driver reports outside a query (e.g. a Postgres idle client dropping). */
  onError?: (err: Error) => void;
  /** Postgres only: run every connection in this schema (tests give each case its own). */
  schema?: string;
};

const SCHEMA_NAME = /^[a-z_][a-z0-9_]{0,62}$/;

async function sqliteDriver(file: string): Promise<SqlDriver> {
  const { DatabaseSync } = await import('node:sqlite');
  if (file !== ':memory:') mkdirSync(dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec('PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;');
  const norm = (params: unknown[]) => params.map((p) => (typeof p === 'boolean' ? Number(p) : p)) as (string | number | null)[];
  // Inside a transaction: run directly (the transaction already holds the queue).
  const tx: SqlTx = {
    run: async (sql, params = []) => ({ changes: Number(db.prepare(sql).run(...norm(params)).changes) }),
    all: async (sql, params = []) => db.prepare(sql).all(...norm(params)) as never[],
  };
  // One connection for every request. Everything goes through one queue, so while a transaction
  // is open no other request's statement lands inside its BEGIN (and is rolled back with it).
  let queue: Promise<unknown> = Promise.resolve();
  const enqueue = <T>(fn: () => T | Promise<T>): Promise<T> => {
    const next = queue.then(fn);
    queue = next.catch(() => undefined);
    return next;
  };
  return {
    dialect: 'sqlite',
    transaction<T>(fn: (t: SqlTx) => Promise<T>): Promise<T> {
      return enqueue(async () => {
        db.exec('BEGIN');
        try {
          const out = await fn(tx);
          db.exec('COMMIT');
          return out;
        } catch (e) {
          db.exec('ROLLBACK');
          throw e;
        }
      });
    },
    exec: (sql) => enqueue(() => void db.exec(sql)),
    execAtomic: (sql) =>
      enqueue(() => {
        db.exec('BEGIN');
        try {
          db.exec(sql);
          db.exec('COMMIT');
        } catch (e) {
          db.exec('ROLLBACK');
          throw e;
        }
      }),
    run: (sql, params) => enqueue(() => tx.run(sql, params)),
    all: <T,>(sql: string, params?: unknown[]) => enqueue(() => tx.all<T>(sql, params)),
    close: () => enqueue(() => db.close()),
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
  if (opts.schema) {
    if (!SCHEMA_NAME.test(opts.schema)) throw new Error(`Invalid schema name: ${opts.schema}`);
    const setPath = `SET search_path TO ${opts.schema}`;
    // Queued on the new client before any other query, so every statement sees the schema.
    pool.on('connect', (client) => void client.query(setPath).catch(() => undefined));
  }
  const toPg = (sql: string) => {
    let i = 0;
    return sql.replace(/\?/g, () => `$${++i}`);
  };
  return {
    dialect: 'postgres',
    async transaction<T>(fn: (tx: SqlTx) => Promise<T>): Promise<T> {
      const client = await pool.connect();
      /** Set when ROLLBACK itself failed: the connection is broken and must not go back to the pool. */
      let broken: Error | undefined;
      try {
        await client.query('BEGIN');
        const out = await fn({
          run: async (sql, params = []) => ({ changes: (await client.query(toPg(sql), params)).rowCount ?? 0 }),
          all: async (sql, params = []) => (await client.query(toPg(sql), params)).rows as never[],
        });
        await client.query('COMMIT');
        return out;
      } catch (e) {
        await client.query('ROLLBACK').catch((err: unknown) => {
          broken = err instanceof Error ? err : new Error(String(err));
        });
        throw e;
      } finally {
        client.release(broken);
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
