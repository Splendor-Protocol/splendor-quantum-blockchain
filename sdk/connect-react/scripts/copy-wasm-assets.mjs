#!/usr/bin/env node
/**
 * Copy sqc_signer.wasm + wasm_exec.js from @splendor/sqc-sdk into a public dir.
 *
 * Usage:
 *   node scripts/copy-wasm-assets.mjs [destDir]
 *   node scripts/copy-wasm-assets.mjs ./public
 *
 * Default dest: ./public (cwd-relative)
 */
import { chmodSync, copyFileSync, existsSync, mkdirSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));

function findWasmDist() {
  // Prefer the sibling sqc-sdk package in this monorepo, then the installed one.
  const sibling = resolve(here, '../../sqc-sdk/wasm/dist');
  if (existsSync(join(sibling, 'sqc_signer.wasm'))) return sibling;

  try {
    const pkgJson = require.resolve('@splendor/sqc-sdk/package.json');
    const candidate = join(dirname(pkgJson), 'wasm', 'dist');
    if (existsSync(join(candidate, 'sqc_signer.wasm'))) return candidate;
  } catch {
    /* not installed */
  }
  return null;
}

const destArg = process.argv[2] || 'public';
const dest = resolve(process.cwd(), destArg);
const src = findWasmDist();

if (!src) {
  console.error(
    'copy-wasm-assets: could not find sqc_signer.wasm. Build @splendor/sqc-sdk wasm first (sdk/sqc-sdk/wasm/build.ps1).',
  );
  process.exit(1);
}

mkdirSync(dest, { recursive: true });
const files = ['sqc_signer.wasm', 'wasm_exec.js'];
for (const f of files) {
  const from = join(src, f);
  if (!existsSync(from)) {
    console.error('Missing', from);
    process.exit(1);
  }
  const to = join(dest, f);
  // Go ships wasm_exec.js read-only; copyFileSync keeps that mode, which breaks the next copy.
  if (existsSync(to)) chmodSync(to, 0o644);
  copyFileSync(from, to);
  chmodSync(to, 0o644);
  console.log('Copied', f, '→', join(dest, f));
}

console.log('WASM assets ready in', dest, '(from', src + ')');
if (process.env.DEBUG) {
  console.log('src listing:', readdirSync(src));
}
