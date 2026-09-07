/**
 * Sync scripts:
 * 1) Copy src/config/sqc.networks.json → config/sqc.networks.json
 * 2) Verify Go DerivationDomainMessage matches TS/JSON domain message
 * 3) Optionally refresh wasmSha256 when wasm/dist/sqc_signer.wasm exists
 * 4) Optionally refresh execSha256 when wasm/dist/wasm_exec.js exists
 *
 * Usage: node scripts/sync-config.mjs
 */
import fs from 'fs';
import path from 'path';
import crypto from 'crypto';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, '..');
const srcConfig = path.join(root, 'src/config/sqc.networks.json');
const mirrorConfig = path.join(root, 'config/sqc.networks.json');
const signerGo = path.join(root, 'signer/signer.go');
const wasmPath = path.join(root, 'wasm/dist/sqc_signer.wasm');
const execPath = path.join(root, 'wasm/dist/wasm_exec.js');

const raw = JSON.parse(fs.readFileSync(srcConfig, 'utf8'));
const wireChainId =
  raw.protocol.wireChainId ||
  raw.networks[raw.defaultNetwork]?.chainId ||
  'splendor-quantum-1';
const v = raw.protocol.derivationDomainVersion || 'v1';
const expectedDomain = `Splendor Quantum Chain\nKey derivation ${v}\nchain: ${wireChainId}`;

let configDirty = false;

// Update wasmSha256 pins when artifact present
if (fs.existsSync(wasmPath)) {
  const hash = crypto.createHash('sha256').update(fs.readFileSync(wasmPath)).digest('hex');
  let changed = false;
  for (const net of Object.values(raw.networks)) {
    const n = net;
    if (n.wasmSha256 !== hash) {
      n.wasmSha256 = hash;
      changed = true;
    }
  }
  if (changed) {
    configDirty = true;
    console.log(`[sync] updated wasmSha256 → ${hash}`);
  }
}

// Update execSha256 pins when artifact present
if (fs.existsSync(execPath)) {
  const hash = crypto.createHash('sha256').update(fs.readFileSync(execPath)).digest('hex');
  let changed = false;
  for (const net of Object.values(raw.networks)) {
    const n = net;
    if (n.execSha256 !== hash) {
      n.execSha256 = hash;
      changed = true;
    }
  }
  if (changed) {
    configDirty = true;
    console.log(`[sync] updated execSha256 → ${hash}`);
  }
}

if (configDirty) {
  fs.writeFileSync(srcConfig, JSON.stringify(raw, null, 2) + '\n');
}

// Mirror config
fs.mkdirSync(path.dirname(mirrorConfig), { recursive: true });
fs.copyFileSync(srcConfig, mirrorConfig);
console.log(`[sync] mirrored ${path.relative(root, srcConfig)} → ${path.relative(root, mirrorConfig)}`);

// Domain message sync check
const goSrc = fs.readFileSync(signerGo, 'utf8');
const m = goSrc.match(/const DerivationDomainMessage = "((?:[^"\\]|\\.)*)"/);
if (!m) {
  console.error('[sync] could not find DerivationDomainMessage in signer.go');
  process.exit(1);
}
const goDomain = m[1].replace(/\\n/g, '\n');
if (goDomain !== expectedDomain) {
  console.error('[sync] DOMAIN MISMATCH');
  console.error('  Go: ', JSON.stringify(goDomain));
  console.error('  TS: ', JSON.stringify(expectedDomain));
  console.error('Update signer.DerivationDomainMessage to match sqc.networks.json');
  process.exit(1);
}
console.log('[sync] domain message OK');
console.log('[sync] done');
