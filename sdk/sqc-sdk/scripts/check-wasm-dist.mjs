#!/usr/bin/env node
/**
 * Fail publish/pack when sqc_signer.wasm / wasm_exec.js are missing, or when
 * their digests have drifted from the wasmSha256 / execSha256 pins consumers
 * verify at load. A present-but-unpinned artifact is the worse failure: every
 * consumer fails closed on it, so the package would ship a signer nobody can
 * load. Rebuild with wasm/build.sh, then `npm run sync:config`.
 */
import { existsSync, readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname, join, relative } from 'node:path';
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

const sha256 = (p) => createHash('sha256').update(readFileSync(p)).digest('hex');
const digests = { wasmSha256: sha256(wasm), execSha256: sha256(exec) };

// Both copies. sync-config.mjs writes src/config and mirrors it to config/,
// so checking only the source would pass a mirror that never got the update —
// and config/ is the one packaged consumers read.
const configs = ['src/config/sqc.networks.json', 'config/sqc.networks.json'];
const drift = [];
for (const rel of configs) {
  const file = join(root, rel);
  if (!existsSync(file)) continue;
  const config = JSON.parse(readFileSync(file, 'utf8'));
  for (const [id, net] of Object.entries(config.networks)) {
    for (const [field, actual] of Object.entries(digests)) {
      if (net[field] !== actual) {
        drift.push(`  - ${rel} networks.${id}.${field}: pinned ${net[field] || '(unset)'}, built ${actual}`);
      }
    }
  }
}

if (drift.length) {
  console.error(
    '[check-wasm-dist] WASM digests do not match the pins consumers verify at load:\n' +
      drift.join('\n') +
      '\nRun: npm run sync:config (and refresh the wallet-extension copy + its pins).',
  );
  process.exit(1);
}

console.log(`[check-wasm-dist] WASM artifacts OK — sqc_signer.wasm ${digests.wasmSha256}`);
