import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['security.test.ts', 'tls-policy.test.ts'],
    testTimeout: 60_000,
    hookTimeout: 60_000,
  },
});
