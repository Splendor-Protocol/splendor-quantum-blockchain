import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/__tests__/**/*.test.ts', 'src/__tests__/**/*.test.tsx'],
    // jsdom widget flows take under 1 s alone but several seconds when every file runs in parallel.
    testTimeout: 20_000,
  },
});
