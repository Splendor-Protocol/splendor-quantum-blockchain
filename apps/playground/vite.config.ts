/// <reference types="vitest/config" />
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { defineConfig, loadEnv, searchForWorkspaceRoot, type PluginOption, type ProxyOptions } from 'vite';
import react from '@vitejs/plugin-react';
import { sqcWasmPlugin } from '@splendor/connect-react/vite';
import { resolveNetworks } from './src/networks';
import { ASSET_HEADERS, SECURITY_HEADERS } from './src/securityHeaders';

/** `vite preview`: /assets/* gets ASSET_HEADERS, like `/assets/*` in public/_headers and vercel.json. */
function assetHeaders(): PluginOption {
  return {
    name: 'playground-asset-headers',
    configurePreviewServer(server) {
      server.middlewares.use((req, res, next) => {
        if (req.url?.startsWith('/assets/')) for (const [k, v] of Object.entries(ASSET_HEADERS)) res.setHeader(k, v);
        next();
      });
    },
  };
}

export default defineConfig(({ mode }) => {
  const env = { ...loadEnv(mode, process.cwd(), ['VITE_', 'PLAYGROUND_']), ...process.env };

  // Public Splendor RPCs do not send CORS headers for localhost, so dev/preview proxy each
  // https network under /rpc/<id> (src/networks.ts points the provider there on localhost).
  const proxy: Record<string, ProxyOptions> = {};
  for (const net of resolveNetworks(env)) {
    const target = net.rpcUrls.find((u) => u.startsWith('https://'));
    if (!target) continue;
    proxy[`/rpc/${net.id}`] = {
      target,
      changeOrigin: true,
      secure: true,
      rewrite: (p) => p.replace(new RegExp(`^/rpc/${net.id}`), ''),
    };
  }
  // Same-origin API in dev so the session cookie is first-party.
  proxy['/api'] = {
    target: env.PLAYGROUND_API_PROXY ?? 'http://127.0.0.1:8787',
    changeOrigin: false,
    rewrite: (p) => p.replace(/^\/api/, ''),
  };

  const version = (rel: string) =>
    (JSON.parse(readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')) as { version: string }).version;

  return {
    // In this monorepo connect-react's .d.ts sees its own dev copy of vite; the plugin is version-agnostic.
    plugins: [react(), sqcWasmPlugin() as unknown as PluginOption, assetHeaders()],
    define: {
      'import.meta.env.VITE_SQC_SDK_VERSION': JSON.stringify(version('../../sdk/sqc-sdk/package.json')),
      'import.meta.env.VITE_CONNECT_REACT_VERSION': JSON.stringify(version('../../sdk/connect-react/package.json')),
    },
    server: {
      port: 5180,
      proxy,
      fs: {
        allow: [
          searchForWorkspaceRoot(process.cwd()),
          fileURLToPath(new URL('../../sdk', import.meta.url)),
          fileURLToPath(new URL('../../packages', import.meta.url)),
        ],
      },
    },
    // `vite preview` serves the production build the way the deploy configs do (public/_headers,
    // vercel.json), so the e2e suite runs under the real CSP and with the isolated preview iframe.
    // As in those configs, only /assets is cross-origin (assetHeaders below), never /api or /rpc.
    preview: { port: 5180, proxy, headers: SECURITY_HEADERS, cors: false },
    // The entry chunk carries the SDK + widgets that every page needs. preview.html is the preview
    // iframe's own entry, so a frame does not load the playground's pages and router.
    build: {
      chunkSizeWarningLimit: 800,
      rollupOptions: {
        input: {
          main: fileURLToPath(new URL('./index.html', import.meta.url)),
          preview: fileURLToPath(new URL('./preview.html', import.meta.url)),
        },
      },
    },
    resolve: {
      dedupe: ['react', 'react-dom', '@splendor/connect-react', '@splendor/sqc-sdk'],
    },
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.{ts,tsx}'],
    },
  };
});
