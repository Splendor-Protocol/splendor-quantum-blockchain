#!/usr/bin/env node
// Runs the playground API tests on Postgres instead of in-memory SQLite. Each test case gets its
// own schema (created and dropped by the suite), so any database the user can create schemas in
// works. Start one with:
//   docker run --rm -e POSTGRES_PASSWORD=pg -p 5432:5432 postgres:16
import { spawnSync } from 'node:child_process';

const url = process.env.TEST_DATABASE_URL ?? 'postgres://postgres:pg@127.0.0.1:5432/postgres';
if (!/^postgres(ql)?:\/\//.test(url)) {
  console.error(`TEST_DATABASE_URL must be a postgres:// URL (got ${url})`);
  process.exit(2);
}
console.log(`[test-api-postgres] ${url.replace(/:[^:@/]+@/, ':***@')}`);
const r = spawnSync('npm', ['test', '-w', '@splendor/playground-api'], {
  stdio: 'inherit',
  shell: process.platform === 'win32',
  env: { ...process.env, TEST_DATABASE_URL: url },
});
process.exit(r.status ?? 1);
