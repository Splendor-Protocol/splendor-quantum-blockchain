/**
 * Vite plugin: copy sqc_signer.wasm + wasm_exec.js into the app public dir.
 *
 *   import { sqcWasmPlugin } from '@splendor/connect-react/vite';
 *   plugins: [react(), sqcWasmPlugin()]
 */
import { copyFileSync, existsSync, mkdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';

const require = createRequire(import.meta.url);

export type SqcWasmPluginOptions = {
  /** Destination directory relative to Vite root (default: publicDir). */
  outDir?: string;
  /** Fail Vite build/dev if WASM assets are missing (default true). */
  strict?: boolean;
};

const FILES = ['sqc_signer.wasm', 'wasm_exec.js'] as const;

export function findSqcWasmDist(fromDir?: string): string | null {
  const here = fromDir || dirname(fileURLToPath(import.meta.url));
  const siblings = [
    resolve(here, '../../sqc-sdk/wasm/dist'),
    resolve(here, '../../bridge/wasm/dist'),
    resolve(here, '../../../sqc-sdk/wasm/dist'),
    resolve(here, '../../../bridge/wasm/dist'),
    resolve(here, '../sqc-sdk/wasm/dist'),
    resolve(here, '../bridge/wasm/dist'),
  ];
  for (const sibling of siblings) {
    if (existsSync(join(sibling, 'sqc_signer.wasm'))) return sibling;
  }
  try {
    for (const pkg of ['@splendor/sqc-sdk', '@splendor/pq-bridge']) {
      try {
        const pkgJson = require.resolve(`${pkg}/package.json`);
        const candidates = [
          join(dirname(pkgJson), 'wasm', 'dist'),
          resolve(dirname(pkgJson), '..', 'wasm', 'dist'),
        ];
        for (const c of candidates) {
          if (existsSync(join(c, 'sqc_signer.wasm'))) return c;
        }
      } catch {
        /* not installed */
      }
    }
  } catch {
    /* not installed */
  }
  return null;
}

export function copySqcWasmAssets(destDir: string, srcDir?: string | null): string {
  const src = srcDir ?? findSqcWasmDist();
  if (!src) {
    throw new Error(
      '@splendor/connect-react/vite: could not find sqc_signer.wasm. ' +
<<<<<<< HEAD
        'Build @splendor/sqc-sdk WASM (npm run build:wasm / wasm/build.ps1 in sdk/sqc-sdk) and ensure the package is installed.',
=======
        'Build @splendor/pq-bridge WASM (npm run build:wasm in sdk/sqc-sdk) and ensure the package is installed.',
>>>>>>> 0cfe34734150d754e18212c33093a189229d1991
    );
  }
  mkdirSync(destDir, { recursive: true });
  for (const f of FILES) {
    const from = join(src, f);
    if (!existsSync(from)) {
      throw new Error(`@splendor/connect-react/vite: missing ${from}`);
    }
    copyFileSync(from, join(destDir, f));
  }
  return src;
}

export function sqcWasmPlugin(options: SqcWasmPluginOptions = {}): Plugin {
  const strict = options.strict !== false;
  let root = process.cwd();
  let publicDir = 'public';
  let copied = false;

  const runCopy = (command: 'serve' | 'build') => {
    if (copied) return;
    const dest = options.outDir
      ? resolve(root, options.outDir)
      : resolve(root, publicDir);
    try {
      const src = copySqcWasmAssets(dest);
      console.log(
        `[sqc-wasm] copied signer assets → ${dest} (from ${src}) [${command}]`,
      );
      copied = true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (strict) throw e;
      console.warn(`[sqc-wasm] ${msg}`);
    }
  };

  return {
    name: 'splendor-sqc-wasm',
    resolveId(id) {
      // Keep Node fs WASM loader out of browser bundles (Rollup follows both import branches).
      if (id.includes('wasm-loader.node')) {
        return '\0splendor-sqc-wasm-loader-node-stub';
      }
      return null;
    },
    load(id) {
      if (id === '\0splendor-sqc-wasm-loader-node-stub') {
        return 'export async function loadWasmInNode() { throw new Error("Node WASM loader is not available in the browser"); }';
      }
      return null;
    },
    configResolved(config) {
      root = config.root;
      publicDir = config.publicDir || 'public';
    },
    buildStart() {
      runCopy('build');
    },
    configureServer() {
      runCopy('serve');
    },
  };
}

export default sqcWasmPlugin;
