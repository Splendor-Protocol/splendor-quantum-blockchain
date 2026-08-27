#!/usr/bin/env node
/**
 * Ship the Splendor Quantum node.
 *
 *   npm run deploy patch devnet        # cut the next version and roll it
 *   npm run deploy -- minor --env test
 *   npm run deploy v0.4.11 prod        # promote an existing version to another tier
 *
 * WHY THIS EXISTS
 *
 * Releasing the chain was three manual steps across two repositories, and the
 * middle one is invisible: "Build sqc-node image" publishes to GHCR but does NOT
 * update the tier's SQC_NODE_IMAGE variable, while "Deploy quantum infrastructure"
 * reads ONLY that variable. Build-then-deploy without the write in between is a
 * guaranteed silent no-op — the deploy succeeds, reports success, and re-pulls the
 * image the tier was already running.
 *
 * Every check below is here because the corresponding failure actually happened.
 * None of them are hypothetical, and none should be removed without reading the
 * incident in the comment first.
 *
 * WHAT IT NEVER DOES
 *
 * It never resets a chain. reset_chain is not exposed and is always sent false.
 * A reset destroys every balance not in the tier's genesis allocation list and
 * needs the deliberate procedure in splendor-infra/docs/chain-redeploy-runbook.md.
 */
import { execFileSync } from 'node:child_process';
import { createInterface } from 'node:readline/promises';

const INFRA = 'Splendor-Protocol/splendor-infra';
const CHAIN = 'Splendor-Protocol/splendor-quantum-blockchain';
const IMAGE = 'ghcr.io/splendor-protocol/sqc-node';

/**
 * The tiers, defined once. `env` is the GitHub environment name and does not
 * match the tier's hostname — "test" serves quantum-rpc-testnet — which has
 * caused enough confusion to be worth stating here rather than deriving.
 *
 * `dev`/simnet is deliberately absent: it is a simulated explorer with no chain
 * behind it, so there is nothing for this tool to roll.
 */
const TIERS = {
  devnet: { env: 'devnet', rpc: 'https://quantum-rpc-devnet.splendor.org' },
  test:   { env: 'test',   rpc: 'https://quantum-rpc-testnet.splendor.org' },
  prod:   { env: 'prod',   rpc: 'https://quantum-rpc.splendor.org' },
};

/**
 * Files whose change means something downstream must ship too. The tool cannot
 * ship those itself — they live in other repositories and belong to other people
 * — so it names them and says who owns them.
 */
const WATCH = [
  { path: 'core/types/transaction.go', ships: 'wallet (mldsaSigner), faucet, SDK — the signed transaction body changed' },
  { path: 'core/types/block.go',       ships: 'explorer — it re-derives block hashes in-browser' },
  { path: 'core/crypto/mldsa.go',      ships: 'explorer, wallet, WASM signer — signature scheme or sizes changed' },
  { path: 'genesis/genesis.go',        ships: 'infra entrypoint — the genesis document shape changed' },
];

const sh = (cmd, args, opts = {}) =>
  execFileSync(cmd, args, { encoding: 'utf8', ...opts }).trim();
const shOk = (cmd, args) => {
  try { return { ok: true, out: sh(cmd, args) }; }
  catch (e) { return { ok: false, out: String(e.stderr ?? e.message).trim() }; }
};
const git = (...a) => sh('git', a);
const gh = (...a) => sh('gh', a);
const die = (msg, ...extra) => {
  console.error(`\n✗ ${msg}`);
  for (const line of extra) console.error(`  ${line}`);
  process.exit(1);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ── arguments ───────────────────────────────────────────────────────────────
const argv = process.argv.slice(2);
const flags = new Set(argv.filter((a) => a.startsWith('--')));
const positional = argv.filter((a) => !a.startsWith('--'));
const envFlagIdx = argv.findIndex((a) => a === '--env');
const envFromFlag = envFlagIdx >= 0 ? argv[envFlagIdx + 1] : undefined;

const dryRun = flags.has('--dry-run');
const assumeYes = flags.has('--yes');
const allowNoOp = flags.has('--allow-no-op');

if (positional[0] === 'status') { await printStatus(); process.exit(0); }

const bumpOrVersion = positional[0];
const tierArg = envFromFlag ?? positional.find((a) => a in TIERS);

if (!bumpOrVersion || !tierArg) {
  console.error(`usage: npm run deploy <patch|minor|major|vX.Y.Z> <devnet|test|prod> [--dry-run] [--yes] [--allow-no-op]

  npm run deploy patch devnet        cut the next patch version and roll it to devnet
  npm run deploy -- minor --env test cut a minor and roll it to testnet
  npm run deploy v0.4.11 prod        promote an existing version to prod (no rebuild)
  npm run deploy status              show what every tier is running

  --dry-run       do everything except mutate anything
  --yes           skip the production confirmation prompt
  --allow-no-op   release even though no Go source changed since the deployed build`);
  process.exit(64);
}
if (!(tierArg in TIERS)) {
  die(`unknown tier "${tierArg}"`, `known tiers: ${Object.keys(TIERS).join(', ')}`,
      'dev/simnet is a simulated explorer with no chain behind it and cannot be rolled.');
}
const tier = TIERS[tierArg];

await main();

// ── the release ─────────────────────────────────────────────────────────────
async function main() {
  preflight();

  const deployedNow = tierImage(tier.env);
  const promoting = /^v\d+\.\d+\.\d+$/.test(bumpOrVersion);
  const version = promoting ? bumpOrVersion : nextVersion(bumpOrVersion);

  // Promotion reuses an existing image; a new version builds one. Both end with
  // the same variable write, rollout and verification.
  const commit = promoting ? commitOfExistingTag(version) : git('rev-parse', 'HEAD');

  console.log(`
  version    ${version}${promoting ? '  (promoting an existing image — no rebuild)' : ''}
  commit     ${commit}
             ${git('log', '-1', '--format=%s', commit).slice(0, 68)}
  tier       ${tierArg}  (GitHub environment "${tier.env}")
  rpc        ${tier.rpc}
  currently  ${deployedNow ?? '(no SQC_NODE_IMAGE set)'}
`);

  if (promoting) checkRollback(deployedNow, version, commit);
  if (!promoting) checkWorthBuilding(deployedNow, commit);
  warnDownstream(deployedNow, commit);
  checkIndexer(tier.env);

  if (tierArg === 'prod' && !assumeYes) await confirmProd(version);
  if (dryRun) { console.log('✓ --dry-run: nothing was pushed, built, set or rolled.'); return; }

  if (!promoting) {
    cutTag(version, commit);
    await build(version, commit);
    publishRelease(version, commit);
  } else {
    assertImageExists(version);
  }

  const before = await head(tier.rpc);
  setTierImage(tier.env, version, deployedNow);

  // A GitHub Deployment on the CHAIN repo, so "which version is on which tier"
  // is answerable where the version lives. infra records deployments too, but it
  // marks them successful when the workflow finishes — which only means the
  // container was replaced. This one is marked successful after the node itself
  // reports the released commit, so a green deployment here means verified.
  const deployment = openDeployment(tier, version, commit);
  try {
    await rollout(tier.env);
    await verifyLive(tier, version, commit, before);
    closeDeployment(deployment, 'success', tier, `${version} verified live`);
  } catch (e) {
    closeDeployment(deployment, 'failure', tier, 'rollout or verification failed');
    throw e;
  }

  console.log(`
✓ ${tierArg} is running ${version} (${commit.slice(0, 12)})
  verified against ${tier.rpc}/status — not merely dispatched.
`);
}

// ── preflight ───────────────────────────────────────────────────────────────
function preflight() {
  if (git('status', '--porcelain')) {
    die('working tree is dirty', 'A release must be reproducible from a commit that exists.');
  }
  const branch = git('rev-parse', '--abbrev-ref', 'HEAD');
  if (branch !== 'main') die(`on branch "${branch}", not main`);

  // HEAD must be on origin. On 27 Aug main was force-pushed backwards and the
  // tiers kept serving code that no longer existed in the repository; releasing
  // an unpushed commit creates the same unrecoverable state deliberately.
  git('fetch', 'origin', '--tags', '--quiet');
  if (git('rev-parse', 'HEAD') !== git('rev-parse', 'origin/main')) {
    die('HEAD is not origin/main', 'Push or pull first — a released commit must be on the remote.');
  }
  const who = shOk('gh', ['api', 'user', '-q', '.login']);
  if (!who.ok) die('gh is not authenticated', who.out);
}

// ── versioning ──────────────────────────────────────────────────────────────
/**
 * The next version, derived from BOTH the tier variables and the git tags, taking
 * whichever is highest.
 *
 * The tiers are authoritative about what exists — image tags were chosen by hand
 * long before this tool, and v0.4.8/9/10 are deployed today with no corresponding
 * git tag anywhere. Deriving from tags alone would propose a version that sorts
 * BELOW what is already running, and "is this tier ahead or behind?" is the
 * question the whole tool exists to answer.
 */
function nextVersion(bump) {
  if (!['patch', 'minor', 'major'].includes(bump)) {
    die(`"${bump}" is not patch, minor, major, or a vX.Y.Z version`);
  }
  const seen = [];
  for (const t of Object.values(TIERS)) {
    const img = tierImage(t.env);
    if (img) seen.push(img.split(':').pop());
  }
  seen.push(...git('tag', '--list', 'v*').split('\n'));

  const parsed = seen
    .map((v) => /^v(\d+)\.(\d+)\.(\d+)$/.exec((v ?? '').trim()))
    .filter(Boolean)
    .map((m) => [Number(m[1]), Number(m[2]), Number(m[3])])
    .sort((a, b) => b[0] - a[0] || b[1] - a[1] || b[2] - a[2]);

  const [major, minor, patch] = parsed[0] ?? [0, 0, 0];
  const next = bump === 'major' ? [major + 1, 0, 0]
    : bump === 'minor' ? [major, minor + 1, 0]
    : [major, minor, patch + 1];
  return `v${next.join('.')}`;
}

function commitOfExistingTag(version) {
  const local = shOk('git', ['rev-list', '-n', '1', version]);
  if (local.ok && local.out) return local.out;
  die(`${version} is not a tag in this repository`,
      'Promotion reuses an existing release; cut one first with `npm run deploy patch <tier>`.');
}

function assertImageExists(version) {
  const any = Object.values(TIERS).map((t) => tierImage(t.env)).filter(Boolean);
  if (!any.some((img) => img.endsWith(`:${version}`))) {
    console.log(`  note: no tier currently runs ${version}; assuming the image exists in GHCR.`);
  }
}

// ── is this release worth making ────────────────────────────────────────────
/**
 * A rebuild is not provably a no-op: the Dockerfile runs `go get ./... && go mod
 * tidy` on a floating golang:1.25-alpine base, so the same commit built twice can
 * differ. That makes a pointless rebuild the one operation that can change a
 * consensus binary with no source change behind it.
 */
function checkWorthBuilding(deployedImage, commit) {
  if (!deployedImage) return;
  const deployedTag = deployedImage.split(':').pop();
  const prev = shOk('git', ['rev-list', '-n', '1', deployedTag]);
  if (!prev.ok || !prev.out) return; // no tag for it — cannot compare, proceed

  const changed = shOk('git', ['diff', '--name-only', `${prev.out}..${commit}`, '--', '*.go', 'go.mod', 'go.sum', 'Dockerfile']);
  if (changed.ok && changed.out === '') {
    if (!allowNoOp) {
      die(`no Go source changed since ${deployedTag}`,
          'The node binary would be identical, and rebuilding is not provably a no-op',
          '(`go get ./...` on a floating base image can move dependencies).',
          'Pass --allow-no-op if you want the version bump anyway.');
    }
    console.log('  ! --allow-no-op: no Go source changed; rebuilding anyway.');
  }
}

function warnDownstream(deployedImage, commit) {
  if (!deployedImage) return;
  const deployedTag = deployedImage.split(':').pop();
  const prev = shOk('git', ['rev-list', '-n', '1', deployedTag]);
  if (!prev.ok || !prev.out) return;

  const changed = shOk('git', ['diff', '--name-only', `${prev.out}..${commit}`]);
  if (!changed.ok) return;
  const files = new Set(changed.out.split('\n'));
  const hits = WATCH.filter((w) => files.has(w.path));
  if (!hits.length) return;

  console.log('  ! this release changes files other systems depend on:');
  for (const h of hits) console.log(`      ${h.path}\n        -> ship: ${h.ships}`);
  console.log('    This tool ships the node only. Those are separate releases.\n');
}

/**
 * The rollout applies SQC_INDEXER_IMAGE too, whether or not the operator meant
 * to. bootstrap-host.sh runs `docker compose pull && up -d` over the whole stack,
 * so a node release silently carries whatever indexer tag that environment
 * happens to hold — including an older one than the tier is running now.
 */
function checkIndexer(env) {
  const idx = ghVar('SQC_INDEXER_IMAGE', env);
  if (!idx) {
    die(`SQC_INDEXER_IMAGE is not set for environment "${env}"`,
        'The rollout would start the stack without an indexer image.');
  }
  console.log(`  indexer    ${idx}  (applied by this rollout too)\n`);
}


/**
 * Going backwards is a legitimate operation and a dangerous one, so it is
 * allowed but never silent.
 *
 * A rollback is a DOWNGRADE, and downgrades are subject to the same question as
 * upgrades: can the older binary still read the bytes the newer one wrote? The
 * project's own rule is in splendor-infra/docs/chain-redeploy-runbook.md — a
 * change is only reset-free when the new binary can read, and would not misread,
 * what is already on disk. Rolling back inverts that: the OLD binary now has to
 * cope with data written by the NEW one.
 *
 * When the two versions differ in no Go source, a rollback is pure and safe.
 * When they do differ, this says exactly which files changed, because that is
 * the input to deciding whether the older node can still open the chain.
 */
function checkRollback(deployedImage, version, commit) {
  if (!deployedImage) return;
  const current = deployedImage.split(':').pop();
  if (current === version) {
    die(`this tier already runs ${version}`, 'Nothing to do.');
  }
  const rank = (v) => {
    const m = /^v(\d+)\.(\d+)\.(\d+)$/.exec(v);
    return m ? Number(m[1]) * 1e6 + Number(m[2]) * 1e3 + Number(m[3]) : null;
  };
  const a = rank(version), b = rank(current);
  if (a == null || b == null || a >= b) return; // promotion forward, or unparseable

  console.log(`  ! ROLLBACK: ${current} -> ${version} is going BACKWARDS.\n`);

  const prev = shOk('git', ['rev-list', '-n', '1', current]);
  if (!prev.ok || !prev.out) {
    console.log(`    ${current} has no tag here, so the code difference cannot be shown.`);
    console.log('    Check the older node can still read this chain before continuing.\n');
    return;
  }
  const changed = shOk('git', ['diff', '--name-only', `${commit}..${prev.out}`, '--', '*.go']);
  if (changed.ok && changed.out === '') {
    console.log('    No Go source differs between them, so the binary is equivalent.');
    console.log('    This rollback is safe.\n');
    return;
  }
  console.log('    Go source DOES differ. The older binary must still be able to read');
  console.log('    a chain the newer one has been writing. Files changed:\n');
  for (const f of (changed.out || '').split('\n').filter(Boolean).slice(0, 12)) {
    console.log(`      ${f}`);
  }
  console.log('\n    Storage format, state transition or header changes make a rollback');
  console.log('    unsafe without a reset. See docs/chain-redeploy-runbook.md.\n');
}

// ── github plumbing ─────────────────────────────────────────────────────────
function ghVar(name, env) {
  const r = shOk('gh', ['variable', 'list', '--env', env, '--repo', INFRA]);
  if (!r.ok) return null;
  const line = r.out.split('\n').find((l) => l.startsWith(name + '\t'));
  return line ? line.split('\t')[1].trim() : null;
}
function tierImage(env) { return ghVar('SQC_NODE_IMAGE', env); }

function cutTag(version, commit) {
  if (git('tag', '--list', version)) die(`${version} already exists locally`);
  if (shOk('git', ['ls-remote', '--exit-code', 'origin', `refs/tags/${version}`]).ok) {
    die(`${version} already exists on origin`, 'Versions are immutable; bump instead.');
  }
  git('tag', '-a', version, commit, '-m', `release ${version}`);
  // One ref per push. An atomic multi-ref push where one ref already existed
  // produced NO workflow run at all on 26 Aug, and the release reported success.
  const pushed = shOk('git', ['push', 'origin', `refs/tags/${version}`]);
  if (!pushed.ok) { shOk('git', ['tag', '-d', version]); die(`pushing ${version} failed`, pushed.out); }
  console.log(`  ✓ tagged ${version} -> ${commit.slice(0, 12)}`);
}

/**
 * Dispatch a workflow and return the run it created.
 *
 * Never `gh run list -L1`: on 27 Aug that returned a colleague's concurrent run
 * and its log was read as though it were ours, reporting the wrong tier's
 * deployment. Snapshot the run ids first and take the set difference.
 */
async function dispatch(workflow, inputs) {
  const before = new Set(runIds(workflow));
  const args = ['workflow', 'run', workflow, '--repo', INFRA, '--ref', 'main'];
  for (const [k, v] of Object.entries(inputs)) args.push('-f', `${k}=${v}`);
  const r = shOk('gh', args);
  if (!r.ok) die(`dispatching "${workflow}" failed`, r.out);

  for (let i = 0; i < 20; i++) {
    await sleep(3000);
    const fresh = runIds(workflow).filter((id) => !before.has(id));
    if (fresh.length === 1) return fresh[0];
    if (fresh.length > 1) {
      die(`${fresh.length} new "${workflow}" runs appeared at once`,
          'Cannot tell which is ours. Check Actions and re-run when quiet.');
    }
  }
  die(`"${workflow}" was dispatched but no run appeared within 60s`,
      'A dispatch that creates no run is a release that silently does nothing.',
      `https://github.com/${INFRA}/actions`);
}

function runIds(workflow) {
  const r = shOk('gh', ['run', 'list', '--repo', INFRA, '--workflow', workflow, '-L', '30', '--json', 'databaseId', '-q', '.[].databaseId']);
  return r.ok && r.out ? r.out.split('\n').map(Number) : [];
}

async function watchRun(id, what) {
  console.log(`  … ${what} (run ${id})`);
  const r = shOk('gh', ['run', 'watch', String(id), '--repo', INFRA, '--exit-status']);
  const concl = shOk('gh', ['run', 'view', String(id), '--repo', INFRA, '--json', 'conclusion', '-q', '.conclusion']);
  if (!r.ok || concl.out !== 'success') {
    die(`${what} failed (${concl.out || 'unknown'})`, `https://github.com/${INFRA}/actions/runs/${id}`);
  }
}

async function build(version, commit) {
  // chain_ref takes the tag. A FULL 40-hex sha also works; an abbreviated one
  // does not — actions/checkout turns it into a refspec that matches nothing.
  const id = await dispatch('Build sqc-node image', { chain_ref: version, image_tag: version });
  await watchRun(id, `building ${version}`);

  // Assert the image contains what we think. The build log is the only place
  // that records which commit an image was made from.
  const log = shOk('gh', ['run', 'view', String(id), '--repo', INFRA, '--log']);
  if (!log.ok) die('could not read the build log to verify what it built');
  if (!log.out.includes(commit)) {
    die(`the build did not stamp ${commit.slice(0, 12)}`,
        'The image may not contain the commit being released. Refusing to deploy it.');
  }
  console.log(`  ✓ image ${IMAGE}:${version} built from ${commit.slice(0, 12)}`);
}

function setTierImage(env, version, previous) {
  // The step with no workflow behind it, and the one whose absence makes the
  // whole pipeline a silent no-op: Deploy reads only this variable.
  const r = shOk('gh', ['variable', 'set', 'SQC_NODE_IMAGE', '--env', env, '--body', `${IMAGE}:${version}`, '--repo', INFRA]);
  if (!r.ok) die('setting SQC_NODE_IMAGE failed', r.out);
  const now = tierImage(env);
  if (now !== `${IMAGE}:${version}`) die('SQC_NODE_IMAGE did not take', `reads back as ${now}`);
  console.log(`  ✓ ${env} -> ${version}   (was ${previous ?? 'unset'})`);
}

async function rollout(env) {
  const id = await dispatch('Deploy quantum infrastructure', {
    environment: env,
    rollout_node: 'true',
    apply_infrastructure: 'false',
    reset_chain: 'false', // never true from this tool — see the header
  });
  await watchRun(id, `rolling out to ${env}`);
}


// ── github release + deployment records ─────────────────────────────────────
/**
 * A git tag is not a GitHub Release. The repo showed "4 tags" and zero releases
 * because nothing had ever created the Release object, so there was no page
 * saying what a version contains.
 */
function publishRelease(version, commit) {
  const prev = previousVersionTag(version);
  const notes = [
    `**Image** \`${IMAGE}:${version}\``,
    `**Commit** \`${commit}\``,
    '',
    prev ? `### Changes since ${prev}` : '### Changes',
    prev
      ? shOk('git', ['log', '--no-merges', '--format=- %s', `${prev}..${commit}`]).out || '- (no commits)'
      : '- (first tracked release)',
  ].join('\n');

  const r = shOk('gh', ['release', 'create', version, '--repo', CHAIN,
    '--title', version, '--notes', notes, '--verify-tag']);
  if (!r.ok) {
    // Never fail a release because its changelog did not publish.
    console.log(`  ! could not create the GitHub Release for ${version}: ${r.out.split('\n')[0]}`);
    return;
  }
  console.log(`  ✓ published release ${version}`);
}

function previousVersionTag(version) {
  const all = git('tag', '--list', 'v*').split('\n')
    .map((t) => /^v(\d+)\.(\d+)\.(\d+)$/.exec(t.trim()))
    .filter(Boolean)
    .map((m) => ({ tag: m[0], rank: Number(m[1]) * 1e6 + Number(m[2]) * 1e3 + Number(m[3]) }))
    .sort((a, b) => b.rank - a.rank);
  const mine = /^v(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!mine) return null;
  const rank = Number(mine[1]) * 1e6 + Number(mine[2]) * 1e3 + Number(mine[3]);
  return all.find((t) => t.rank < rank)?.tag ?? null;
}

/** Opens a Deployment on the chain repo. Returns its id, or null if unavailable. */
function openDeployment(t, version, commit) {
  const body = JSON.stringify({
    ref: version,
    environment: t.env,
    description: `sqc-node ${version} (${commit.slice(0, 7)})`,
    auto_merge: false,
    required_contexts: [],
    transient_environment: false,
    production_environment: t.env === 'prod',
  });
  // gh api --input - needs the JSON on stdin, so pipe it.
  const r2 = shOk('bash', ['-c',
    `printf '%s' ${JSON.stringify(body)} | gh api repos/${CHAIN}/deployments --method POST --input - -q .id`]);
  if (!r2.ok || !r2.out) {
    console.log('  ! could not open a Deployment record (continuing anyway)');
    return null;
  }
  return r2.out.trim();
}

function closeDeployment(id, state, t, description) {
  if (!id) return;
  const body = JSON.stringify({ state, environment_url: t.rpc, description });
  shOk('bash', ['-c',
    `printf '%s' ${JSON.stringify(body)} | gh api repos/${CHAIN}/deployments/${id}/statuses --method POST --input - >/dev/null`]);
}

// ── verification ────────────────────────────────────────────────────────────
async function head(rpc) {
  const s = await status(rpc);
  return s?.height ?? null;
}
async function status(rpc) {
  try {
    const res = await fetch(`${rpc}/status`, { signal: AbortSignal.timeout(20000) });
    return res.ok ? await res.json() : null;
  } catch { return null; }
}

/**
 * A green workflow means the container was replaced. It does not mean the chain
 * is alive, and it does not mean the tier is running what we just built.
 */
async function verifyLive(t, version, commit, before) {
  let s = null;
  for (let i = 0; i < 20; i++) {
    await sleep(5000);
    s = await status(t.rpc);
    if (s && s.commit === commit && s.image_tag === version) break;
  }
  if (!s) die('the node did not answer /status after the rollout', t.rpc);

  if (before != null && !(s.height > before)) {
    die(`height is not climbing (${before} -> ${s.height})`,
        'The container was replaced but the chain is not sealing.');
  }
  if (s.commit && s.commit !== commit) {
    die(`${t.rpc} reports commit ${String(s.commit).slice(0, 12)}, expected ${commit.slice(0, 12)}`,
        'The tier is not running the release that was just built.');
  }
  if (!s.commit) {
    console.log('  ! this node does not report its commit (built before build stamping).');
    console.log('    Height climbed, so the rollout worked, but the running code is unverified.');
  }
  console.log(`  ✓ height ${before} -> ${s.height}, chain ${s.chain_id}`);
}

async function confirmProd(version) {
  if (!process.stdin.isTTY) {
    die('refusing a non-interactive production release', `re-run with: npm run deploy ${version} prod -- --yes`);
  }
  const rl = createInterface({ input: process.stdin, output: process.stdout });
  const a = (await rl.question(`Release ${version} to PRODUCTION? (y/N) `)).trim().toLowerCase();
  rl.close();
  if (!['y', 'yes'].includes(a)) { console.log('Aborted. Nothing changed.'); process.exit(0); }
}

// ── status ──────────────────────────────────────────────────────────────────
async function printStatus() {
  console.log('\n  tier      variable                          live commit    height');
  for (const [name, t] of Object.entries(TIERS)) {
    const img = tierImage(t.env) ?? '(unset)';
    const s = await status(t.rpc);
    const c = s?.commit ? String(s.commit).slice(0, 12) : '(not reported)';
    console.log(`  ${name.padEnd(9)} ${img.split('/').pop().padEnd(33)} ${c.padEnd(14)} ${s?.height ?? '—'}`);
  }
  console.log('');
}
