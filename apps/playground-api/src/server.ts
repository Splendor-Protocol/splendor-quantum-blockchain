import { existsSync } from 'node:fs';
import { buildApp } from './app.js';
import { loadConfig } from './config.js';
import { createStore } from './store/index.js';

const HOUSEKEEPING_INTERVAL_MS = 10 * 60 * 1000;
const SHUTDOWN_TIMEOUT_MS = 10_000;

if (existsSync('.env.local')) process.loadEnvFile('.env.local');
const config = loadConfig();
let log: { error: (o: object, m: string) => void } = { error: (o, m) => console.error(m, o) };
const store = await createStore(config.databaseUrl, { onError: (err) => log.error({ err }, 'database client error') });
const app = await buildApp({ config, store, logger: true, housekeepingIntervalMs: HOUSEKEEPING_INTERVAL_MS });
log = app.log;

let closing = false;
const shutdown = async (signal: string) => {
  if (closing) return;
  closing = true;
  app.log.info({ signal }, 'shutting down');
  // In-flight requests get a bounded window; a stuck one must not keep the process alive forever.
  setTimeout(() => {
    app.log.error('shutdown timed out, exiting');
    process.exit(1);
  }, SHUTDOWN_TIMEOUT_MS).unref();
  try {
    await app.close();
    await store.close();
    process.exit(0);
  } catch (err) {
    app.log.error({ err }, 'shutdown failed');
    process.exit(1);
  }
};
process.on('SIGINT', () => void shutdown('SIGINT'));
process.on('SIGTERM', () => void shutdown('SIGTERM'));

await app.listen({ port: config.port, host: config.host });
for (const w of config.warnings) app.log.warn(w);
if (config.allowTestLogin) app.log.warn('ALLOW_TEST_LOGIN=1: password-less /auth/test-login is enabled (tests only)');
if (!config.github.token) app.log.info('GITHUB_TOKEN not set: release dashboard is read-only (npm versions only)');
if (config.production && config.trustProxy === false) {
  app.log.info('TRUST_PROXY is off: client IPs (rate limits, audit) are the TCP peer. Set it when running behind a reverse proxy.');
}
