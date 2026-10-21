#!/usr/bin/env node
// Static smoke test for the "Open in StackBlitz / WebContainer" export path. It does NOT boot a
// WebContainer or run `npm install`: it checks the inputs a WebContainer would need.
//
//   1. apps/playground/public/vendor/vendor-bundle.json exists and carries both vendored SDK packages
//      (and the signer as base64).
//   2. A project generated in vendor mode depends on file:vendor/@splendor/* and ships those trees,
//      the wasm decode script and its postinstall hook.
//   3. The generated project has the studio helpers (contracts.ts + actions.ts).
//
//   node scripts/webcontainer-smoke.mjs [--allow-missing] [--frameworks=vite,next]
//
// Run `npm run pack:sdk-vendor` first (it needs `npm run build:packages`).
// --allow-missing: exit 0 with a notice when the bundle has not been packed (for optional CI jobs).
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
const args = Object.fromEntries(
  process.argv.slice(2).map((a) => {
    const m = /^--([a-z-]+)(?:=(.*))?$/.exec(a);
    if (!m) throw new Error(`Unknown argument ${a}`);
    return [m[1], m[2] ?? true];
  }),
);

const failures = [];
const check = (ok, message) => {
  if (!ok) failures.push(message);
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${message}`);
};

const bundlePath = join(root, 'apps/playground/public/vendor/vendor-bundle.json');
if (!existsSync(bundlePath)) {
  const msg = `vendor-bundle.json not found at ${bundlePath}. Run \`npm run pack:sdk-vendor\` first.`;
  if (args['allow-missing']) {
    console.log(`skip  ${msg}`);
    process.exit(0);
  }
  console.error(msg);
  process.exit(1);
}

// 1. The bundle
const bundle = JSON.parse(readFileSync(bundlePath, 'utf8'));
const WASM_KEY = 'vendor/@splendor/sqc-sdk/wasm/dist/sqc_signer.wasm.b64';
const PACKAGES = ['sqc-sdk', 'connect-react'];
for (const name of PACKAGES) {
  const key = `vendor/@splendor/${name}/package.json`;
  check(typeof bundle[key] === 'string', `bundle has ${key}`);
  if (typeof bundle[key] === 'string') {
    try {
      const pkg = JSON.parse(bundle[key]);
      check(pkg.name === `@splendor/${name}`, `${key} is @splendor/${name}`);
      check(!pkg.scripts?.prepare && !pkg.scripts?.prepublishOnly, `${name}: prepare/prepublishOnly scripts stripped`);
    } catch {
      check(false, `${key} is valid JSON`);
    }
  }
  check(Object.keys(bundle).some((k) => k.startsWith(`vendor/@splendor/${name}/dist/`)), `bundle has ${name}/dist files`);
}
check(typeof bundle[WASM_KEY] === 'string' && bundle[WASM_KEY].length > 1000, 'bundle has the signer as base64');
check(!Object.keys(bundle).some((k) => k.includes('/dapp-spec/')), 'dapp-spec is not bundled into exported projects');

// 2 + 3. A generated project
const libPath = join(root, 'packages/create-splendor-dapp/bin/lib.mjs');
const catalogPath = join(root, 'packages/create-splendor-dapp/catalog.json');
if (!existsSync(join(root, 'packages/dapp-spec/dist/index.js')) || !existsSync(catalogPath)) {
  console.error('Build first: `npm run build:packages` and `npm run build -w create-splendor-dapp`.');
  process.exit(1);
}
const lib = await import(pathToFileURL(libPath).href);
const frameworks = args.frameworks ? String(args.frameworks).split(',') : ['vite', 'next'];
for (const framework of frameworks) {
  const label = `[${framework}]`;
  const project = lib.buildProject({
    name: `smoke-${framework}`,
    template: 'wallet-dashboard',
    framework,
    network: 'testnet',
    sdk: 'vendor',
    vendorFiles: bundle,
    vendorWasmBase64: bundle[WASM_KEY],
  });
  const files = project.files;
  const pkg = JSON.parse(files['package.json']);
  for (const name of PACKAGES) {
    const dep = pkg.dependencies[`@splendor/${name}`];
    check(dep === `file:vendor/@splendor/${name}`, `${label} @splendor/${name} -> ${dep}`);
    check(`vendor/@splendor/${name}/package.json` in files, `${label} ships vendor/@splendor/${name}/package.json`);
  }
  check(pkg.scripts?.postinstall === 'node scripts/decode-wasm.mjs', `${label} postinstall decodes the signer`);
  check('scripts/decode-wasm.mjs' in files, `${label} has scripts/decode-wasm.mjs`);
  check(WASM_KEY in files, `${label} ships the signer as base64`);
  const dir = framework === 'vite' ? 'src' : 'app';
  check(`${dir}/contracts.ts` in files, `${label} has ${dir}/contracts.ts`);
  check(`${dir}/actions.ts` in files, `${label} has ${dir}/actions.ts`);
  const registry = Object.entries(pkg.dependencies).filter(([n, v]) => n.startsWith('@splendor/') && !String(v).startsWith('file:vendor/'));
  check(registry.length === 0, `${label} has no registry-pinned @splendor deps`);
}

if (failures.length) {
  console.error(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log('\nWebContainer smoke checks passed (static; no WebContainer was started).');
