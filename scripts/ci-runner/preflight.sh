#!/usr/bin/env bash
#
# preflight.sh — does this host meet what ci.yml actually needs?
#
# Run it on a candidate box BEFORE registering a runner, and again after. It
# checks only things this repository's CI genuinely depends on, each with the
# reason attached, so a failure says what will break rather than just "no".
#
#   bash preflight.sh
#
# Exit 0 = the box can run this CI. Exit 1 = something listed will fail.
set -uo pipefail

pass=0 fail=0 warn=0
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; pass=$((pass+1)); }
no()   { printf '  \033[1;31m✗\033[0m %s\n' "$*"; fail=$((fail+1)); }
meh()  { printf '  \033[1;33m!\033[0m %s\n' "$*"; warn=$((warn+1)); }
head_() { printf '\n\033[1;36m%s\033[0m\n' "$*"; }

# Portable enough to run on the laptop you are about to deploy from, so a typo
# is caught before it is inside an SSM session on a box you cannot see.
cpu_count() {
	if command -v nproc >/dev/null 2>&1; then nproc
	elif [ -r /proc/cpuinfo ]; then grep -c ^processor /proc/cpuinfo
	elif command -v sysctl >/dev/null 2>&1; then sysctl -n hw.ncpu
	else echo 0; fi
}
mem_gb() {
	if [ -r /proc/meminfo ]; then
		awk '/MemTotal/{printf "%d", $2/1024/1024}' /proc/meminfo
	elif command -v sysctl >/dev/null 2>&1; then
		sysctl -n hw.memsize 2>/dev/null | awk '{printf "%d", $1/1024/1024/1024}'
	else echo 0; fi
}
free_gb() {
	df -Pk "$1" 2>/dev/null | awk 'NR==2{printf "%d", $4/1024/1024}' || echo 0
}

if [ "$(uname -s)" != "Linux" ]; then
	printf '\033[1;33mnote\033[0m this host is %s; the runner target is Linux x64. Shape and\n' "$(uname -s)"
	printf '     egress checks still mean something, tool paths do not.\n'
fi

head_ "shape"

# The load-bearing one. `go test` defaults -p to NumCPU. ci.yml now pins -p 4,
# so more cores are harmless — but fewer than 4 means the suite runs at a
# parallelism it has never been tuned at, and the 25-minute timeout was set
# against a 4-vCPU box.
cpus="$(cpu_count)"
if [ "$cpus" -ge 4 ]; then
	ok "$cpus vCPU (ci.yml pins -p 4; the suite was tuned on a 4-vCPU hosted runner)"
else
	no "$cpus vCPU — fewer than the 4 this suite is tuned for; expect timeouts, and consensus timing tests to behave differently"
fi

mem_gb="$(mem_gb)"
if [ "$mem_gb" -ge 8 ]; then ok "${mem_gb} GB RAM"
else no "${mem_gb} GB RAM — ubuntu-24.04 gives 16; Go build + vitest will thrash"; fi

free_gb="$(free_gb "${RUNNER_HOME:-/tmp}")"
if [ "$free_gb" -ge 30 ]; then ok "${free_gb} GB free"
else no "${free_gb} GB free — the Go module and build caches alone want tens of GB, and an ephemeral runner re-downloads on a cold cache"; fi

head_ "tools the workflows assume exist"
for t in git curl tar unzip jq; do
	command -v "$t" >/dev/null && ok "$t" || no "$t — actions/checkout and action unpacking need it"
done
# gh is bundled in GitHub's images and in nothing else. ci.yml's wallet-sync
# job calls it directly; without it that step warns and skips.
command -v gh >/dev/null && ok "gh $(gh --version 2>/dev/null | head -1 | awk '{print $3}')" \
	|| meh "gh is missing — the wallet-extension dispatch will warn and skip (soft-fail by design)"

head_ "toolchains"
# Go and Node are installed per-job by actions/setup-*, so a missing system
# copy is fine. A WRONG system Go is not: sdk/sqc-sdk/wasm/build.sh prefers
# /usr/local/go/bin/go over PATH, and a different version there silently
# changes the signer bytes and re-pins sqc.networks.json to a digest nobody
# else can reproduce.
want_go="1.25.0"
if [ -x /usr/local/go/bin/go ]; then
	have="$(/usr/local/go/bin/go env GOVERSION 2>/dev/null | sed 's/^go//')"
	if [ "$have" = "$want_go" ]; then ok "/usr/local/go is go${have}"
	else no "/usr/local/go is go${have}, not ${want_go} — wasm/build.sh prefers this path over setup-go's and would emit different signer bytes"; fi
else
	ok "no /usr/local/go (actions/setup-go supplies ${want_go}; nothing shadows it)"
fi

head_ "egress (every job downloads; there is no vendor dir and no offline mode)"
# Reachability, not a healthy root page: several of these serve 400 or 404 at
# "/" by design (objects.githubusercontent.com is a blob host). Any HTTP
# response at all means TLS completed and egress is open, which is the thing
# being tested. -o /dev/null with a written-out code distinguishes "answered"
# from "could not connect".
probe() {
	local host="$1" code
	code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "https://${host}/" 2>/dev/null || echo 000)"
	[ "$code" != "000" ]
}
for host in github.com api.github.com proxy.golang.org sum.golang.org vuln.go.dev registry.npmjs.org objects.githubusercontent.com; do
	if probe "$host"; then
		ok "$host"
	else
		case "$host" in
			vuln.go.dev)      why="govulncheck cannot fetch its database" ;;
			registry.npmjs.org) why="both npm ci steps fail" ;;
			proxy.golang.org|sum.golang.org) why="go mod download and the GOTOOLCHAIN fetch in wasm/build.sh fail" ;;
			*)                why="checkout and action downloads fail" ;;
		esac
		no "$host unreachable — $why"
	fi
done

head_ "isolation"
# The runner executes code from pull requests. Anything reachable from this
# box is reachable by that code.
if [ -r /root/.aws/credentials ] || [ -r "${HOME:-/root}/.aws/credentials" ]; then
	meh "AWS credentials are present on this host — a pull request could read them. Prefer a box with no production credentials."
else
	ok "no AWS credential file in root's home"
fi
if command -v docker >/dev/null 2>&1 && docker ps >/dev/null 2>&1; then
	meh "this host runs Docker with other containers — CI shares the kernel and daemon with whatever else is here"
else
	ok "no reachable Docker daemon sharing this host"
fi

printf '\n%d passed, %d warnings, %d failures\n' "$pass" "$warn" "$fail"
[ "$fail" -eq 0 ]
