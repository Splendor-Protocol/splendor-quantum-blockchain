import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';

/**
 * Browser embed bundle: one IIFE (`dist/embed.js`, global `SplendorEmbed`) with React inlined, for
 * `<script src>` use on pages that have no bundler. Runs after `tsc` and overwrites the ESM `embed.js` it
 * emitted (the `.d.ts` stays). Pair it with `@splendor/connect-react/style.css`.
 */
export default defineConfig({
  publicDir: false,
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    lib: {
      entry: fileURLToPath(new URL('./src/embed.tsx', import.meta.url)),
      name: 'SplendorEmbed',
      formats: ['iife'],
      fileName: () => 'embed.js',
    },
    outDir: 'dist',
    emptyOutDir: false,
    minify: true,
    sourcemap: false,
    rollupOptions: { output: { exports: 'named' } },
  },
});
