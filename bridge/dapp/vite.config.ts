import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';

export default defineConfig({
  plugins: [react(), sqcWasmPlugin()],
  server: { port: 5175 },
  resolve: { dedupe: ['react', 'react-dom'] },
});
