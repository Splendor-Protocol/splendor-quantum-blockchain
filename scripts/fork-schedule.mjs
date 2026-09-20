#!/usr/bin/env node
/**
 * Report the EFFECTIVE protocol-upgrade schedule of every tier, read from the
 * tiers themselves.
 *
 *   npm run forks                 print the schedule table
 *   npm run forks -- --check      exit 1 on a divergence that matters
 *   npm run forks -- --json       machine-readable, for a report build
 *
 * WHY THIS EXISTS
 *
 * The repository kept saying "`bft_rounds_v1` is unscheduled on every public
 * tier". That sentence was written once and then aged: devnet was reset onto a
 * genesis that schedules it at height 1, so the claim was false for a third of
 * the fleet while it sat in the readme and in a published validation report.
 * Installing a release does not activate the rules it ships — genesis does —
 * and the only honest source for "which rules is this chain enforcing" is the
 * chain's own /status.forks.
 *
 * So nothing here is written down in advance. The fork NAMES come from
 * `var Known` in core/forks/forks.go — the same list the node's
 * Schedule.Validate accepts a genesis against — and every HEIGHT comes from
 * the tier. A fork this build knows but nowhere in the fleet is still listed,
 * as unscheduled.
 *
 * WHAT --check ENFORCES
 *
 * One invariant, the one the tier table already promises in prose: testnet is
 * "mainnet's rules plus what mainnet has not adopted yet". A consensus- or
 * execution-affecting rule that mainnet enforces and testnet does not means
 * testnet is not rehearsing mainnet, and the next promotion is untested.
 * Application modules (exchange, bridge, lending…) are deliberately tier-local
 * and are reported but not enforced — and a name this script has not classified
 * as one or the other is a reported problem, enforced as a consensus rule
 * meanwhile, rather than a silent tier-local default.
 */
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { TIERS } from './readme-tiers.mjs';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PROBES = 2;
const TIMEOUT_MS = 20000;

/**
 * Rules that change how a block is validated or executed. These are the ones
 * a tier must not enforce ahead of the tier that rehearses it.
 */
const CONSENSUS_RULES = new Set([
  'multi_validator', 'validators_v1', 'validators_admission_v1', 'bft_rounds_v1',
  'min_fee_v1', 'modules_v2', 'module_revert_v1', 'vm_revert_v1',
  'vm_gas_v1', 'vm_call_v1', 'verify_identity_v1', 'verify_v1',
  // x402_settle_v1 writes a settlement leaf into the state root. It reads like
  // a payments feature and is not one: a tier that enforces it and a tier that
  // does not compute different roots for the same transaction.
  'x402_settle_v1',
]);

/**
 * Application modules: a routed call surface whose absence changes no
 * consensus outcome, so a tier may carry one and another tier not.
 *
 * This list exists so the two sets TOGETHER cover every name in forks.Known.
 * "Not in CONSENSUS_RULES" used to mean "application module" by default, which
 * is the one classification mistake that hides a real divergence: a consensus
 * fork added to Go and not added here would have been reported as tier-local
 * and quietly excluded from the invariant. Now an unlisted name is an error
 * (see classify) — counted as consensus for the invariant AND reported — so a
 * missing classification cannot mask a divergence.
 */
const APPLICATION_MODULES = new Set([
  'qrc20_v2', 'qrc1155_v1', 'qrc721_v1', 'qrc4626_v1',
  'exchange_v1', 'bridge_v1', 'lending_v1', 'oracle_v1',
  'multisig_v1', 'governance_v1',
]);

/**
 * classify returns 'consensus', 'application', or null when the name is in
 * neither list or (a contradiction) in both. null is a reported error, never a
 * silent tier-local default.
 */
export function classify(fork) {
  const consensus = CONSENSUS_RULES.has(fork);
  const application = APPLICATION_MODULES.has(fork);
  if (consensus === application) return null; // in neither, or in both
  return consensus ? 'consensus' : 'application';
}

/**
 * classifyForks labels every name and reports which ones could not be labelled.
 * `consensus` is true for an unclassified name too: the invariant is applied to
 * it until someone says otherwise, because the failure that matters is a
 * consensus rule quietly exempted from the check. Split out from main so this
 * is testable without probing a tier.
 */
export function classifyForks(names) {
  const kinds = new Map();
  const unclassified = [];
  for (const f of [...names].sort()) {
    const kind = classify(f);
    if (!kind) unclassified.push(f);
    kinds.set(f, { kind: kind || 'unclassified', consensus: kind !== 'application' });
  }
  return { kinds, unclassified };
}

/**
 * Every fork name this build understands, read from core/forks/forks.go.
 *
 * The authority is `var Known`, not "every string constant in the file":
 * forks.isKnown is what Schedule.Validate refuses a genesis against, so Known
 * is exactly the set a node accepts, and a constant declared but deliberately
 * not yet in Known (the file reserves names before their gate exists) is a
 * name a node REFUSES. Scanning constants instead listed those as merely
 * unscheduled, and would have swept up any future non-fork string constant
 * added to the file, or missed a typed or differently formatted declaration.
 *
 * Resolution is two steps — the identifier list, then identifier → value — and
 * every step that cannot be parsed throws. A partial list is the dangerous
 * output: a name missing from it is reported only if some tier schedules it,
 * and not at all if none does. So there is no partial-list path here.
 */
export function knownForks() {
  const path = 'core/forks/forks.go';
  const raw = readFileSync(resolve(ROOT, path), 'utf8');
  // Drop comments before parsing: a name inside prose is not a declaration.
  // No fork value contains "//" or "/*", so this cannot eat one.
  const src = raw.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');

  // Step 1: identifier -> value, for every string constant declaration.
  // Accepts `const X = "y"`, a grouped `X = "y"`, and a typed `X Name = "y"`.
  const values = new Map();
  const decl = /^[ \t]*(?:const[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)(?:[ \t]+[A-Za-z_][A-Za-z0-9_.]*)?[ \t]*=[ \t]*"([^"]*)"[ \t]*\r?$/gm;
  for (const m of src.matchAll(decl)) {
    const [, ident, value] = m;
    const prev = values.get(ident);
    if (prev !== undefined && prev !== value) {
      throw new Error(`${path}: ${ident} is declared twice with different values (${prev}, ${value})`);
    }
    values.set(ident, value);
  }

  // Step 2: the Known list, which says which of those the build honours.
  const block = src.match(/\bvar[ \t]+Known\b[^{]*\{([^}]*)\}/);
  if (!block) {
    throw new Error(`${path}: could not find "var Known = []string{...}", the list this tool reads. ` +
      'Refusing to guess the fork set from the file\'s string constants.');
  }
  const entries = block[1].split(',').map((s) => s.trim()).filter((s) => s.length > 0);
  if (entries.length === 0) throw new Error(`${path}: var Known is empty`);

  const names = new Set();
  for (const entry of entries) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(entry)) {
      throw new Error(`${path}: var Known entry ${JSON.stringify(entry)} is not a bare constant identifier. ` +
        'This tool resolves identifiers to their declared values; it cannot read this shape.');
    }
    const value = values.get(entry);
    if (value === undefined) {
      throw new Error(`${path}: var Known names ${entry}, but no string constant ${entry} = "..." was parsed. ` +
        'Either the constant left this file or its declaration is a shape this tool does not read.');
    }
    if (!/^[a-z][a-z0-9_]*$/.test(value)) {
      throw new Error(`${path}: ${entry} = ${JSON.stringify(value)} is not a fork-name shape (lower_snake_case)`);
    }
    if (names.has(value)) throw new Error(`${path}: fork name ${JSON.stringify(value)} appears twice in var Known`);
    names.add(value);
  }
  return names;
}

async function getJSON(url) {
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(TIMEOUT_MS) });
    return res.ok ? await res.json() : null;
  } catch {
    return null;
  }
}

/**
 * Read a tier twice. A hostname that answers from two chains reports two
 * genesis hashes, and a schedule read from such a hostname means nothing.
 */
async function observe(tier) {
  const seen = [];
  for (let i = 0; i < PROBES; i++) {
    const s = await getJSON(`${tier.rpc}/status`);
    if (s) seen.push(s);
  }
  if (seen.length === 0) return { key: tier.key, name: tier.name, reachable: false };
  const genesis = new Set(seen.map((s) => s.genesis_hash));
  const s = seen[0];
  return {
    key: tier.key,
    name: tier.name,
    reachable: true,
    mixed: genesis.size > 1,
    genesis: s.genesis_hash,
    height: s.height,
    version: s.node_version,
    commit: s.commit,
    validators: s.validator_set_size,
    forks: s.forks || {},
  };
}

/** active | ahead | unscheduled, from the tier's own head. */
function state(tier, fork) {
  const at = tier.forks[fork];
  if (at === undefined) return { s: 'unscheduled', cell: '—' };
  if (typeof tier.height === 'number' && at > tier.height) return { s: 'ahead', cell: `@${at} (pending)` };
  return { s: 'active', cell: at === 1 ? 'genesis' : `@${at}` };
}

async function main() {
  const argv = process.argv.slice(2);
  const known = knownForks();
  const tiers = await Promise.all(TIERS.map(observe));
  const live = tiers.filter((t) => t.reachable);

  // A name a tier schedules that this build does not know is the dangerous
  // direction: the node would refuse that genesis outright.
  const unknown = new Set();
  for (const t of live) for (const f of Object.keys(t.forks)) if (!known.has(f)) unknown.add(`${t.name}:${f}`);

  // An unclassified name is counted as consensus so the invariant still
  // applies to it, and listed below so the gap gets closed.
  const { kinds, unclassified } = classifyForks(known);
  const rows = [...kinds].map(([fork, k]) => ({
    fork,
    kind: k.kind,
    consensus: k.consensus,
    per: Object.fromEntries(live.map((t) => [t.key, state(t, fork)])),
  }));

  if (argv.includes('--json')) {
    console.log(JSON.stringify({ observed: new Date().toISOString(), tiers, rows, unclassified }, null, 2));
  } else {
    console.log(`\nEffective protocol schedules, read live ${new Date().toISOString()}\n`);
    for (const t of tiers) {
      if (!t.reachable) { console.log(`  ${t.name.padEnd(8)} unreachable`); continue; }
      console.log(`  ${t.name.padEnd(8)} ${String(t.version || '?').padEnd(8)} ${String(t.commit || '').slice(0, 7)}  head ${t.height}  validators ${t.validators}  genesis ${String(t.genesis).slice(0, 12)}…${t.mixed ? '  ** MIXED: two chains on one hostname **' : ''}`);
    }
    const w = Math.max(...rows.map((r) => r.fork.length));
    console.log(`\n  ${'rule'.padEnd(w)}  ${live.map((t) => t.name.padEnd(14)).join('')}`);
    for (const r of rows) {
      // · = application module, tier-local by design. ? = not classified in
      // this script, so it is treated as a consensus rule until someone says.
      const mark = r.kind === 'application' ? '·' : r.kind === 'unclassified' ? '?' : ' ';
      console.log(`  ${r.fork.padEnd(w)}${mark} ${live.map((t) => r.per[t.key].cell.padEnd(14)).join('')}`);
    }
    console.log('\n  · = application module (tier-local by design). Blank = consensus or execution rule.');
    if (unclassified.length) console.log('  ? = NOT CLASSIFIED in scripts/fork-schedule.mjs; enforced as a consensus rule meanwhile.');
  }

  // The invariant: testnet rehearses mainnet, so it cannot lag mainnet on a rule.
  const test = live.find((t) => t.key === 'test');
  const prod = live.find((t) => t.key === 'prod');
  const lagging = [];
  if (test && prod) {
    for (const r of rows) {
      if (!r.consensus) continue;
      if (r.per.prod.s === 'active' && r.per.test.s !== 'active') lagging.push(r.fork);
    }
  }
  const problems = [];
  if (lagging.length) problems.push(`testnet does not enforce ${lagging.join(', ')} while mainnet does — testnet is not rehearsing mainnet's rules`);
  if (unclassified.length) {
    problems.push(`${unclassified.join(', ')} ${unclassified.length === 1 ? 'is' : 'are'} in forks.Known but ` +
      'in neither CONSENSUS_RULES nor APPLICATION_MODULES in this script (or in both). ' +
      'Classified as consensus meanwhile; add each one to the set it belongs to');
  }
  for (const u of unknown) problems.push(`${u} is scheduled on a tier but unknown to this build`);
  for (const t of live) if (t.mixed) problems.push(`${t.name} answered from more than one chain`);
  if (tiers.some((t) => !t.reachable)) problems.push(`unreachable: ${tiers.filter((t) => !t.reachable).map((t) => t.name).join(', ')}`);

  if (problems.length && !argv.includes('--json')) {
    console.log('\n  Divergences:');
    for (const p of problems) console.log(`    - ${p}`);
    console.log('');
  }
  if (argv.includes('--check') && problems.length) process.exitCode = 1;
}

// Run only when this file is the entry point. process.argv[1] is absent under
// `node --input-type=module -e`, which is how the parser is exercised without
// touching a tier; pathToFileURL(undefined) threw there.
const entry = process.argv[1] ? pathToFileURL(process.argv[1]).href : '';
if (import.meta.url === entry) await main();
