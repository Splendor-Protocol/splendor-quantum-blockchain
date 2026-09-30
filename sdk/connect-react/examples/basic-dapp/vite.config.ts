import { fileURLToPath } from 'node:url';
import { defineConfig, loadEnv, searchForWorkspaceRoot, type ProxyOptions } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
import { resolveNetworks } from './src/networks';

export default defineConfig(({ mode }) => {
  const env = { ...loadEnv(mode, process.cwd(), 'VITE_'), ...process.env };

  // Public Splendor RPCs do not send CORS headers for localhost origins, so the browser
  // blocks direct calls from `vite dev`. Proxy each https network under /rpc/<id>,
  // targeting the RPC after VITE_SQC_<ID>_RPC_URL overrides.
  const rpcProxy: Record<string, ProxyOptions> = {};
  for (const net of resolveNetworks(env)) {
    const target = net.rpcUrls.find((u) => u.startsWith('https://'));
    if (!target) continue;
    rpcProxy[`/rpc/${net.id}`] = {
      target,
      changeOrigin: true,
      secure: true,
      rewrite: (p) => p.replace(new RegExp(`^/rpc/${net.id}`), ''),
    };
  }

  return {
    plugins: [react(), sqcWasmPlugin()],
    server: {
      port: 5173,
      proxy: rpcProxy,
      // The SDK guide tab imports ../../docs/GUIDE.md, which sits outside this app's root.
      fs: {
        allow: [
          searchForWorkspaceRoot(process.cwd()),
          fileURLToPath(new URL('../../docs', import.meta.url)),
        ],
      },
    },
    preview: {
      proxy: rpcProxy,
    },
    resolve: {
      // Monorepo: prefer workspace packages when linked
      dedupe: ['react', 'react-dom'],
    },
  };
});
