import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    // 42 jsdom files run in parallel; on a loaded machine a ~1 s widget flow can pass 5 s.
    testTimeout: 15_000,
    // 42 jsdom files run in parallel; on a loaded machine a ~1 s widget flow can pass 5 s.
    testTimeout: 15_000,
    include: ['src/__tests__/**/*.test.ts', 'src/__tests__/**/*.test.tsx'],
    // jsdom widget flows take under 1 s alone but several seconds when every file runs in parallel.
    testTimeout: 20_000,
  },
});
