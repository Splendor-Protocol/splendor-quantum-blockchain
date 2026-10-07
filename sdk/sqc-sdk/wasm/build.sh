#!/usr/bin/env bash
# Build Splendor QC WASM signer for Node.js / browser
#
# This file must stay LF-terminated. It was committed with CRLF once, and bash
# then read `set -euo pipefail<CR>` as an option named "pipefail\r" and refused
# to start ("set: pipefail: invalid option name"), which took the signer-drift
# CI job down before it built anything.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$ROOT/dist"
mkdir -p "$OUT"

# ── Pin the toolchain ──────────────────────────────────────────────────────
# The artifact digest is Go-release-sensitive: identical source built under a
# different Go produces different bytes (go1.25.0 → 4,100,732 B, go1.26.2 →
# 4,266,441 B). Whoever rebuilds with whatever they happen to have installed
# therefore invalidates the wasmSha256 pin in sqc.networks.json for everyone
# else — and a pin nobody can reproduce is a pin that gets dropped. GOTOOLCHAIN
# makes any Go >= 1.21 fetch and use exactly this release instead.
# 1.25.14 is what go.mod declares and the family the node Dockerfile builds
# with; circl v1.6.5 (FIPS-204 ML-DSA-65) requires >= 1.25. Kept in step with
# go.mod on purpose: govulncheck in CI scans the stdlib of the toolchain that
# runs it, and go1.25.0 carries 28 published CVEs that later patch releases
# fix. Bumping here changes the signer bytes, and wasm-drift re-pins them.
export GOTOOLCHAIN="${SQC_GO_TOOLCHAIN:-go1.25.14}"

# ── Find Go binary ─────────────────────────────────────────────────────────
# Prefer /usr/local/go/bin/go (installed by install.sh), then PATH fallback.
# Any 1.21+ Go will do — GOTOOLCHAIN above switches it to the pinned release.
if [ -f /usr/local/go/bin/go ]; then
  export PATH=$PATH:/usr/local/go/bin
  GO=/usr/local/go/bin/go
elif command -v go &>/dev/null; then
  GO=$(command -v go)
else
  echo "Error: Go not found. Run install.sh first, or install Go 1.21+ from https://go.dev/dl/"
  exit 1
fi

echo "Using Go: $($GO version)  (GOTOOLCHAIN=$GOTOOLCHAIN)"

export GOOS=js
export GOARCH=wasm
export GO111MODULE=on

# go build resolves the module from the WORKING DIRECTORY, not from the path of
# the file it is handed, so this only ever worked when run from the repo root.
# The wallet extension's CI builds this signer from its own checkout to check its
# copy against source rather than against the chain's committed binary, and got
# "go.mod file not found" for it. Build from the module root instead; -trimpath
# means the bytes do not depend on where that root is.
cd "$ROOT/../../.."

echo "Building sqc_signer.wasm..."
# Use the resolved $GO binary (robust when go is not on PATH).
#
# -trimpath strips the filesystem path, so no cd workaround is needed.
#
# -ldflags=-buildid= is what actually makes the digest reproducible. Go stamps a
# build ID whose leading action ID hashes the build environment, not just the
# source, so two machines compiling identical source with the identical pinned
# toolchain still emit different bytes in that one 62-byte header field — the
# code after it is identical. That is enough to break a sha256 pin, and a pin
# nobody else can reproduce is a pin that gets deleted the first time it fires.
# Emptying the build ID removes the only environment-dependent field, so the
# artifact is a pure function of source + toolchain.
$GO build -trimpath -ldflags=-buildid= -o "$OUT/sqc_signer.wasm" "$ROOT/main.go"

GOROOT="$($GO env GOROOT)"
# wasm_exec.js ships read-only from the Go toolchain, and a previous build
# leaves a read-only copy in $OUT; cp then cannot overwrite it in place and
# fails with EACCES. Remove the destination first — the file is a pure copy of
# the toolchain's, never edited.
rm -f "$OUT/wasm_exec.js"
cp "$GOROOT/misc/wasm/wasm_exec.js" "$OUT/wasm_exec.js" 2>/dev/null \
  || cp "$GOROOT/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

# Print the digests the pins must carry. Every consumer verifies these before
# instantiating, so a rebuild that is not followed by `npm run sync:config`
# (SDK) and a refreshed wallet-extension copy fails closed rather than silently
# shipping an unpinned signer.
if command -v shasum &>/dev/null; then
  SHA_WASM=$(shasum -a 256 "$OUT/sqc_signer.wasm" | cut -d' ' -f1)
  SHA_EXEC=$(shasum -a 256 "$OUT/wasm_exec.js" | cut -d' ' -f1)
elif command -v sha256sum &>/dev/null; then
  SHA_WASM=$(sha256sum "$OUT/sqc_signer.wasm" | cut -d' ' -f1)
  SHA_EXEC=$(sha256sum "$OUT/wasm_exec.js" | cut -d' ' -f1)
else
  SHA_WASM="(no sha256 tool)"
  SHA_EXEC="(no sha256 tool)"
fi

echo "Done!"
echo "  $OUT/sqc_signer.wasm   sha256 $SHA_WASM"
echo "  $OUT/wasm_exec.js      sha256 $SHA_EXEC"
echo ""
echo "Next, refresh every consumer of these bytes:"
echo "  (cd .. && npm run sync:config)            # rewrites wasmSha256/execSha256 pins"
echo "  # then, in splendor-quantum-wallet-extension: npm run sync:signer"
echo "  # (copies these files and re-pins WASM_SHA256/EXEC_SHA256)"
