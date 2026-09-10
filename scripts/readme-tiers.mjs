#!/usr/bin/env node
/**
 * Regenerate the "The tiers" table in readme.md §20 from the tiers themselves.
 *
 *   npm run readme:tiers              rewrite the table in readme.md
 *   npm run readme:tiers -- --check   exit 1 when the committed table is stale
 *   npm run readme:tiers -- --dry-run print the table, write nothing
 *
 * WHY THIS EXISTS
 *
 * The table used to be edited by hand after a roll, so it described the tiers
 * as they were the last time somebody remembered: it said v1.0.11 while every
 * tier ran v1.0.12, and before that it carried a mainnet genesis hash of a
 * chain that had been reset away. A reader who pinned `--genesis-hash` from
 * that table followed a chain that no longer existed.
 *
 * release.mjs calls refreshReadmeTiers() after every VERIFIED rollout, so the
 * table follows the tiers. Everything between the two marker comments is
 * owned by this script; prose around the table stays hand-written.
 *
 * Nothing here is invented. Every live cell is read from the tier's own
 * /status, /block/0 and /idx/v1/health. A tier that does not answer is shown
 * as unreachable at that timestamp, not guessed.
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const BEGIN = '<!-- tiers:begin -->';
export const END = '<!-- tiers:end -->';

/**
 * Static columns are what a tier IS; live columns are what it is RUNNING.
 * The static values change only when infrastructure is renamed, which is a
 * deliberate edit here, not something to scrape.
 */
export const TIERS = [
  {
    key: 'devnet',
    name: 'Devnet',
    rpc: 'https://quantum-rpc-devnet.splendor.org',
    explorer: 'https://quantum-devnet-explorer.splendor.org',
    faucet: 'https://quantum-faucet.splendor.org',
    purpose: 'Sandbox; new releases land here first',
  },
  {
    key: 'test',
    name: 'Testnet',
    rpc: 'https://quantum-rpc-testnet.splendor.org',
    explorer: 'https://quantum-testnet-explorer.splendor.org',
    faucet: 'https://quantum-faucet.splendor.org',
    purpose: "Rehearsal; mainnet's rules plus what mainnet has not adopted yet",
  },
  {
    key: 'prod',
    name: 'Mainnet',
    rpc: 'https://quantum-rpc.splendor.org',
    explorer: 'https://quantum-explorer.splendor.org',
    faucet: null,
    purpose: 'Real balances; promoted images only',
  },
];

/** How many times /status is read per tier to catch a hostname serving two chains. */
const PROBES = 3;
const PROBE_GAP_MS = 700;
const TIMEOUT_MS = 20000;

/**
 * Rows whose value legitimately changes between two honest reads. `--check`
 * ignores them, otherwise a climbing height would make every check fail.
 */
const VOLATILE_ROWS = new Set(['Height', 'Peers reported']);

const fmtInt = new Intl.NumberFormat('en-US');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function getJSON(url) {
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(TIMEOUT_MS) });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

/**
 * `validator_addr` is a qcrypto.Address: Go marshals the [32]byte as a JSON
 * array of numbers, not the "SQ:hex" string the rest of the surface uses.
 */
function sqAddress(value) {
  if (typeof value === 'string') return value;
  if (Array.isArray(value) && value.length === 32) {
    return `SQ:${value.map((b) => Number(b).toString(16).padStart(2, '0')).join('')}`;
  }
  return null;
}

/**
 * Read a tier several times. Devnet's hostname has answered from two different
 * chains in one minute (a pipeline node and a lab chain on an out-of-tree
 * build). A single read cannot see that; three reads that disagree can.
 */
async function observe(tier) {
  const statuses = [];
  for (let i = 0; i < PROBES; i++) {
    if (i) await sleep(PROBE_GAP_MS);
    const s = await getJSON(`${tier.rpc}/status`);
    if (s) statuses.push(s);
  }
  const [genesis, idx] = await Promise.all([
    getJSON(`${tier.rpc}/block/0`),
    getJSON(`${tier.rpc}/idx/v1/health`),
  ]);
  return { statuses, genesis, idx };
}

/** Distinct values of a field across probes, as strings, in first-seen order. */
function distinct(statuses, pick) {
  const seen = [];
  for (const s of statuses) {
    const v = pick(s);
    const key = v == null ? '—' : String(v);
    if (!seen.includes(key)) seen.push(key);
  }
  return seen;
}

/**
 * One cell. A field that disagreed across probes is shown as every value seen,
 * marked mixed, so the reader knows the hostname is not one chain right now.
 */
function cell(statuses, pick, render = (v) => v) {
  const vals = distinct(statuses, pick);
  if (vals.length === 0) return '—';
  if (vals.length === 1) return vals[0] === '—' ? '—' : render(vals[0]);
  return `${vals.map((v) => (v === '—' ? '—' : render(v))).join(' / ')} (mixed — see note)`;
}

/**
 * A cell for a value that moves between two honest reads of the same chain
 * (height, peer count). Comparing probes would call every live chain "mixed",
 * so take the last read; identity fields above are what detect two chains.
 */
function latest(statuses, pick, render = (v) => v) {
  const s = statuses.at(-1);
  const v = s ? pick(s) : null;
  return v == null ? '—' : render(v);
}

function forkSchedule(forks) {
  if (!forks || typeof forks !== 'object') return '—';
  const byHeight = new Map();
  for (const [name, h] of Object.entries(forks)) {
    const height = Number(h);
    if (!byHeight.has(height)) byHeight.set(height, []);
    byHeight.get(height).push(name);
  }
  return [...byHeight.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([h, names]) => `${names.sort().map((n) => `\`${n}\``).join(', ')} at ${fmtInt.format(h)}`)
    .join('; ');
}

function consensus(s) {
  const mode = s.consensus_mode ?? '—';
  if (mode === 'bft') {
    const valid = s.head_qc_valid === true ? 'certificate valid' : 'certificate INVALID';
    const votes = s.head_qc_votes != null && s.head_quorum_required != null
      ? `, ${s.head_qc_votes} of ${s.head_quorum_required} vote${s.head_quorum_required === 1 ? '' : 's'}`
      : '';
    return `\`bft\`, ${valid}${votes}`;
  }
  return `\`${mode}\``;
}

function liveRows(observed, when) {
  const rows = [];
  const row = (label, f) => rows.push([label, ...observed.map(f)]);

  const unreachable = (o) => o.statuses.length === 0;
  const dash = `unreachable at ${when}`;

  row('Chain id', (o) => (unreachable(o) ? dash : cell(o.statuses, (s) => s.chain_id, (v) => `\`${v}\``)));
  row('Node image / commit', (o) => {
    if (unreachable(o)) return dash;
    const img = cell(o.statuses, (s) => s.image_tag, (v) => `\`sqc-node:${v}\``);
    const commit = cell(o.statuses, (s) => s.commit, (v) => `\`${String(v).slice(0, 12)}\``);
    return `${img} / ${commit}`;
  });
  row('Indexer image', (o) => {
    const tag = o.idx?.build?.image_tag;
    return tag ? `\`sqc-indexer:${tag}\`` : '—';
  });
  row('Height', (o) => (unreachable(o)
    ? dash
    : latest(o.statuses, (s) => s.height, (v) => fmtInt.format(Number(v)))));
  row('Fork schedule (`/status.forks`)', (o) => (unreachable(o)
    ? dash
    : cell(o.statuses, (s) => JSON.stringify(s.forks ?? null), (v) => forkSchedule(JSON.parse(v)))));
  row('Consensus (`/status.consensus_mode`, `head_qc_valid`)', (o) => (unreachable(o)
    ? dash
    : cell(o.statuses, (s) => JSON.stringify({
      consensus_mode: s.consensus_mode ?? null,
      head_qc_valid: s.head_qc_valid ?? null,
      head_qc_votes: s.head_qc_votes ?? null,
      head_quorum_required: s.head_quorum_required ?? null,
    }), (v) => consensus(JSON.parse(v)))));
  row('Registered validators (`validator_set_size`)', (o) => (unreachable(o)
    ? dash
    : cell(o.statuses, (s) => s.validator_set_size)));
  row('Genesis block hash (`/status.genesis_hash`)', (o) => (unreachable(o)
    ? dash
    : cell(o.statuses, (s) => s.genesis_hash, (v) => `\`${v}\``)));
  row('Genesis validator (`/block/0`)', (o) => {
    const addr = sqAddress(o.genesis?.header?.validator_addr);
    return addr ? `\`${addr}\`` : '—';
  });
  row('Peers reported', (o) => (unreachable(o) ? dash : latest(o.statuses, (s) => s.peer_count)));
  return rows;
}

function staticRows() {
  return [
    ['Purpose', ...TIERS.map((t) => t.purpose)],
    ['Node RPC', ...TIERS.map((t) => `\`${t.rpc}\``)],
    ['Indexer read API', ...TIERS.map((t) => `\`${t.rpc}/idx/v1\``)],
    ['Explorer', ...TIERS.map((t) => `\`${t.explorer}\``)],
    ['Faucet', ...TIERS.map((t) => (t.faucet ? `\`${t.faucet}\`` : 'none, by design'))],
  ];
}

/** ISO-8601 UTC to the minute, the precision the old hand-written stamps used. */
function stamp(date = new Date()) {
  return date.toISOString().replace(/:\d\d\.\d{3}Z$/, 'Z');
}

/** The full block between the markers, markers included, LF line endings. */
export async function renderTiersBlock({ now = new Date() } = {}) {
  const when = stamp(now);
  const observed = await Promise.all(TIERS.map(observe));
  const rows = [...staticRows(), ...liveRows(observed, when)];

  const lines = [
    BEGIN,
    `Live rows were read from each tier's \`/status\`, \`/block/0\` and`,
    `\`/idx/v1/health\` at **${when}** by \`npm run readme:tiers\`, which`,
    `\`npm run deploy\` runs after every verified rollout. Do not edit this table by`,
    `hand; edit \`scripts/readme-tiers.mjs\` and regenerate.`,
    '',
    `| | ${TIERS.map((t) => t.name).join(' | ')} |`,
    `|---|${TIERS.map(() => '---').join('|')}|`,
    ...rows.map((r) => `| ${r.join(' | ')} |`),
    END,
  ];
  return { block: lines.join('\n'), observed, when };
}

/** Everything in `block` except the rows and stamp that move on every honest read. */
function stable(block) {
  return block
    .split('\n')
    .filter((line) => !line.startsWith('`/idx/v1/health` at **'))
    .filter((line) => {
      const label = /^\| ([^|]+?) \|/.exec(line)?.[1]?.trim();
      return !(label && VOLATILE_ROWS.has(label));
    })
    .join('\n');
}

function splitReadme(text) {
  const eol = text.includes('\r\n') ? '\r\n' : '\n';
  const lf = text.split('\r\n').join('\n');
  const start = lf.indexOf(BEGIN);
  const stop = lf.indexOf(END);
  if (start < 0 || stop < 0 || stop < start) {
    throw new Error(`readme.md has no ${BEGIN} … ${END} block to regenerate`);
  }
  return {
    eol,
    before: lf.slice(0, start),
    current: lf.slice(start, stop + END.length),
    after: lf.slice(stop + END.length),
  };
}

/**
 * Rewrite the table in place. Returns true when the file changed.
 * Preserves the file's line endings: readme.md is CRLF, and converting the
 * whole file would turn a ten-line change into a two-thousand-line diff.
 */
export async function refreshReadmeTiers({ cwd = process.cwd(), dryRun = false, check = false } = {}) {
  const path = resolve(cwd, 'readme.md');
  const text = readFileSync(path, 'utf8');
  const { eol, before, current, after } = splitReadme(text);
  const { block, observed, when } = await renderTiersBlock();

  const down = TIERS.filter((_, i) => observed[i].statuses.length === 0).map((t) => t.name);

  if (dryRun) {
    process.stdout.write(block + '\n');
    return current !== block;
  }
  if (check) {
    if (down.length) {
      throw new Error(`cannot check the table: ${down.join(', ')} unreachable at ${when}`);
    }
    if (stable(current) !== stable(block)) {
      throw new Error('readme.md §20 is stale — run `npm run readme:tiers` and commit the result');
    }
    return false;
  }
  if (current === block) return false;
  writeFileSync(path, (before + block + after).split('\n').join(eol));
  if (down.length) {
    console.log(`  ! ${down.join(', ')} did not answer; the table says so rather than guessing.`);
  }
  return true;
}

// ── command line ────────────────────────────────────────────────────────────
const invokedDirectly = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedDirectly) {
  const flags = new Set(process.argv.slice(2));
  const mode = flags.has('--check') ? 'check' : flags.has('--dry-run') ? 'dry-run' : 'write';
  try {
    const changed = await refreshReadmeTiers({ dryRun: mode === 'dry-run', check: mode === 'check' });
    if (mode === 'check') console.log('✓ readme.md §20 matches the tiers');
    else if (mode === 'dry-run') console.log(changed ? '(readme.md would change)' : '(readme.md already matches)');
    else console.log(changed ? '✓ readme.md §20 regenerated' : '· readme.md §20 already matches the tiers');
  } catch (e) {
    console.error(`✗ ${e.message ?? e}`);
    process.exit(1);
  }
}
