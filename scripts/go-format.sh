#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
mode=${1:-check}
case "$mode" in check|write|staged) ;; *) echo "Usage: $0 [check|write|staged]" >&2; exit 2 ;; esac
gofmt="$("${GO:-go}" env GOROOT)/bin/gofmt"
files=$(mktemp)
trap 'rm -f "$files"' EXIT

if [ "$mode" = staged ]; then
  git diff --cached --name-only --diff-filter=ACMR -z -- '*.go' ':(exclude)vendor/**' > "$files"
else
  git ls-files --cached --others --exclude-standard -z -- '*.go' ':(exclude)vendor/**' > "$files"
fi

failed=0
while IFS= read -r -d '' file; do
  # An unstaged deletion is still in ls-files; staged mode reads the index.
  if [ "$mode" != staged ] && [ ! -e "$file" ]; then
    continue
  fi
  if [ "$mode" = write ]; then
    "$gofmt" -w "$file"
  elif [ "$mode" = staged ]; then
    # Check the index, not the working copy: partial staging must not hide
    # unformatted code or accidentally stage unrelated edits.
    if ! diff=$(git show ":$file" | "$gofmt" -d); then
      echo "Cannot format staged Go source: $file" >&2
      failed=1
    elif [ -n "$diff" ]; then
      printf 'gofmt required in staged file: %s\n%s\n' "$file" "$diff" >&2
      failed=1
    fi
  else
    if ! diff=$("$gofmt" -d "$file"); then
      echo "Cannot format Go source: $file" >&2
      failed=1
    elif [ -n "$diff" ]; then
      printf 'gofmt required: %s\n%s\n' "$file" "$diff" >&2
      failed=1
    fi
  fi
done < "$files"

if [ "$failed" -ne 0 ]; then
  echo "Run make fmt, review the result, then git add the files you want to commit." >&2
  exit 1
fi
