#!/usr/bin/env node
/**
 * create-splendor-dapp — scaffold a Vite + React dApp with ConnectKit + TransferWidget.
 *
 *   npx create-splendor-dapp my-dapp
 *   npm exec create-splendor-dapp my-dapp
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const templateDir = path.join(__dirname, '..', 'create-template');

function usage() {
  console.log(`Usage: create-splendor-dapp <project-name>

Scaffolds a Vite + React dApp with:
  - SplendorConnectProvider + ConnectKit
  - TransferWidget + AccountCard
  - TokenPanel + DeployTokenWidget
  - TxToastHost + WrongNetworkBanner
  - sqcWasmPlugin for the ML-DSA signer

Next steps after scaffolding:
  cd <project-name>
  npm install
  npm run dev
`);
}

function copyDir(src, dest) {
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const from = path.join(src, entry.name);
    const to = path.join(dest, entry.name);
    if (entry.isDirectory()) copyDir(from, to);
    else fs.copyFileSync(from, to);
  }
}

function main() {
  const name = process.argv[2];
  if (!name || name === '-h' || name === '--help') {
    usage();
    process.exit(name ? 0 : 1);
  }
  if (!/^[a-zA-Z0-9._-]+$/.test(name)) {
    console.error('Invalid project name. Use letters, numbers, ., _, -');
    process.exit(1);
  }
  if (!fs.existsSync(templateDir)) {
    console.error('Template missing at', templateDir);
    process.exit(1);
  }
  const target = path.resolve(process.cwd(), name);
  if (fs.existsSync(target)) {
    console.error(`Directory already exists: ${target}`);
    process.exit(1);
  }

  copyDir(templateDir, target);

  const pkgPath = path.join(target, 'package.json');
  const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
  pkg.name = name;
  // Pin to the connect-react release running this script and the sqc-sdk it was built against.
  const self = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  const sdkMin = /(\d+\.\d+\.\d+)/.exec(self.peerDependencies?.['@splendor/sqc-sdk'] ?? '')?.[1] ?? self.version;
  pkg.dependencies['@splendor/connect-react'] = `^${self.version}`;
  pkg.dependencies['@splendor/sqc-sdk'] = `^${sdkMin}`;
  fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
  fs.rmSync(path.join(target, 'tsconfig.tsbuildinfo'), { force: true });

  console.log(`Created ${name}/

  cd ${name}
  npm install
  npm run dev

Pin wasmSha256 before production — see README in the project.

More templates (token launchpad, tip jar, members area, NFT drop; Vite or Next.js):
  npm create splendor-dapp@latest
`);
}

main();
