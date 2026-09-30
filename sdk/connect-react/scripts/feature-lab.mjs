#!/usr/bin/env node
/**
 * feature-lab — build the SDKs, run the unit tests, and start the Feature Lab dApp
 * (examples/basic-dapp) where every connect-react feature has a card to verify by hand.
 *
 *   node scripts/feature-lab.mjs                 # build + test + dev server on mainnet (real SPLD)
 *   node scripts/feature-lab.mjs --network=devnet # free faucet coins; also testnet / local
 *   node scripts/feature-lab.mjs --skip-tests    # faster restart (skips both SDKs' unit tests)
 *   node scripts/feature-lab.mjs --skip-build    # just start the dev server
 *   node scripts/feature-lab.mjs --preview       # production build + preview (needs --wasm-sha256)
 *   node scripts/feature-lab.mjs --check         # build + test + typecheck, no server (CI)
 *
 * Override the selected network's endpoints (otherwise the SDK built-ins are used):
 *   --rpc=https://quantum-rpc-testnet.splendor.org
 *   --chain-id=splendor-quantum-testnet-1
 *   --explorer=https://quantum-testnet-explorer.splendor.org
 * Same as VITE_SQC_<ID>_RPC_URL / _CHAIN_ID / _EXPLORER_URL in examples/basic-dapp/.env.local,
 * which is created from .env.example on first run. Precedence: flags > shell env > .env.local.
 */
import { spawnSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const connectDir = path.resolve(here, '..');
const sdkDir = path.resolve(connectDir, '..', 'sqc-sdk');
const labDir = path.join(connectDir, 'examples', 'basic-dapp');
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const npx = process.platform === 'win32' ? 'npx.cmd' : 'npx';

const argv = process.argv.slice(2);
const flag = (name) => argv.includes(`--${name}`);
const opt = (name, fallback) => {
  const hit = argv.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
};

if (flag('help') || flag('h')) {
  console.log(fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('*/')[0]);
  process.exit(0);
}

const envFile = path.join(labDir, '.env.local');
const envExample = path.join(labDir, '.env.example');
if (!fs.existsSync(envFile) && fs.existsSync(envExample)) {
  fs.copyFileSync(envExample, envFile);
  console.log(`Created ${path.relative(process.cwd(), envFile)} from .env.example (edit it to change networks).`);
}

function readEnvFile(file) {
  if (!fs.existsSync(file)) return {};
  const out = {};
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const m = line.match(/^\s*([A-Z0-9_]+)\s*=\s*(.*?)\s*$/);
    if (m) out[m[1]] = m[2].replace(/^(['"])(.*)\1$/, '$2');
  }
  return out;
}
const fileEnv = readEnvFile(envFile);

const network = opt('network', process.env.VITE_SQC_NETWORK || fileEnv.VITE_SQC_NETWORK || 'mainnet');
const wasmSha256 = opt(
  'wasm-sha256',
  process.env.VITE_SQC_WASM_SHA256 || fileEnv.VITE_SQC_WASM_SHA256 || '',
);
const portFlag = opt('port');
const skipBuild = flag('skip-build');
const skipTests = flag('skip-tests') || skipBuild;
const preview = flag('preview');
const checkOnly = flag('check');

const env = { ...fileEnv, ...process.env, VITE_SQC_NETWORK: network };
if (wasmSha256) env.VITE_SQC_WASM_SHA256 = wasmSha256;

const netKey = `VITE_SQC_${network.toUpperCase()}`;
const overrides = { RPC_URL: opt('rpc'), CHAIN_ID: opt('chain-id'), EXPLORER_URL: opt('explorer') };
for (const [key, value] of Object.entries(overrides)) {
  if (value) env[`${netKey}_${key}`] = value;
}

let stepNo = 0;
function step(title, cmd, args, cwd) {
  stepNo += 1;
  const label = `[${stepNo}] ${title}`;
  console.log(`\n\x1b[36m${label}\x1b[0m  (${path.relative(process.cwd(), cwd) || '.'}: ${cmd} ${args.join(' ')})`);
  const started = Date.now();
  const res = spawnSync(cmd, args, { cwd, stdio: 'inherit', env, shell: process.platform === 'win32' });
  const secs = ((Date.now() - started) / 1000).toFixed(1);
  if (res.status !== 0) {
    console.error(`\n\x1b[31mFAILED\x1b[0m ${label} after ${secs}s (exit ${res.status ?? res.signal})`);
    process.exit(res.status ?? 1);
  }
  console.log(`\x1b[32mOK\x1b[0m ${label} (${secs}s)`);
}

function ensureInstalled(dir, name) {
  if (!fs.existsSync(path.join(dir, 'node_modules'))) {
    step(`Install ${name}`, npm, ['install'], dir);
  }
}

function assertWasm() {
  const candidates = [
    path.join(sdkDir, 'wasm', 'dist', 'sqc_signer.wasm'),
    path.join(sdkDir, 'wasm', 'sqc_signer.wasm'),
  ];
  if (!candidates.some((p) => fs.existsSync(p))) {
    console.error(
      '\n\x1b[31mMissing sqc_signer.wasm.\x1b[0m Build it once with Go installed:\n' +
        '  cd sdk/sqc-sdk && npm run build:wasm\n',
    );
    process.exit(1);
  }
}

console.log(`Splendor Feature Lab — network: ${network}${wasmSha256 ? ' (wasm pinned)' : ''}`);
for (const key of Object.keys(overrides)) {
  if (env[`${netKey}_${key}`]) console.log(`  ${netKey}_${key}=${env[`${netKey}_${key}`]}`);
}
if (network === 'mainnet') {
  console.log(
    '\x1b[33mMainnet: transactions spend real SPLD. Use --network=devnet for free faucet coins.\x1b[0m',
  );
}

if (!skipBuild) {
  ensureInstalled(sdkDir, '@splendor/sqc-sdk');
  assertWasm();
  step('Build @splendor/sqc-sdk', npm, ['run', 'build'], sdkDir);
  if (!skipTests) step('Unit tests @splendor/sqc-sdk', npm, ['test'], sdkDir);

  ensureInstalled(connectDir, '@splendor/connect-react');
  if (!skipTests) step('Unit tests @splendor/connect-react', npm, ['test'], connectDir);
  step('Build @splendor/connect-react', npm, ['run', 'build'], connectDir);

  ensureInstalled(labDir, 'feature lab');
  step('Typecheck feature lab', npx, ['tsc', '--noEmit', '-p', 'tsconfig.json'], labDir);
} else {
  ensureInstalled(labDir, 'feature lab');
}

if (checkOnly) {
  console.log('\n\x1b[32mAll checks passed.\x1b[0m');
  process.exit(0);
}

if (preview) {
  if (!wasmSha256) {
    console.error(
      '\n--preview is a production build and needs the signer pin:\n' +
        '  node scripts/feature-lab.mjs --preview --wasm-sha256=<sha256 of sqc_signer.wasm>\n',
    );
    process.exit(1);
  }
  step('Production build feature lab', npm, ['run', 'build'], labDir);
}

function portFree(p) {
  return new Promise((resolve) => {
    const srv = net.createServer();
    srv.once('error', () => resolve(false));
    srv.once('listening', () => srv.close(() => resolve(true)));
    srv.listen(p, 'localhost');
  });
}

let port = portFlag || '5173';
if (!portFlag) {
  let p = 5173;
  while (!(await portFree(p)) && p < 5193) p += 1;
  if (p !== 5173) console.log(`Port 5173 is busy (another lab or app is running); using ${p}.`);
  port = String(p);
}

const serveArgs = preview
  ? ['run', 'preview', '--', '--port', port, '--strictPort']
  : ['run', 'dev', '--', '--port', port, '--strictPort'];

console.log(
  `\n\x1b[36mStarting Feature Lab\x1b[0m → http://localhost:${port}\n` +
    '  Work top to bottom; tick "Verified" on each card when every check passes.\n' +
    '  Ctrl+C to stop.\n',
);

const child = spawn(npm, serveArgs, {
  cwd: labDir,
  stdio: 'inherit',
  env,
  shell: process.platform === 'win32',
});
child.on('exit', (code) => process.exit(code ?? 0));
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => child.kill(sig));
}
