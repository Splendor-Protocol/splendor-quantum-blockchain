#!/usr/bin/env node
/**
 * Ship the Splendor Quantum node.
 *
 *   npm run deploy patch devnet        # cut the next version and roll it
 *   npm run deploy -- minor --env test
 *   npm run deploy v0.4.11 prod        # promote an existing version to another tier
 *
 * A bump on a commit that is already released promotes that release instead of
 * cutting a new one: the same commit never gets two versions (releasedVersionAt).
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
 * WHAT DEPLOY NEVER DOES
 *
 * `deploy` never resets a chain. reset_chain is always sent false from a
 * rollout. A wipe is the separate `reset` / `wipe` command. See
 * splendor-infra/docs/chain-redeploy-runbook.md.
 */
import { execFileSync } from 'node:child_process';
import { createInterface } from 'node:readline/promises';

const INFRA = 'Splendor-Protocol/splendor-infra';
const CHAIN = 'Splendor-Protocol/splendor-quantum-blockchain';
const IMAGE = 'ghcr.io/splendor-protocol/sqc-node';
/** Mainnet's native supply in base units: 26 billion SPLD, 18 decimals. */
const MAINNET_SUPPLY = 26_000_000_000n * 10n ** 18n;

/**
 * The tiers, defined once. `env` is the GitHub environment name and does not
 * match the tier's hostname — "test" serves quantum-rpc-testnet — which has
 * caused enough confusion to be worth stating here rather than deriving.
 *
 * `dev`/simnet is deliberately absent: it is a simulated explorer with no chain
 * behind it, so there is nothing for this tool to roll.
 */
/**
 * What people actually type. The GitHub environment is "test" but the host is
 * quantum-rpc-TESTNET, and "mainnet" is what everyone calls prod — so both were
 * rejected with a usage dump, which is a footgun the naming mismatch guarantees.
 */
const ALIASES = {
  testnet: 'test', tn: 'test',
  mainnet: 'prod', main: 'prod', production: 'prod',
  dn: 'devnet',
};

const TIERS = {
  devnet: { env: 'devnet', rpc: 'https://quantum-rpc-devnet.splendor.org' },
  test:   { env: 'test',   rpc: 'https://quantum-rpc-testnet.splendor.org' },
  prod:   { env: 'prod',   rpc: 'https://quantum-rpc.splendor.org' },
};
const HUMAN_TIER = { devnet: 'devnet', test: 'testnet', prod: 'mainnet' };
const PROD_RESET_PHRASE = 'reset prod chain';

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
  { path: 'sdk/sqc-sdk/wasm/dist/sqc_signer.wasm', ships: 'wallet extension — it pins this exact digest; its sync workflow opens the PR on its next push to main, merge it before the wallet ships' },
];

const sh = (cmd, args, opts = {}) =>
  execFileSync(cmd, args, { encoding: 'utf8', ...opts }).trim();
/**
 * A probe whose failure is expected and handled. stderr is CAPTURED, not
 * inherited: execFileSync passes a child's stderr straight to the terminal by
 * default, so `git rev-list <tag>` on a tag that does not exist printed two
 * `fatal: ambiguous argument` lines in the middle of a release that was
 * proceeding correctly. A handled path must not look like a crash.
 */
const shOk = (cmd, args) => {
  try { return { ok: true, out: sh(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'] }) }; }
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
/** vX.Y.Z as a sortable number, or null when the string is not a version. */
const rank = (v) => {
  const m = /^v(\d+)\.(\d+)\.(\d+)$/.exec(v);
  return m ? Number(m[1]) * 1e6 + Number(m[2]) * 1e3 + Number(m[3]) : null;
};

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
if (positional[0] === 'reset' || positional[0] === 'wipe') {
  await runReset(positional.slice(1));
  process.exit(0);
}

const bumpOrVersion = positional[0];
const rawTier = envFromFlag ?? positional.find((a) => a in TIERS || a in ALIASES);
const tierArg = rawTier && (ALIASES[rawTier] ?? rawTier);
if (rawTier && ALIASES[rawTier]) {
  console.log(`  (tier "${rawTier}" -> "${tierArg}" — the GitHub environment name)`);
}

if (!bumpOrVersion || !tierArg) {
  console.error(`usage: npm run deploy <patch|minor|major|vX.Y.Z> <devnet|test|prod> [--dry-run] [--yes] [--allow-no-op]

  npm run deploy patch devnet        cut the next patch version and roll it to devnet
  npm run deploy -- minor --env test cut a minor and roll it to testnet
  npm run deploy v0.4.11 prod        promote an existing version to prod (no rebuild)
  npm run deploy status              show what every tier is running
  npm run reset <devnet|test|prod>   wipe a tier: snapshot balances, ask, reset_chain=true

  --dry-run       do everything except mutate anything
  --yes           skip the production confirmation prompt
  --allow-no-op   release even though no Go source changed since the deployed build,
                  or HEAD is already released (forces a fresh version and rebuild)

  A bump on a commit that is already released promotes that release instead of
  cutting a new one, so the same commit never gets two versions.`);
  process.exit(64);
}
if (!(tierArg in TIERS)) {
  const near = Object.keys({ ...TIERS, ...ALIASES }).filter((k) => k.startsWith(String(tierArg)[0] ?? ''));
  die(`unknown tier "${tierArg}"`,
      `tiers: ${Object.keys(TIERS).join(', ')}   (aliases: ${Object.keys(ALIASES).join(', ')})`,
      near.length ? `did you mean: ${near.join(', ')}?` : '',
      'dev/simnet is a simulated explorer with no chain behind it and cannot be rolled.');
}
const tier = TIERS[tierArg];

await main();

// ── the release ─────────────────────────────────────────────────────────────
async function main() {
  preflight();

  const deployedNow = tierImage(tier.env);
  const explicit = /^v\d+\.\d+\.\d+$/.test(bumpOrVersion);
  if (!explicit && !['patch', 'minor', 'major'].includes(bumpOrVersion)) {
    die(`"${bumpOrVersion}" is not patch, minor, major, or a vX.Y.Z version`);
  }

  // A bump on a commit that is already released is a promotion in disguise.
  const already = explicit || allowNoOp ? null : releasedVersionAt(git('rev-parse', 'HEAD'));
  if (already) {
    console.log(`\n  HEAD is already released as ${already}; promoting that image instead of`);
    console.log('  cutting a new version. (--allow-no-op forces a fresh version and rebuild.)');
  }
  const promoting = explicit || Boolean(already);
  const version = explicit ? bumpOrVersion : (already ?? nextVersion(bumpOrVersion));

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

  // Resolved once, then shared: all three checks want the same "what is this
  // tier actually running?" answer, and it may cost an RPC round trip.
  const prevCommit = await deployedCommit(tier, deployedNow);

  if (promoting) checkRollback(deployedNow, version, commit, prevCommit);
  if (!promoting) checkWorthBuilding(deployedNow, prevCommit, commit);
  warnDownstream(deployedNow, prevCommit, commit);
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

/**
 * The highest version tag on `commit` that has a published GitHub Release, or
 * null when the commit has never been released.
 *
 * On 2 Sep, `deploy:test` run straight after `deploy:devnet` proposed v0.6.4 for
 * the commit devnet had just verified as v0.6.3. `patch` always cut, and the
 * "worth building" check diffs against the TARGET tier (still on v0.6.1), so
 * nothing objected: the same commit would have been rebuilt — not provably
 * identical, see checkWorthBuilding — under a second number, and testnet would
 * not have run the image devnet had tested. A commit is released once; every
 * tier after the first gets that image.
 *
 * A tag with no Release is ignored: it is the leftover of an interrupted cut
 * (the tag is pushed before the build runs) and has no image behind it.
 */
function releasedVersionAt(commit) {
  const tags = git('tag', '--list', 'v*', '--points-at', commit).split('\n')
    .map((t) => t.trim())
    .filter((t) => rank(t) != null)
    .sort((a, b) => rank(b) - rank(a));
  for (const t of tags) {
    const rel = shOk('gh', ['release', 'view', t, '--repo', CHAIN, '--json', 'publishedAt', '-q', '.publishedAt']);
    if (rel.ok && rel.out) return t;
  }
  return null;
}

/**
 * Confirm the image being promoted actually exists.
 *
 * This used to infer it from what the tiers happened to be running, which said
 * "assuming the image exists" for a version that had definitely been built —
 * because the tier that built it had since been rolled back. A published release
 * is the real record that a version was built and pushed.
 */
function assertImageExists(version) {
  const rel = shOk('gh', ['release', 'view', version, '--repo', CHAIN, '--json', 'publishedAt', '-q', '.publishedAt']);
  if (rel.ok && rel.out) {
    console.log(`  image      ${IMAGE}:${version}  (released ${rel.out.slice(0, 10)})`);
    return;
  }
  const onTier = Object.values(TIERS).map((t) => tierImage(t.env)).filter(Boolean)
    .some((img) => img.endsWith(`:${version}`));
  if (onTier) {
    console.log(`  image      ${IMAGE}:${version}  (in use by another tier)`);
    return;
  }
  die(`${version} has no release and no tier is running it`,
      'Refusing to point a tier at an image that may not exist.',
      `Cut it first: npm run deploy <patch|minor|major> <tier>`);
}

// ── is this release worth making ────────────────────────────────────────────
/**
 * A rebuild is not provably a no-op: the Dockerfile runs `go get ./... && go mod
 * tidy` on a floating golang:1.25-alpine base, so the same commit built twice can
 * differ. That makes a pointless rebuild the one operation that can change a
 * consensus binary with no source change behind it.
 */
/**
 * The commit a tier is actually running, for the checks that diff it against
 * the candidate.
 *
 * A tier's image tag is NOT reliably a git tag: v0.4.8/9/10 and v0.5.0 were
 * chosen by hand before this tool existed and have no tag anywhere. Every check
 * that resolved the tag itself therefore failed open — `git rev-list <tag>`
 * errored, the function returned, and the release proceeded with no "did any Go
 * source actually change?" and no downstream warning at all. Guards that
 * silently do nothing are worse than no guards, because the output looks the
 * same as a clean run.
 *
 * So ask the node: /status reports the commit it was built from, which is more
 * authoritative than a tag that may never have been created. The tag is still
 * tried first — it works offline, and for a tier that is down.
 */
async function deployedCommit(t, deployedImage) {
  if (!deployedImage) return null;
  const tag = deployedImage.split(':').pop();
  const viaTag = shOk('git', ['rev-list', '-n', '1', tag]);
  if (viaTag.ok && viaTag.out) return viaTag.out;

  const reported = (await status(t.rpc))?.commit;
  if (!reported) {
    console.log(`  ! ${tag} has no git tag here and ${t.rpc} reports no commit,`);
    console.log('    so the change checks below cannot run.\n');
    return null;
  }
  if (!shOk('git', ['cat-file', '-e', `${reported}^{commit}`]).ok) {
    console.log(`  ! the tier reports commit ${String(reported).slice(0, 12)}, which is not in this`);
    console.log('    repository — fetch it, or the tier runs code from elsewhere. Checks skipped.\n');
    return null;
  }
  console.log(`  · ${tag} has no git tag; using the commit the tier reports (${reported.slice(0, 12)})`);
  return reported;
}

function checkWorthBuilding(deployedImage, prevCommit, commit) {
  if (!deployedImage || !prevCommit) return;
  const deployedTag = deployedImage.split(':').pop();

  const changed = shOk('git', ['diff', '--name-only', `${prevCommit}..${commit}`, '--', '*.go', 'go.mod', 'go.sum', 'Dockerfile']);
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

function warnDownstream(deployedImage, prevCommit, commit) {
  if (!deployedImage || !prevCommit) return;

  const changed = shOk('git', ['diff', '--name-only', `${prevCommit}..${commit}`]);
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
function checkRollback(deployedImage, version, commit, prevCommit) {
  if (!deployedImage) return;
  const current = deployedImage.split(':').pop();
  if (current === version) {
    die(`this tier already runs ${version}`, 'Nothing to do.');
  }
  const a = rank(version), b = rank(current);
  if (a == null || b == null || a >= b) return; // promotion forward, or unparseable

  console.log(`  ! ROLLBACK: ${current} -> ${version} is going BACKWARDS.\n`);

  if (!prevCommit) {
    console.log(`    ${current} cannot be resolved to a commit here, so the code difference`);
    console.log('    cannot be shown. Check the older node can still read this chain first.\n');
    return;
  }
  const changed = shOk('git', ['diff', '--name-only', `${commit}..${prevCommit}`, '--', '*.go']);
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

/**
 * Watch a run and say what it is doing.
 *
 * `gh run watch` prints nothing useful when its output is piped, so a rollout sat
 * silent for minutes and looked hung — and the run URL it printed is no help to
 * anyone without access to the repository. This polls the jobs API and reports
 * the step that is currently running, the fraction complete, and elapsed time.
 */
async function watchRun(id, what) {
  const url = `https://github.com/${INFRA}/actions/runs/${id}`;
  const started = Date.now();
  const tty = process.stdout.isTTY;
  let lastPrinted = '';

  console.log(`  … ${what}`);
  if (!tty) console.log(`    ${url}`);

  for (;;) {
    const raw = shOk('gh', ['api', `repos/${INFRA}/actions/runs/${id}/jobs`]);
    let jobs = [];
    if (raw.ok) { try { jobs = JSON.parse(raw.out).jobs ?? []; } catch { /* transient */ } }

    const steps = jobs.flatMap((j) => j.steps ?? []);
    const done = steps.filter((st) => st.status === 'completed').length;
    const running = steps.find((st) => st.status === 'in_progress');
    const pct = steps.length ? Math.round((done / steps.length) * 100) : 0;

    const elapsed = Math.round((Date.now() - started) / 1000);
    const clock = `${String(Math.floor(elapsed / 60)).padStart(2, '0')}:${String(elapsed % 60).padStart(2, '0')}`;
    const now = running?.name ?? (jobs.some((j) => j.status === 'queued') ? 'waiting for a runner' : 'finishing');
    const line = `    [${clock}] ${String(pct).padStart(3)}%  ${now}`.slice(0, 110);

    if (tty) {
      process.stdout.write('\r' + line.padEnd(Math.max(lastPrinted.length, line.length)));
    } else if (line.replace(/\[\d\d:\d\d\]/, '') !== lastPrinted.replace(/\[\d\d:\d\d\]/, '')) {
      console.log(line); // no TTY: one line per step change, not per poll
    }
    lastPrinted = line;

    const allDone = jobs.length > 0 && jobs.every((j) => j.status === 'completed');
    if (allDone) {
      if (tty) process.stdout.write('\r' + ' '.repeat(lastPrinted.length) + '\r');
      const bad = jobs.find((j) => j.conclusion !== 'success' && j.conclusion !== 'skipped');
      if (bad) {
        const failed = (bad.steps ?? []).find((st) => st.conclusion === 'failure');
        die(`${what} failed at "${failed?.name ?? bad.name}" (${bad.conclusion})`, url);
      }
      console.log(`    done in ${clock}`);
      return;
    }
    if (elapsed > 45 * 60) die(`${what} has run for 45 minutes; giving up watching`, url);
    await sleep(4000);
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
  const mine = rank(version);
  if (mine == null) return null;
  return git('tag', '--list', 'v*').split('\n')
    .map((t) => t.trim())
    .filter((t) => rank(t) != null && rank(t) < mine)
    .sort((a, b) => rank(b) - rank(a))[0] ?? null;
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

// ── reset / wipe ────────────────────────────────────────────────────────────
/**
 * Erase a tier and mint a fresh genesis. Deploy cannot do this; reset_chain
 * is always false on a rollout. This command is the only way out of the
 * blockchain repo.
 *
 * Order: snapshot balances into SQC_GENESIS_ALLOC_ADDRESS (so known wallets
 * survive), typed confirmation, dispatch, then verify height collapsed and
 * started climbing. The validator address is stripped from the alloc list
 * because the entrypoint already funds it via SQC_GENESIS_SUPPLY — leaving
 * it in fails the boot after the wipe.
 *
 * This is a balance snapshot, not an EBS snapshot. Disk undo is still a
 * manual AWS step; this tool has no AWS credentials on purpose.
 *
 * An optional vX.Y.Z means "come back up on that image". The wipe boots
 * whatever SQC_NODE_IMAGE says, so resetting without it silently re-genesises
 * the tier on the OLD build — the image is written only after the typed
 * confirmation, so an abort leaves the tier pointing where it did.
 */
async function runReset(args) {
  const wantAll = flags.has('--all');
  const noSnapshot = flags.has('--no-snapshot');
  const version = args.find((a) => /^v\d+\.\d+\.\d+$/.test(a));
  const raw = envFromFlag ?? args.find((a) => a in TIERS || a in ALIASES);
  const tierName = raw && (ALIASES[raw] ?? raw);
  if (!tierName || !(tierName in TIERS)) {
    console.error(`usage: npm run reset <devnet|test|prod> [vX.Y.Z] [--dry-run] [--all] [--no-snapshot]

  npm run reset:devnet             snapshot curated wallets, ask, wipe devnet
  npm run reset:test               …testnet
  npm run reset:prod               …mainnet (must type "${PROD_RESET_PHRASE}")
  npm run reset -- prod v1.0.1     …and come back up on that image
  npm run reset -- prod v1.0.1 --no-snapshot
                                   wipe using SQC_GENESIS_ALLOC_ADDRESS as already set
  npm run reset -- test --all      snapshot every indexer-known account
  --allow-supply-mismatch          prod only: wipe even though genesis would not be
                                   exactly 26B on the treasury with the validator at 0

  Without a version the tier re-genesises on the image it already runs.
  --yes is ignored. A reset always asks.`);
    process.exit(64);
  }
  if (raw && ALIASES[raw]) {
    console.log(`  (tier "${raw}" -> "${tierName}" — the GitHub environment name)`);
  }

  const t = TIERS[tierName];
  const human = HUMAN_TIER[tierName];
  const who = shOk('gh', ['api', 'user', '-q', '.login']);
  if (!who.ok) die('gh is not authenticated', who.out);

  const img = tierImage(t.env);
  const live = await status(t.rpc);
  const before = live?.height ?? null;
  console.log(`
  tier       ${human}  (GitHub environment "${t.env}")
  rpc        ${t.rpc}
  image      ${img ?? '(no SQC_NODE_IMAGE set)'}${version ? `  ->  ${IMAGE}:${version}` : '  (unchanged by this reset)'}
  currently  height ${before ?? '—'}   chain ${live?.chain_id ?? '—'}
`);
  if (version) assertImageExists(version);

  if (noSnapshot) {
    console.log('  using SQC_GENESIS_ALLOC_ADDRESS as already set — not re-read from the live chain');
    guardSupply(tierName, printGenesisTotal(t.env));
  } else {
    console.log('  snapshotting balances that must survive the wipe…');
    await snapshotBalances(t, wantAll);
  }
  if (dryRun) {
    console.log('✓ --dry-run: snapshot printed, nothing was written or wiped.');
    return;
  }
  await dropValidatorFromAlloc(t);
  // The alloc list is final now (snapshot taken, validator stripped): this is
  // the genesis the wipe will mint, so this is where its total is checked.
  if (!noSnapshot) guardSupply(tierName, printGenesisTotal(t.env));

  await confirmReset(tierName, human, before, live?.chain_id, version);
  if (version) setTierImage(t.env, version, img);
  const inputs = {
    environment: t.env,
    rollout_node: 'true',
    apply_infrastructure: 'false',
    reset_chain: 'true',
  };
  if (tierName === 'prod') inputs.reset_confirm = PROD_RESET_PHRASE;

  const id = await dispatch('Deploy quantum infrastructure', inputs);
  await watchRun(id, `resetting ${human}`);
  await verifyReset(t, before, version);
  console.log(`
✓ ${human} was wiped and is sealing a new genesis${version ? ` on ${version}` : ''}
  verified against ${t.rpc}/status — height collapsed, then climbed.
  Redeploy that tier's explorer Worker (it caches the old genesis).
  Re-fund the faucet if it was not in the snapshot.
`);
}

/**
 * What the entrypoint will mint: SQC_GENESIS_SUPPLY to the validator (0 means
 * no validator line at all) plus every SQC_GENESIS_ALLOC_ADDRESS entry, and
 * total supply is their sum -- the node derives it, nothing declares it.
 */
function printGenesisTotal(env) {
  const supply = ghVar('SQC_GENESIS_SUPPLY', env);
  if (supply == null || !/^\d+$/.test(supply)) die(`SQC_GENESIS_SUPPLY is not a number for environment "${env}"`, `reads as ${supply ?? '(unset)'}`);
  const entries = (ghVar('SQC_GENESIS_ALLOC_ADDRESS', env) ?? '').split(',').map((e) => e.trim()).filter(Boolean);
  const extra = entries.reduce((sum, e) => sum + BigInt((e.split('=')[1] ?? '').trim() || '0'), 0n);
  const total = BigInt(supply) + extra;
  const validator = supply === '0' ? 'validator 0 (no validator allocation)' : `validator ${supply}`;
  console.log(`  genesis    ${validator}${entries.length ? ` + ${entries.length} configured allocation(s)` : ' and no configured allocations'}`);
  console.log(`             total ${total} base units = ${total / 10n ** 18n} SPLD`);
  return { supply: BigInt(supply), entries, total };
}

/**
 * Mainnet mints exactly 26B, once. Two prod resets on 2 Sep 2026 carried a
 * stray 1B entry on top of the 26B validator line and minted 27B; nothing
 * looked at the sum. The model since is a validator that holds 0 and one
 * treasury allocation carrying the whole 26B, and any other total is a
 * misconfiguration until someone says otherwise on the command line.
 */
function guardSupply(tierName, g) {
  if (tierName !== 'prod') return;
  if (g.total === MAINNET_SUPPLY && g.supply === 0n) return;
  const why = g.total !== MAINNET_SUPPLY
    ? `mainnet genesis would mint ${g.total / 10n ** 18n} SPLD, not ${MAINNET_SUPPLY / 10n ** 18n}`
    : `mainnet genesis would credit the validator ${g.supply / 10n ** 18n} SPLD; the validator signs and holds 0`;
  if (flags.has('--allow-supply-mismatch')) {
    console.log(`  ! --allow-supply-mismatch: ${why}. Proceeding anyway.`);
    return;
  }
  die(why,
      'SQC_GENESIS_SUPPLY must be 0 and SQC_GENESIS_ALLOC_ADDRESS must sum to exactly 26e27 for prod.',
      'Fix the variables in splendor-infra, or pass --allow-supply-mismatch if you really mean it.');
}

async function snapshotBalances(t, wantAll) {
  const pairs = wantAll ? await snapshotAllAccounts(t) : await snapshotCuratedAccounts(t);
  if (!pairs.length) die('nothing to snapshot', 'no funded addresses on this tier.');
  const out = pairs.join(',');
  if (out.length > 48000) {
    die(`allocation string is ${out.length} bytes; GitHub variables cap at 48 KB`,
        'Re-run with the curated list (drop --all), or raise a dust floor by hand.');
  }
  if (dryRun) {
    console.log(`  ${pairs.length} account(s) captured (${out.length} bytes) — not written (--dry-run)`);
    return;
  }
  const r = shOk('gh', ['variable', 'set', 'SQC_GENESIS_ALLOC_ADDRESS', '--env', t.env, '--body', out, '--repo', INFRA]);
  if (!r.ok) die('setting SQC_GENESIS_ALLOC_ADDRESS failed', r.out);
  console.log(`  SQC_GENESIS_ALLOC_ADDRESS set for ${t.env} — ${pairs.length} account(s)`);
}

async function readBalance(rpc, addr) {
  try {
    const res = await fetch(`${rpc}/balance/${addr}`, { signal: AbortSignal.timeout(15000) });
    if (!res.ok) return null;
    const body = await res.json();
    const bal = body?.balance;
    return typeof bal === 'string' && /^\d+$/.test(bal) ? bal : null;
  } catch {
    return null;
  }
}

async function snapshotCuratedAccounts(t) {
  const raw = shOk('gh', ['api', `repos/${INFRA}/contents/scripts/accounts.txt`, '-q', '.content']);
  if (!raw.ok || !raw.out) die('could not read accounts.txt from splendor-infra', raw.out);
  const text = Buffer.from(raw.out.replace(/\s/g, ''), 'base64').toString('utf8');
  const pairs = [];
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const [addr, ...rest] = trimmed.split(/\s+/);
    if (!addr.startsWith('SQ:')) continue;
    const label = rest.join(' ') || addr;
    const bal = await readBalance(t.rpc, addr);
    if (bal == null) {
      die(`could not read balance for ${addr} (${label})`,
          'Refusing a partial snapshot — that account would be wiped.');
    }
    if (bal === '0') {
      console.log(`  · zero, skipped: ${label}`);
      continue;
    }
    console.log(`  ✓ ${label} = ${bal}`);
    pairs.push(`${addr}=${bal}`);
  }
  return pairs;
}

async function snapshotAllAccounts(t) {
  let health;
  try {
    const res = await fetch(`${t.rpc}/idx/v1/health`, { signal: AbortSignal.timeout(20000) });
    health = res.ok ? await res.json() : null;
  } catch { health = null; }
  const checkpoint = Number(health?.checkpoint);
  if (!Number.isFinite(checkpoint)) {
    die('indexer /idx/v1/health is unreachable',
        '--all needs a healthy indexer. Omit --all to snapshot the curated list.');
  }
  const live = await status(t.rpc);
  const head = live?.height;
  if (head == null) die(`node ${t.rpc}/status is unreachable — cannot verify indexer lag`);
  const lag = head - checkpoint;
  if (lag > 25) {
    die(`indexer is ${lag} blocks behind the node (checkpoint ${checkpoint}, head ${head})`,
        'Wait for it to catch up, or omit --all.');
  }
  console.log(`  indexer healthy: checkpoint ${checkpoint}, node head ${head} (lag ${lag})`);

  const pairs = [];
  let cursor = '';
  let pages = 0;
  for (;;) {
    const url = new URL(`${t.rpc}/idx/v1/accounts/alloc`);
    url.searchParams.set('limit', '500');
    url.searchParams.set('min_balance', '1');
    if (cursor) url.searchParams.set('after', cursor);
    let body;
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(120000) });
      if (!res.ok) die(`${url} failed (${res.status})`, 'A partial --all snapshot is refused.');
      body = await res.json();
    } catch (e) {
      die(`${url} failed`, String(e.message ?? e));
    }
    const alloc = body.alloc ?? '';
    if (alloc) pairs.push(...alloc.split(',').filter(Boolean));
    pages += 1;
    console.log(`  page ${pages}: ${body.scanned ?? '?'} scanned, ${body.accounts ?? '?'} with a balance (running total ${pairs.length})`);
    if (body.has_more !== true) break;
    if (!body.next_cursor) die('has_more=true but no next_cursor — refusing to loop');
    cursor = body.next_cursor;
  }
  return pairs;
}

/**
 * `validator_addr` is a qcrypto.Address, and Go marshals a [32]byte as a JSON
 * ARRAY of numbers — not the "SQ:hex" string the rest of the surface uses. An
 * address IS those 32 bytes, so the string form is a hex encode of them.
 */
function sqAddress(value) {
  if (typeof value === 'string') return value;
  if (Array.isArray(value) && value.length === 32) {
    return `SQ:${value.map((b) => b.toString(16).padStart(2, '0')).join('')}`;
  }
  return '';
}

async function dropValidatorFromAlloc(t) {
  let validator = '';
  try {
    const res = await fetch(`${t.rpc}/block/latest`, { signal: AbortSignal.timeout(20000) });
    if (res.ok) {
      const block = await res.json();
      validator = sqAddress(block?.header?.validator_addr);
    }
  } catch { /* live node may already be down */ }
  if (!validator) {
    console.log('  ! could not read the validator address; not stripping it from the alloc list');
    return;
  }
  const current = ghVar('SQC_GENESIS_ALLOC_ADDRESS', t.env);
  if (!current) return;
  const kept = current.split(',').filter((p) => !p.toLowerCase().startsWith(`${validator.toLowerCase()}=`));
  if (kept.length === current.split(',').length) return;
  console.log(`  · dropped validator ${validator} from genesis alloc (entrypoint already funds it)`);
  if (dryRun) return;
  const r = shOk('gh', ['variable', 'set', 'SQC_GENESIS_ALLOC_ADDRESS', '--env', t.env, '--body', kept.join(','), '--repo', INFRA]);
  if (!r.ok) die('stripping the validator from SQC_GENESIS_ALLOC_ADDRESS failed', r.out);
}

async function confirmReset(tierName, human, height, chainId, version) {
  if (!process.stdin.isTTY) {
    die('refusing a non-interactive chain reset',
        'A reset destroys every balance not in SQC_GENESIS_ALLOC_ADDRESS.');
  }
  console.log(`
  This will ERASE ${human}.
  Height now: ${height ?? '—'}   chain: ${chainId ?? '—'}
  Comes back: ${version ? `${IMAGE}:${version}` : 'the image this tier already runs'}
  Survives:   addresses just written to SQC_GENESIS_ALLOC_ADDRESS
  Dies:       every other balance, all history, contracts, indexer, faucet float
  Kept:       the validator key (the workflow does not rotate it)
  Not taken:  an EBS volume snapshot — do that in AWS first if you may need to undo this.
`);
  const rl = createInterface({ input: process.stdin, output: process.stdout });
  const typed = (await rl.question(`Type ${human} to continue: `)).trim();
  if (typed !== human) {
    rl.close();
    console.log('Aborted. The balance snapshot was written; the chain was not wiped.');
    process.exit(0);
  }
  if (tierName === 'prod') {
    const phrase = (await rl.question(`Type exactly "${PROD_RESET_PHRASE}": `)).trim();
    if (phrase !== PROD_RESET_PHRASE) {
      rl.close();
      console.log('Aborted. The chain was not wiped.');
      process.exit(0);
    }
  }
  rl.close();
}

async function verifyReset(t, before, version) {
  let firstLow = null;
  for (let i = 0; i < 24; i++) {
    await sleep(5000);
    const s = await status(t.rpc);
    if (!s) continue;
    const collapsed = before == null || s.height < before;
    if (!collapsed) continue;
    // A wipe that came back on the previous build is the failure this whole
    // argument exists to prevent, and a low height alone cannot tell them apart.
    if (version && s.image_tag && s.image_tag !== version) {
      die(`${t.rpc} came back on ${s.image_tag}, not ${version}`,
          'The chain was wiped but is running the wrong build.');
    }
    if (firstLow == null) {
      firstLow = s.height;
      continue;
    }
    if (s.height > firstLow) {
      console.log(`  ✓ height ${before} -> ${s.height} (fresh genesis, sealing${version ? ` ${version}` : ''})`);
      return;
    }
  }
  die('reset did not produce a climbing new genesis',
      before != null ? `last seen height before dispatch: ${before}` : 'could not read height before dispatch',
      t.rpc);
}
