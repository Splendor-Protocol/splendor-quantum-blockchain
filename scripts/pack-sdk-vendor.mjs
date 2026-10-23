#!/usr/bin/env node
/**
 * Packs built @splendor packages into apps/playground/public/vendor for playground exports
 * that use file: dependencies (StackBlitz / unpublished npm).
 *
 *   npm run pack:sdk-vendor
 *
 * What ends up where:
 * - `vendor/@splendor/sqc-sdk` and `vendor/@splendor/connect-react` are copied AND listed in
 *   `vendor-bundle.json`: the playground merges that bundle into every exported project.
 * - `vendor/@splendor/dapp-spec` (its `dist` and `package.json`) is copied when `packages/dapp-spec/dist`
 *   exists, but is NOT in the bundle: exported dApps never import the code generator. It is there for
 *   tooling that runs next to the vendored SDK (for example scripts/webcontainer-smoke.mjs).
 *
 * Needs the packages built first: `npm run build:packages`.
 */
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const vendorRoot = join(root, 'apps/playground/public/vendor');
const out = join(vendorRoot, '@splendor');

const packs = [
  { name: 'sqc-sdk', from: join(root, 'sdk/sqc-sdk'), files: ['package.json', 'dist', 'wasm/dist'] },
  { name: 'connect-react', from: join(root, 'sdk/connect-react'), files: ['package.json', 'dist'] },
  // Optional: only packed when built. Not bundled into exported projects (see header).
  { name: 'dapp-spec', from: join(root, 'packages/dapp-spec'), files: ['package.json', 'dist'], optional: true, bundle: false },
];

const TEXT_EXT = new Set(['.js', '.mjs', '.cjs', '.ts', '.tsx', '.json', '.css', '.md', '.map', '.d.ts', '.txt', '.html']);

function isText(path) {
  if (path.endsWith('.wasm.b64')) return true;
  if (path.endsWith('.wasm')) return false;
  const i = path.lastIndexOf('.');
  if (i < 0) return false;
  const ext = path.slice(i);
  if (ext === '.ts' && path.endsWith('.d.ts')) return true;
  return TEXT_EXT.has(ext) || path.endsWith('.d.ts');
}

function walkFiles(dir, base, into) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    const rel = relative(base, full).replace(/\\/g, '/');
    if (statSync(full).isDirectory()) walkFiles(full, base, into);
    else into.push(rel);
  }
}

rmSync(vendorRoot, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

const bundle = {};
const packed = [];
const skipped = [];

for (const p of packs) {
  if (p.optional && !existsSync(join(p.from, 'dist'))) {
    console.log(`[pack-sdk-vendor] skip ${p.name} (no dist; run npm run build:packages)`);
    skipped.push(p.name);
    continue;
  }
  const dest = join(out, p.name);
  mkdirSync(dest, { recursive: true });
  for (const f of p.files) {
    const src = join(p.from, f);
    if (!existsSync(src)) {
      console.warn(`[pack-sdk-vendor] missing ${src}`);
      continue;
    }
    cpSync(src, join(dest, f), { recursive: true });
  }
  const pkgPath = join(dest, 'package.json');
  if (existsSync(pkgPath)) {
    const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'));
    delete pkg.scripts?.prepare;
    delete pkg.scripts?.prepublishOnly;
    // npm installs a file: dependency's devDependencies too (a published tarball never has them):
    // they would pull a second Vite/React into exported projects and clash with the app's own.
    delete pkg.devDependencies;
    writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
  }
  const files = [];
  walkFiles(dest, dest, files);
  for (const rel of files) {
    const abs = join(dest, rel);
    const key = `vendor/@splendor/${p.name}/${rel}`;
    if (rel.endsWith('.wasm')) {
      const b64 = readFileSync(abs).toString('base64');
      if (p.bundle !== false) bundle[`${key}.b64`] = b64;
      writeFileSync(abs + '.b64', b64);
      continue;
    }
    if (p.bundle !== false && isText(rel)) bundle[key] = readFileSync(abs, 'utf8');
  }
  packed.push({ name: p.name, bundled: p.bundle !== false });
  console.log(`[pack-sdk-vendor] ${p.name} (${files.length} files${p.bundle === false ? ', not bundled' : ''})`);
}

writeFileSync(join(vendorRoot, 'vendor-bundle.json'), JSON.stringify(bundle));
writeFileSync(
  join(vendorRoot, 'manifest.json'),
  JSON.stringify(
    {
      packedAt: new Date().toISOString(),
      packages: packed.map((p) => p.name),
      bundled: packed.filter((p) => p.bundled).map((p) => p.name),
      skipped,
      fileCount: Object.keys(bundle).length,
    },
    null,
    2,
  ) + '\n',
);
console.log(`[pack-sdk-vendor] bundle ${Object.keys(bundle).length} entries -> public/vendor`);
