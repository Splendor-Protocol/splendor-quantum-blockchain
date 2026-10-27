#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
base=$(bash scripts/lint-base.sh)
echo "Checking new lint issues since $base"
exec .tools/bin/golangci-lint run --new-from-rev="$base" ./...
