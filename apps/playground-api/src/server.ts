import { existsSync } from 'node:fs';
import { buildApp } from './app.js';
import { loadConfig } from './config.js';
import { createStore } from './store/index.js';

if (existsSync('.env.local')) process.loadEnvFile('.env.local');
const config = loadConfig();
const store = await createStore(config.databaseUrl);
const app = await buildApp({ config, store, logger: true });

const shutdown = async () => {
  await app.close();
  await store.close();
  process.exit(0);
};
process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);

await app.listen({ port: config.port, host: config.host });
if (config.allowTestLogin) app.log.warn('ALLOW_TEST_LOGIN=1: password-less /auth/test-login is enabled (tests only)');
if (!config.github.token) app.log.info('GITHUB_TOKEN not set: release dashboard is read-only (npm versions only)');
