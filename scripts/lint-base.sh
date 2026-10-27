#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
base=${LINT_BASE:-origin/main}
if ! git rev-parse --verify "$base^{commit}" >/dev/null 2>&1; then
  echo "Cannot resolve lint base '$base'. Run git fetch origin main, or set LINT_BASE to an existing commit." >&2
  exit 1
fi
git merge-base "$base" HEAD
