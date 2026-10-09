import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { TEMPLATES, createCatalog, generateProject, instantiateTemplate, validateSpec } from '@splendor/dapp-spec';

const here = dirname(fileURLToPath(import.meta.url));
export const FRAMEWORKS = ['vite', 'next'];
export const NETWORKS = ['testnet', 'devnet', 'mainnet', 'local'];

/** Versions to pin come from this package's own package.json, so every release pins the matching SDK. */
export function pinnedVersions() {
  const pkg = JSON.parse(readFileSync(join(here, '..', 'package.json'), 'utf8'));
  const strip = (v) => String(v ?? '').replace(/^[^\d]*/, '');
  const connectReact = strip(pkg.devDependencies?.['@splendor/connect-react']);
  const sqcSdk = strip(pkg.devDependencies?.['@splendor/sqc-sdk']);
  if (!connectReact || !sqcSdk) throw new Error('create-splendor-dapp: package.json is missing pinned SDK versions');
  return { connectReact, sqcSdk, self: pkg.version };
}

export function loadCatalog() {
  const file = join(here, '..', 'catalog.json');
  if (!existsSync(file)) throw new Error('create-splendor-dapp: catalog.json missing (run npm run build)');
  return createCatalog(JSON.parse(readFileSync(file, 'utf8')).widgets);
}

/** Parse `name --template x --framework y --network z --set block.prop=value`. */
export function parseArgs(rawArgv) {
  const argv = rawArgv.flatMap((a) => (/^--[a-z]+=/.test(a) ? [a.slice(0, a.indexOf('=')), a.slice(a.indexOf('=') + 1)] : [a]));
  const out = { name: undefined, template: undefined, framework: undefined, network: undefined, set: [], yes: false, help: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const val = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '-h' || a === '--help') out.help = true;
    else if (a === '-y' || a === '--yes') out.yes = true;
    else if (a === '-t' || a === '--template') out.template = val();
    else if (a === '-f' || a === '--framework') out.framework = val();
    else if (a === '-n' || a === '--network') out.network = val();
    else if (a === '--set') out.set.push(val());
    else if (!a.startsWith('-') && !out.name) out.name = a;
    else throw new Error(`Unknown option ${a}`);
  }
  return out;
}

/** Build the project files for a template; throws with a readable message on bad input. */
export function buildProject({ name, template, framework, network, set = [] }) {
  const t = TEMPLATES.find((x) => x.id === template);
  if (!t) throw new Error(`Unknown template "${template}". Choose one of: ${TEMPLATES.map((x) => x.id).join(', ')}`);
  if (!FRAMEWORKS.includes(framework)) throw new Error(`--framework must be ${FRAMEWORKS.join(' or ')}`);
  if (!NETWORKS.includes(network)) throw new Error(`--network must be one of ${NETWORKS.join(', ')}`);
  const spec = instantiateTemplate(t);
  spec.name = name;
  spec.network = network;
  for (const s of set) {
    const m = /^([A-Za-z0-9_-]+)\.([A-Za-z0-9_]+)=(.*)$/.exec(s);
    if (!m) throw new Error(`--set expects block.prop=value, got "${s}"`);
    const block = spec.pages.flatMap((p) => p.blocks).find((b) => b.id === m[1]);
    if (!block) throw new Error(`--set: no block "${m[1]}" in template ${t.id}`);
    block.props[m[2]] = m[3];
  }
  const catalog = loadCatalog();
  const v = validateSpec(spec, { catalog });
  if (!v.ok) {
    const lines = v.issues.map((i) => `  ${i.path}: ${i.message}`).join('\n');
    const hints = t.setup.map((s) => `  --set ${s.blockId}.${s.prop}=<${s.label}>`).join('\n');
    throw new Error(`The template needs more input:\n${lines}${hints ? `\nProvide:\n${hints}` : ''}`);
  }
  const { connectReact, sqcSdk } = pinnedVersions();
  return generateProject(v.spec, { framework, catalog, versions: { connectReact, sqcSdk } });
}

export function writeProject(targetDir, files) {
  const target = resolve(targetDir);
  if (existsSync(target) && readdirSync(target).length) throw new Error(`Directory is not empty: ${target}`);
  for (const [rel, contents] of Object.entries(files)) {
    const file = join(target, rel);
    if (!file.startsWith(target)) throw new Error(`Refusing to write outside the project: ${rel}`);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, contents);
  }
  return target;
}

export { TEMPLATES };
