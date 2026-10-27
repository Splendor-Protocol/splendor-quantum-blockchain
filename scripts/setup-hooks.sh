#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
existing=$(git config --get core.hooksPath || true)
if [ -n "$existing" ] && [ "$existing" != .githooks ]; then
  echo "Existing core.hooksPath is '$existing'; add 'bash scripts/go-format.sh staged' to your current pre-commit hook." >&2
  exit 1
fi
legacy=$(git rev-parse --git-path hooks/pre-commit)
if [ -z "$existing" ] && [ -f "$legacy" ]; then
  echo "Existing hook at $legacy; add 'bash scripts/go-format.sh staged' there instead of replacing it." >&2
  exit 1
fi
git config --local core.hooksPath .githooks
echo "Installed staged-Go formatting check. Run make fmt and make lint before pushing."
