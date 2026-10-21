import { defineConfig, devices } from '@playwright/test';
import { API_PORT, E2E, FAKE_PORT, WEB_PORT } from './e2e/env';

// e2e/live/* talks to a real network (E2E_LIVE=1, network from E2E_LIVE_NETWORK); the nightly workflow runs it.
const live = process.env.E2E_LIVE === '1';
// By default the suite runs the production build under the deploy CSP, with the isolated preview
// iframe. E2E_DEV=1 uses the dev server instead (faster to iterate, same-origin preview).
const dev = process.env.E2E_DEV === '1';
const web = `--host 127.0.0.1 --port ${WEB_PORT} --strictPort`;

export default defineConfig({
  testDir: './e2e',
  testIgnore: live ? [] : ['**/live/**'],
  timeout: 60_000,
  expect: { timeout: 15_000 },
  // One in-memory API database and one fake GitHub shared by every spec.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: E2E.web,
    trace: 'retain-on-failure',
    acceptDownloads: true,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: `node e2e/fake-services.mjs ${FAKE_PORT}`,
      url: `${E2E.fake}/__health`,
      reuseExistingServer: false,
    },
    {
      command: 'npx tsx src/server.ts',
      cwd: '../playground-api',
      url: `http://127.0.0.1:${API_PORT}/health`,
      reuseExistingServer: false,
      env: {
        NODE_ENV: 'test',
        PORT: String(API_PORT),
        HOST: '127.0.0.1',
        DATABASE_URL: 'sqlite::memory:',
        PLAYGROUND_ORIGINS: `${E2E.web},http://localhost:${WEB_PORT}`,
        ADMIN_ADDRESSES: E2E.admin,
        ALLOW_TEST_LOGIN: '1',
        GITHUB_TOKEN: 'e2e-token',
        GITHUB_REPO: 'Splendor-Protocol/splendor-quantum-blockchain',
        GITHUB_API_URL: `${E2E.fake}/github`,
        NPM_REGISTRY_URL: `${E2E.fake}/npm`,
      },
    },
    {
      command: dev ? `npx vite ${web}` : `npx vite build && npx vite preview ${web}`,
      url: E2E.web,
      reuseExistingServer: false,
      timeout: 240_000,
      env: {
        PLAYGROUND_API_PROXY: `http://127.0.0.1:${API_PORT}`,
        VITE_PLAYGROUND_DEFAULT_NETWORK: live ? (process.env.E2E_LIVE_NETWORK ?? 'devnet') : 'testnet',
        // Registry-pinned deps by default so the export assertions do not depend on a packed vendor bundle.
        // E2E_SDK_SOURCE=vendor (after npm run pack:sdk-vendor) exercises the file:vendor path instead.
        VITE_PLAYGROUND_SDK_SOURCE: process.env.E2E_SDK_SOURCE === 'vendor' ? 'vendor' : 'npm',
      },
    },
  ],
});
