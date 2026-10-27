#!/usr/bin/env bash
set -euo pipefail
root=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/scripts" "$tmp/.githooks" "$tmp/source files"
cp "$root/scripts/"{go-format,lint-base,setup-hooks}.sh "$tmp/scripts/"
cp "$root/.githooks/pre-commit" "$tmp/.githooks/"
cd "$tmp"
git init -q
git config user.name 'Formatting test'
git config user.email 'formatting-test@example.invalid'
printf 'package example\n\nfunc Example() {}\n' > 'source files/example.go'
git add .
git -c core.hooksPath=/dev/null commit -qm baseline
export LINT_BASE
LINT_BASE=$(git rev-parse HEAD)

expect_failure() {
  if "$@" > "$tmp/output.log" 2>&1; then
    echo "Unexpected success: $*" >&2
    exit 1
  fi
}

bash scripts/go-format.sh check
# Both tracked edits and untracked filenames containing spaces are checked.
printf 'package example\nfunc Example(){ }\n' > 'source files/example.go'
printf 'package example\nfunc Other(){ }\n' > 'source files/new file.go'
expect_failure bash scripts/go-format.sh check
bash scripts/go-format.sh write
bash scripts/go-format.sh check
git add .

# Unformatted staged content must fail even if the working copy is fixed.
printf 'package example\nfunc Example(){ }\n' > 'source files/example.go'
git add 'source files/example.go'
staged=$(git rev-parse ':source files/example.go')
bash scripts/go-format.sh write
working=$(git hash-object 'source files/example.go')
expect_failure bash .githooks/pre-commit
test "$staged" = "$(git rev-parse ':source files/example.go')"
test "$working" = "$(git hash-object 'source files/example.go')"

# Conversely, unstaged edits must not be added or rejected by the hook.
git add 'source files/example.go'
printf 'package example\nfunc Example(){ }\n' > 'source files/example.go'
working=$(git hash-object 'source files/example.go')
bash .githooks/pre-commit
test "$working" = "$(git hash-object 'source files/example.go')"
bash scripts/go-format.sh write

# Syntax errors must fail, even though gofmt produces no formatting diff.
printf 'package example\nfunc Broken(\n' > 'source files/new file.go'
expect_failure bash scripts/go-format.sh check
expect_failure bash scripts/go-format.sh write
git add 'source files/new file.go'
expect_failure bash .githooks/pre-commit
git rm -qf -- 'source files/new file.go'
bash .githooks/pre-commit

# Renames are checked under their destination path, including spaces.
git mv 'source files/example.go' 'source files/renamed file.go'
printf 'package example\nfunc Example(){ }\n' > 'source files/renamed file.go'
git add .
expect_failure bash .githooks/pre-commit
bash scripts/go-format.sh write
git add 'source files/renamed file.go'
bash .githooks/pre-commit

# Missing history fails closed instead of silently skipping the gate.
expect_failure env LINT_BASE=missing-base bash scripts/lint-base.sh

# Setup preserves existing hook managers and standalone hooks.
git config core.hooksPath custom-hooks
expect_failure bash scripts/setup-hooks.sh
test "$(git config core.hooksPath)" = custom-hooks
git config --unset core.hooksPath
printf '#!/bin/sh\nexit 0\n' > .git/hooks/pre-commit
expect_failure bash scripts/setup-hooks.sh
rm .git/hooks/pre-commit
bash scripts/setup-hooks.sh
test "$(git config core.hooksPath)" = .githooks
echo 'Formatting guardrail checks passed.'
