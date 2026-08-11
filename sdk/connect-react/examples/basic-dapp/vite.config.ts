import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { viteStaticCopy } from 'vite-plugin-static-copy';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));
const wasmDist = join(root, '../../../bridge/wasm/dist');

export default defineConfig({
  plugins: [
    react(),
    viteStaticCopy({
      targets: [
        { src: join(wasmDist, 'sqc_signer.wasm'), dest: '.' },
        { src: join(wasmDist, 'wasm_exec.js'), dest: '.' },
      ],
    }),
  ],
  server: {
    port: 5173,
  },
});
