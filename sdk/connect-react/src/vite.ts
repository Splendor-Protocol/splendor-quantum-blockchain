/**
 * Vite plugin: copy sqc_signer.wasm + wasm_exec.js into the app public dir.
 *
 *   import { sqcWasmPlugin } from '@splendor/connect-react/vite';
 *   plugins: [react(), sqcWasmPlugin()]
 */
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
  /**
   * Inject `import.meta.env.VITE_SQC_WASM_SHA256` / `VITE_SQC_EXEC_SHA256` with the hashes of
   * the copied assets so the provider can pin them (default true; existing env values win).
   */
  defineHashes?: boolean;
};

export type SqcWasmHashes = { wasmSha256: string; execSha256: string };

/** SHA-256 (hex) of the signer assets in `srcDir`. */
export function sqcWasmHashes(srcDir: string): SqcWasmHashes {
  const hash = (f: string) => createHash('sha256').update(readFileSync(join(srcDir, f))).digest('hex');
  return { wasmSha256: hash('sqc_signer.wasm'), execSha256: hash('wasm_exec.js') };
}

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
        'Build the signer in sdk/sqc-sdk (npm run build:wasm:sh, or build:wasm on Windows) and ensure the package is installed.',
    );
  }
  mkdirSync(destDir, { recursive: true });
  for (const f of FILES) {
    const from = join(src, f);
    if (!existsSync(from)) {
      throw new Error(`@splendor/connect-react/vite: missing ${from}`);
    }
    copyWritable(from, join(destDir, f));
  }
  return src;
}

/** Go ships wasm_exec.js read-only; copyFileSync keeps that mode, which breaks the next copy. */
function copyWritable(from: string, to: string): void {
  if (existsSync(to)) chmodSync(to, 0o644);
  copyFileSync(from, to);
  chmodSync(to, 0o644);
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
      const hashes = sqcWasmHashes(dest);
      writeFileSync(join(dest, 'sqc-wasm.sha256.json'), `${JSON.stringify(hashes, null, 2)}\n`);
      console.log(
        `[sqc-wasm] copied signer assets → ${dest} (from ${src}) [${command}]\n` +
          `[sqc-wasm]   sqc_signer.wasm sha256=${hashes.wasmSha256}\n` +
          `[sqc-wasm]   wasm_exec.js    sha256=${hashes.execSha256}\n` +
          '[sqc-wasm]   pin these via wasmSha256 / execSha256 (or VITE_SQC_WASM_SHA256 / VITE_SQC_EXEC_SHA256).',
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
    async config(userConfig, { mode }) {
      if (options.defineHashes === false) return;
      const src = findSqcWasmDist();
      if (!src || !existsSync(join(src, 'wasm_exec.js'))) return;
      const hashes = sqcWasmHashes(src);
      const define: Record<string, string> = {};
      const existing = userConfig.define ?? {};
      const { loadEnv } = await import('vite');
      const envDir = typeof userConfig.envDir === 'string' ? userConfig.envDir : (userConfig.root ?? process.cwd());
      const env = { ...loadEnv(mode, envDir, 'VITE_SQC_'), ...process.env };
      if (!env.VITE_SQC_WASM_SHA256 && !('import.meta.env.VITE_SQC_WASM_SHA256' in existing)) {
        define['import.meta.env.VITE_SQC_WASM_SHA256'] = JSON.stringify(hashes.wasmSha256);
      }
      if (!env.VITE_SQC_EXEC_SHA256 && !('import.meta.env.VITE_SQC_EXEC_SHA256' in existing)) {
        define['import.meta.env.VITE_SQC_EXEC_SHA256'] = JSON.stringify(hashes.execSha256);
      }
      return { define };
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
