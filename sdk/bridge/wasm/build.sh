# Build Splendor QC WASM signer for Node.js / browser
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$ROOT/dist"
mkdir -p "$OUT"

# ── Find Go binary ─────────────────────────────────────────────────────────
# Prefer /usr/local/go/bin/go (installed by install.sh), then PATH fallback
if [ -f /usr/local/go/bin/go ]; then
  export PATH=$PATH:/usr/local/go/bin
  GO=/usr/local/go/bin/go
elif command -v go &>/dev/null; then
  GO=$(command -v go)
else
  echo "Error: Go not found. Run install.sh first, or install Go 1.23 from https://go.dev/dl/"
  exit 1
fi

echo "Using Go: $($GO version)"

export GOOS=js
export GOARCH=wasm
export GO111MODULE=on

echo "Building sqc_signer.wasm..."
# Use the resolved $GO binary (robust when go is not on PATH) with -trimpath so
# the build is byte-reproducible (the wasmSha256 pin in sqc.networks.json depends
# on this). -trimpath strips the filesystem path, so no cd workaround is needed.
$GO build -trimpath -o "$OUT/sqc_signer.wasm" "$ROOT/main.go"

GOROOT="$($GO env GOROOT)"
cp "$GOROOT/misc/wasm/wasm_exec.js" "$OUT/wasm_exec.js" 2>/dev/null \
  || cp "$GOROOT/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

echo "Done!"
echo "  $OUT/sqc_signer.wasm"
echo "  $OUT/wasm_exec.js"
echo ""
echo "Next: copy to wallet extension:"
echo "  mkdir -p ../../wallet-extension/wasm"
echo "  cp $OUT/sqc_signer.wasm ../../wallet-extension/wasm/"
echo "  cp $OUT/wasm_exec.js ../../wallet-extension/wasm/"
