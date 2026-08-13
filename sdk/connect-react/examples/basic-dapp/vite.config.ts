import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '../../dist/vite.js';

export default defineConfig({
  plugins: [react(), sqcWasmPlugin()],
  server: {
    port: 5173,
  },
});
