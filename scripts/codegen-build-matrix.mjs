#!/usr/bin/env node
// Generates every template for every framework exactly as `create-splendor-dapp`
// would, installs the SDKs from `npm pack` tarballs of this checkout (not the
// workspace links, so a missing `files` entry or export fails here rather than
// for the first user), and runs each project's production build.
//
//   node scripts/codegen-build-matrix.mjs [--templates=a,b] [--frameworks=vite,next] [--out=dir] [--keep]
//
// Needs the packages built first: the signer (sdk/sqc-sdk/wasm/build.sh) and
// `npm run build:packages`.
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
const args = Object.fromEntries(
  process.argv.slice(2).map((a) => {
    const m = /^--([a-z]+)(?:=(.*))?$/.exec(a);
    if (!m) throw new Error(`Unknown argument ${a}`);
    return [m[1], m[2] ?? true];
  }),
);

const SAMPLE_ADDRESS = `SQ:${'5a'.repeat(32)}`;

function run(cmd, cmdArgs, cwd, { quiet = false } = {}) {
  const r = spawnSync(cmd, cmdArgs, {
    cwd,
    encoding: 'utf8',
    shell: process.platform === 'win32',
    env: { ...process.env, CI: '1', NEXT_TELEMETRY_DISABLED: '1' },
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.status !== 0 && !quiet) {
    process.stdout.write(r.stdout ?? '');
    process.stderr.write(r.stderr ?? '');
  }
  return r;
}

function requireBuilt() {
  const needed = [
    'sdk/sqc-sdk/dist/index.js',
    'sdk/sqc-sdk/wasm/dist/sqc_signer.wasm',
    'sdk/connect-react/dist/index.js',
    'packages/dapp-spec/dist/index.js',
  ];
  const missing = needed.filter((p) => !existsSync(join(root, p)));
  if (missing.length) {
    console.error(`Missing build outputs:\n  ${missing.join('\n  ')}`);
    console.error('Run `bash sdk/sqc-sdk/wasm/build.sh` and `npm run build:packages` first.');
    process.exit(1);
  }
  const catalog = join(root, 'packages/create-splendor-dapp/catalog.json');
  if (!existsSync(catalog)) {
    const r = run('npm', ['run', 'build', '-w', 'create-splendor-dapp'], root);
    if (r.status !== 0) process.exit(1);
  }
}

function pack(dest) {
  mkdirSync(dest, { recursive: true });
  // --ignore-scripts: sqc-sdk's prepack rebuilds the signer with Go; the caller already built it.
  const r = run('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', dest, '-w', '@splendor/sqc-sdk', '-w', '@splendor/connect-react'], root);
  if (r.status !== 0) process.exit(1);
  const out = JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  const byName = Object.fromEntries(out.map((p) => [p.name, join(dest, p.filename)]));
  for (const name of ['@splendor/sqc-sdk', '@splendor/connect-react']) {
    if (!byName[name] || !existsSync(byName[name])) throw new Error(`npm pack produced no tarball for ${name}`);
  }
  return byName;
}

function hasFile(dir, test) {
  if (!existsSync(dir)) return false;
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory() ? hasFile(p, test) : test(entry)) return true;
  }
  return false;
}

function checkOutput(dir, framework) {
  if (framework === 'vite') {
    if (!existsSync(join(dir, 'dist/index.html'))) return 'dist/index.html missing';
    if (!hasFile(join(dir, 'dist'), (f) => f.endsWith('.wasm'))) return 'signer .wasm not emitted into dist/';
    return null;
  }
  if (!existsSync(join(dir, '.next/BUILD_ID'))) return '.next/BUILD_ID missing';
  if (!existsSync(join(dir, 'public/sqc_signer.wasm'))) return 'public/sqc_signer.wasm missing';
  const env = existsSync(join(dir, '.env.production.local')) ? readFileSync(join(dir, '.env.production.local'), 'utf8') : '';
  if (!/NEXT_PUBLIC_SQC_WASM_SHA256=[0-9a-f]{64}/.test(env)) return 'NEXT_PUBLIC_SQC_WASM_SHA256 pin missing';
  return null;
}

async function main() {
  requireBuilt();
  const lib = await import(pathToFileURL(join(root, 'packages/create-splendor-dapp/bin/lib.mjs')).href);
  const templates = args.templates ? String(args.templates).split(',') : lib.TEMPLATES.map((t) => t.id);
  const frameworks = args.frameworks ? String(args.frameworks).split(',') : lib.FRAMEWORKS;

  const work = resolve(typeof args.out === 'string' ? args.out : join(tmpdir(), `splendor-codegen-${Date.now()}`));
  mkdirSync(work, { recursive: true });
  console.log(`Working in ${work}`);
  const tarballs = pack(join(work, 'tarballs'));

  const results = [];
  for (const templateId of templates) {
    const template = lib.TEMPLATES.find((t) => t.id === templateId);
    if (!template) throw new Error(`Unknown template ${templateId}`);
    for (const framework of frameworks) {
      const label = `${templateId} / ${framework}`;
      const started = Date.now();
      const dir = join(work, `${templateId}-${framework}`);
      console.log(`\n== ${label}`);
      let error = null;
      try {
        const set = template.setup.map((s) => `${s.blockId}.${s.prop}=${SAMPLE_ADDRESS}`);
        const project = lib.buildProject({ name: `${templateId}-${framework}`, template: templateId, framework, network: 'testnet', set });
        lib.writeProject(dir, project.files);

        const pkgPath = join(dir, 'package.json');
        const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'));
        for (const [name, file] of Object.entries(tarballs)) pkg.dependencies[name] = `file:${file.replace(/\\/g, '/')}`;
        pkg.overrides = { '@splendor/sqc-sdk': '$@splendor/sqc-sdk' };
        writeFileSync(pkgPath, `${JSON.stringify(pkg, null, 2)}\n`);

        if (run('npm', ['install', '--no-audit', '--no-fund', '--loglevel=error'], dir).status !== 0) error = 'npm install failed';
        else if (run('npm', ['run', 'build'], dir).status !== 0) error = 'build failed';
        else error = checkOutput(dir, framework);
      } catch (err) {
        error = err instanceof Error ? err.message : String(err);
      }
      const seconds = Math.round((Date.now() - started) / 1000);
      results.push({ label, error, seconds });
      // A Next.js install is several hundred MB; ten of them fill a CI disk.
      if (!error && !args.keep) rmSync(join(dir, 'node_modules'), { recursive: true, force: true });
      console.log(error ? `FAIL ${label}: ${error}` : `ok   ${label} (${seconds}s)`);
    }
  }

  console.log('\nCodegen build matrix');
  for (const r of results) console.log(`  ${r.error ? 'FAIL' : 'ok  '}  ${r.label.padEnd(28)} ${r.error ?? `${r.seconds}s`}`);
  const failed = results.filter((r) => r.error);
  if (!args.keep && !failed.length) rmSync(work, { recursive: true, force: true });
  else console.log(`\nProjects kept in ${work}`);
  process.exit(failed.length ? 1 : 0);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
