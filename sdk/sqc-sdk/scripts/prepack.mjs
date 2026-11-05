#!/usr/bin/env node
/**
 * npm prepack for @splendor/sqc-sdk, on Linux, macOS and Windows:
 *   1. build the signer from source (wasm/build.sh, or wasm/build.ps1 on Windows),
 *   2. re-pin wasmSha256 / execSha256 to those bytes (sync-config.mjs),
 *   3. compile the TypeScript,
 *   4. refuse to pack unless every network pin names the bytes in wasm/dist.
 *
 * The signer is always rebuilt: packing an existing wasm/dist could ship bytes built from older
 * source, and step 2 would pin them.
 */
import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

function run(label, cmd, args, shell = false) {
  console.log(`[prepack] ${label}`);
  const r = spawnSync(cmd, args, { cwd: root, stdio: 'inherit', shell });
  if (r.error) {
    console.error(`[prepack] ${label}: ${r.error.message}`);
    process.exit(1);
  }
  if (r.status !== 0) process.exit(r.status ?? 1);
}

if (process.platform === 'win32') {
  run('build the signer', 'powershell', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', join(root, 'wasm', 'build.ps1')]);
} else {
  run('build the signer', 'bash', [join(root, 'wasm', 'build.sh')]);
}
run('pin the signer', process.execPath, [join(root, 'scripts', 'sync-config.mjs')]);

// npm sets npm_execpath to its own CLI when it runs a lifecycle script; fall back for direct runs.
const npmCli = process.env.npm_execpath;
if (npmCli && /\.(c?js|mjs)$/.test(npmCli)) run('compile', process.execPath, [npmCli, 'run', 'build:ts']);
else run('compile', 'npm', ['run', 'build:ts'], process.platform === 'win32');

run('verify the pins', process.execPath, [join(root, 'scripts', 'check-wasm-dist.mjs')]);
