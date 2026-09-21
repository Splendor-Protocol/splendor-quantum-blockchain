#!/usr/bin/env bash
#
# install-runner.sh — turn an Ubuntu host into a self-hosted GitHub Actions
# runner for this repository.
#
# Idempotent: re-running upgrades the runner and leaves the service healthy.
# Modelled on observability/bin/install-alloy.sh in the infra repo, which is
# the house pattern for "install a long-lived thing on a box over SSM".
#
# WHY THIS EXISTS
#
# CI stopped running on 2026-09-20: every job began failing in about two
# seconds with zero steps executed, on every branch, while "Dependabot
# Updates" (which runs on GitHub's own infrastructure and consumes no Actions
# minutes) kept succeeding. That shape is runner starvation at the account
# level, not a test failure. A self-hosted runner's minutes are free and
# unmetered, including on a private repository, so this is the direct fix as
# well as a cost decision.
#
# USAGE
#
#   sudo REPO=Splendor-Protocol/splendor-quantum-blockchain \
#        TOKEN=<registration token> \
#        bash install-runner.sh
#
# Mint TOKEN (valid one hour) with a repo-admin credential:
#
#   gh api -X POST /repos/$REPO/actions/runners/registration-token --jq .token
#
# The token is single-use and short-lived by design: it is exchanged for a
# durable per-runner credential at registration and is not kept on the box.
set -euo pipefail

REPO="${REPO:?set REPO=owner/name}"
TOKEN="${TOKEN:?set TOKEN=<registration token>}"
RUNNER_USER="${RUNNER_USER:-ghrunner}"
RUNNER_HOME="${RUNNER_HOME:-/opt/actions-runner}"
RUNNER_VERSION="${RUNNER_VERSION:-2.337.0}"
# The label the workflows select on. Keep it in step with the repo variable
# CI_RUNNER_LABEL; see README.md in this directory.
RUNNER_LABELS="${RUNNER_LABELS:-self-hosted,linux,x64,splendor-ci}"
GO_VERSION="${GO_VERSION:-1.25.14}"

say() { printf '\033[1;36m==\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mFATAL\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
[ "$(uname -m)" = "x86_64" ] || die "this script builds an x64 runner; got $(uname -m)"

# ---------------------------------------------------------------- packages --
#
# The workflows install their own Go and Node through actions/setup-go and
# actions/setup-node, so the toolchain is NOT pinned here. What a stock Ubuntu
# box is missing is smaller than it looks:
#
#   git, curl, tar, unzip  — actions/checkout and every action tarball
#   jq                     — used by the runner's own scripts
#   gh                     — ci.yml dispatches to the wallet extension with it,
#                            and GitHub's images bundle it while nothing else
#                            does. Without it that step warns and skips.
#   build-essential        — not needed by this repo today (no cgo anywhere),
#                            but govulncheck and `go run` compile from source
#                            and a missing linker is a confusing failure.
say "installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends \
	git curl ca-certificates tar unzip jq build-essential acl >/dev/null

if ! command -v gh >/dev/null 2>&1; then
	say "installing the GitHub CLI"
	install -m 0755 -d /etc/apt/keyrings
	curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
		-o /etc/apt/keyrings/githubcli-archive-keyring.gpg
	chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
	echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
		>/etc/apt/sources.list.d/github-cli.list
	apt-get update -qq && apt-get install -y -qq gh >/dev/null
fi

# ------------------------------------------------------------------- user ---
#
# The runner executes whatever a pull request contains. It gets an
# unprivileged user with no sudo and no access to anything else on the box.
if ! id -u "$RUNNER_USER" >/dev/null 2>&1; then
	say "creating user $RUNNER_USER"
	useradd -m -s /bin/bash "$RUNNER_USER"
fi

# ----------------------------------------------------------------- runner ---
say "installing actions-runner $RUNNER_VERSION into $RUNNER_HOME"
mkdir -p "$RUNNER_HOME"
cd "$RUNNER_HOME"

if [ ! -x ./run.sh ] || [ "$(cat .installed-version 2>/dev/null || true)" != "$RUNNER_VERSION" ]; then
	# A registered runner must be told to go away before its files are
	# replaced, or GitHub keeps offering it jobs it can no longer accept.
	if [ -f .runner ]; then
		say "removing the previous registration"
		sudo -u "$RUNNER_USER" ./config.sh remove --token "$TOKEN" || true
	fi
	tarball="actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
	curl -fsSL -o "/tmp/$tarball" \
		"https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${tarball}"
	tar xzf "/tmp/$tarball" -C "$RUNNER_HOME"
	rm -f "/tmp/$tarball"
	echo "$RUNNER_VERSION" >.installed-version
fi
chown -R "$RUNNER_USER:$RUNNER_USER" "$RUNNER_HOME"

if [ ! -f .runner ]; then
	say "registering with $REPO"
	# NOT --ephemeral, deliberately. An ephemeral runner unregisters after one
	# job and needs a fresh registration token to come back, and minting one
	# takes a repo-admin credential — which would then have to live on the box
	# that runs pull-request code. That trades a workspace-hygiene problem for
	# a credential-exposure one. The registration below is persistent; the
	# job-completed hook installed further down wipes _work after every job,
	# which is the part of ephemerality that actually matters here.
	sudo -u "$RUNNER_USER" ./config.sh \
		--unattended \
		--url "https://github.com/${REPO}" \
		--token "$TOKEN" \
		--name "$(hostname -s)" \
		--labels "$RUNNER_LABELS" \
		--work _work \
		--replace
fi

# ------------------------------------------------------------------ hooks ---
#
# Runs after every job, as the runner user, before the next job is accepted.
# _work holds three kinds of thing and only one of them is per-job:
#   _work/<owner>/<repo>  the checkout — wiped
#   _work/_temp           the job's scratch — wiped
#   _work/_tool           setup-go/setup-node's tool cache — KEPT
#   _work/_actions        downloaded actions — KEPT
# The tool cache is what makes the second run faster than the first (no
# 60 MB Go tarball per job), and a job cannot write to it in a way the next
# job would trust. The first version of this hook wiped all of _work and
# re-downloaded Go and Node on every run.
say "installing the job-completed hook"
cat >"$RUNNER_HOME/job-completed.sh" <<'HOOK'
#!/usr/bin/env bash
# Leave nothing from this job for the next one.
set -u
work="$(dirname "$0")/_work"
[ -d "$work" ] || exit 0
for d in "$work"/*/; do
	case "$(basename "$d")" in
		_tool|_actions|_PipelineMapping) ;;   # caches and runner bookkeeping: keep
		*) rm -rf "$d" ;;                      # checkouts and _temp: wipe
	esac
done
exit 0
HOOK
chmod 0755 "$RUNNER_HOME/job-completed.sh"
chown "$RUNNER_USER:$RUNNER_USER" "$RUNNER_HOME/job-completed.sh"
# The runner reads hook paths from .env in its own directory at start-up.
grep -q '^ACTIONS_RUNNER_HOOK_JOB_COMPLETED=' "$RUNNER_HOME/.env" 2>/dev/null \
	|| echo "ACTIONS_RUNNER_HOOK_JOB_COMPLETED=${RUNNER_HOME}/job-completed.sh" >>"$RUNNER_HOME/.env"
chown "$RUNNER_USER:$RUNNER_USER" "$RUNNER_HOME/.env"

# ---------------------------------------------------------------- service ---
#
# The runner ships svc.sh, which writes a unit much like this one; writing it
# here keeps the whole install in one reviewable file.
say "installing the systemd unit"
cat >/etc/systemd/system/actions-runner.service <<UNIT
[Unit]
Description=GitHub Actions runner (${REPO})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUNNER_USER}
WorkingDirectory=${RUNNER_HOME}
ExecStart=${RUNNER_HOME}/run.sh
# Always restart: a runner update or a transient API failure exits the
# process, and the delay keeps a real failure from becoming a hot loop.
Restart=always
RestartSec=5
KillMode=process
KillSignal=SIGTERM
TimeoutStopSec=5min
# The runner needs a real HOME for the tool cache actions/setup-go writes to.
Environment=HOME=/home/${RUNNER_USER}

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now actions-runner.service
sleep 3
systemctl is-active --quiet actions-runner.service \
	|| die "the service did not come up; journalctl -u actions-runner -n 50"

say "runner is up"
echo
echo "  labels : $RUNNER_LABELS"
echo "  repo   : https://github.com/${REPO}/settings/actions/runners"
echo "  logs   : journalctl -u actions-runner -f"
echo
echo "Nothing routes to it until the repo variable CI_RUNNER_LABEL is set."
echo "Point CI at it with:"
echo "    gh variable set CI_RUNNER_LABEL --repo ${REPO} --body splendor-ci"
echo "and back to GitHub's runners by deleting that variable:"
echo "    gh variable delete CI_RUNNER_LABEL --repo ${REPO}"
