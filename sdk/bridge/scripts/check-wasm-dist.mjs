#!/usr/bin/env node
/**
 * Fail publish/pack when sqc_signer.wasm / wasm_exec.js are missing.
 */
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const wasm = join(root, 'wasm', 'dist', 'sqc_signer.wasm');
const exec = join(root, 'wasm', 'dist', 'wasm_exec.js');

const missing = [];
if (!existsSync(wasm)) missing.push(wasm);
if (!existsSync(exec)) missing.push(exec);

if (missing.length) {
  console.error(
    '[prepublish] Missing WASM artifacts required for @splendor/pq-bridge:\n' +
      missing.map((m) => `  - ${m}`).join('\n') +
      '\nRun: npm run build:wasm (or build:wasm:sh) before publish.',
  );
  process.exit(1);
}

console.log('[prepublish] WASM artifacts OK');
