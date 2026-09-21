# Running this repository's CI on our own hardware

CI here runs on GitHub-hosted `ubuntu-24.04` runners. This directory moves it
onto a machine we own, without rewriting the workflows and without losing the
green check marks on pull requests.

## Why

CI stopped working on 20 September 2026. Every job began failing in about two
seconds having executed **zero steps**, on every branch, and it has failed the
same way ever since:

```
build and test                 failure   started 12:19:48Z  completed 12:19:50Z
signer WASM matches its source  failure   started 12:19:48Z  completed 12:19:50Z
SDK typechecks and tests        failure   started 12:19:48Z  completed 12:19:50Z
CodeQL static analysis (Go)     failure   started 12:19:48Z  completed 12:19:51Z
```

Two seconds with no steps is not a test failure — nothing ran. The tell is what
*kept* working: "Dependabot Updates" runs on GitHub's own infrastructure and
consumes no Actions minutes, and it has succeeded throughout. Everything that
needs an Actions **runner** dies instantly; everything that does not is fine.
That is runner starvation at the account level. The organisation is on the
**free** plan with a private repository, which is a metered combination, and
the failures begin minutes after a burst of fourteen Dependabot pull requests
each triggering the full four-job matrix.

A self-hosted runner's minutes are free and unmetered, private repository
included. So this is the fix for the outage as much as it is a cost decision.

## What is here

| file | what it does |
|---|---|
| `preflight.sh` | Checks a candidate host against what `ci.yml` actually needs, with the reason attached to every check. Run it before and after. Portable enough to run on your laptop first. |
| `install-runner.sh` | Turns an Ubuntu host into a registered, ephemeral, systemd-managed runner. Idempotent. |

## Switching CI over, and back

The workflows select their runner through a repository variable:

```yaml
runs-on: ${{ vars.CI_RUNNER_LABEL || 'ubuntu-24.04' }}
```

Unset, everything runs on GitHub exactly as before. So the switch is a
settings change, not a commit, and the rollback is deleting a variable — which
matters when the thing you are changing is the only signal that tells you
whether changes are safe.

```bash
# move CI onto our runner
gh variable set CI_RUNNER_LABEL --repo Splendor-Protocol/splendor-quantum-blockchain --body splendor-ci

# put it back on GitHub's
gh variable delete CI_RUNNER_LABEL --repo Splendor-Protocol/splendor-quantum-blockchain
```

Keep the value in step with `RUNNER_LABELS` in `install-runner.sh`.

## Installing

```bash
REPO=Splendor-Protocol/splendor-quantum-blockchain

# 1. prove the box is suitable
bash preflight.sh

# 2. mint a registration token (one hour, single use, needs repo admin)
TOKEN=$(gh api -X POST /repos/$REPO/actions/runners/registration-token --jq .token)

# 3. install
sudo REPO=$REPO TOKEN=$TOKEN bash install-runner.sh

# 4. confirm GitHub sees it
gh api /repos/$REPO/actions/runners --jq '.runners[] | {name, status, labels: [.labels[].name]}'
```

Then set `CI_RUNNER_LABEL` and open a throwaway pull request to watch a real
job land on it.

## The decisions baked in, and why

**Persistent registration, wiped workspace.** The obvious choice is
`--ephemeral`, where the runner takes one job, unregisters and exits. But an
ephemeral runner needs a *fresh registration token* to come back, and minting
one takes a repo-admin credential — which would then have to live on the box
that executes pull-request code. That trades a workspace-hygiene problem for a
credential-exposure problem, and the second is worse. So the registration is
persistent, and a `ACTIONS_RUNNER_HOOK_JOB_COMPLETED` hook wipes `_work`
after every job instead: nothing a pull request leaves behind — an edited
file, a poisoned checkout, a stray temp dir — survives to the next one. The Go
module cache and the `setup-go`/`setup-node` tool cache live under `$HOME`
and are kept on purpose; they are what makes the second run faster than the
first. One consequence for `ci.yml`: the gofmt gate needs the PR's base commit
locally, a hosted runner's depth-1 clone meant it had been quietly skipping,
and any runner whose checkout persisted history would make it start firing on
its own — against **141 files that are already unformatted**. `ci.yml` now
pins `fetch-depth: 0` so the gate behaves the same way everywhere and is a
decision rather than a side effect.

**Four vCPUs is load-bearing, and is now written down.** `go test` defaults its
`-p` to the core count. This suite has always run at `-p 4` because
`ubuntu-24.04` happens to have four cores — nowhere was that written down.
Several consensus tests race real wall-clock deadlines as short as 30 ms, and
they flake when more package binaries run at once. On a 16-core box the suite
would silently become `-p 16` and start failing tests that have nothing wrong
with them. `ci.yml` now passes `-p 4` explicitly, so the suite no longer
depends on the shape of the machine underneath it.

**No production credentials on the box.** The runner executes whatever is in a
pull request. Anything it can reach, that code can reach. `preflight.sh` warns
when it finds AWS credentials or a shared Docker daemon. Give it its own
unprivileged user, an egress-only security group, and an instance profile with
nothing but SSM core.

**`gh` is installed explicitly.** GitHub's images bundle the CLI and nothing
else does. `ci.yml` calls it directly to nudge the wallet extension; without it
that step warns and skips rather than failing, but the nudge is lost.

**Go and Node are not pinned here.** The workflows install their own through
`actions/setup-go` and `actions/setup-node`. One exception is worth knowing:
`sdk/sqc-sdk/wasm/build.sh` prefers `/usr/local/go/bin/go` over `PATH`, so a
*different* system Go at that path silently changes the signer bytes and
re-pins `sqc.networks.json` to a digest nobody else can reproduce. `preflight.sh`
checks for exactly that.

## CodeQL cannot pass here, on any runner

`ci.yml`'s CodeQL job uploads SARIF to the code-scanning API, which refuses a
private repository unless GitHub Advanced Security is enabled. This
organisation is on the free plan with it off, and the API says so:

```
403 Advanced Security must be enabled for this repository to use code scanning
```

That is independent of where the job runs. It is gated off behind
`ENABLE_CODEQL` rather than deleted, so turning it on is one variable once GHAS
is bought or the repository goes public:

```bash
gh variable set ENABLE_CODEQL --repo $REPO --body true
```

Leaving a permanently red check in place is worse than removing it: it teaches
people to ignore red checks, which is the one thing CI cannot survive.

Note for when it is enabled on a self-hosted runner: the CodeQL bundle is
preinstalled in the hosted image's tool cache and is about a gigabyte. Our
runner downloads it on every cold run.

## What CI needs, for sizing

Measured from the last green run (20 September, 11:35–11:39 UTC): four jobs in
parallel, **about 3m40s wall**, with "build and test" the long pole at 3m35s.

- Linux x86_64, **4 vCPU**, ≥8 GB RAM (hosted gives 16), ≥30 GB free
- No Docker, no `apt` installs, no service containers — nothing in either
  workflow builds or runs a container
- Outbound HTTPS to `github.com`, `api.github.com`,
  `objects.githubusercontent.com`, the Actions cache endpoints,
  `proxy.golang.org`, `sum.golang.org`, `vuln.go.dev`, `registry.npmjs.org`
- **No inbound ports at all.** The runner polls outward, so it needs no DNS
  record, no load balancer, and no ingress rule.

One runner is enough: `ci.yml` sets `concurrency: cancel-in-progress` per ref,
so a branch never has two runs live. Two would let a `main` push and a pull
request overlap without queueing.
