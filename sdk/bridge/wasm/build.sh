# Build Splendor QC WASM signer for Node.js / browser
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
OUT="$ROOT/dist"
mkdir -p "$OUT"

export GOOS=js
export GOARCH=wasm

echo "Building sqc_signer.wasm..."
go build -trimpath -o "$OUT/sqc_signer.wasm" "$ROOT/main.go"

GOROOT="$(go env GOROOT)"
cp "$GOROOT/misc/wasm/wasm_exec.js" "$OUT/wasm_exec.js" 2>/dev/null \
  || cp "$GOROOT/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

echo "Done: $OUT/sqc_signer.wasm"
echo "      $OUT/wasm_exec.js"
