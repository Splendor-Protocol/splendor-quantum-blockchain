# Development checks

Use the Go version declared in `go.mod`. Run these commands from the repository
root on Linux, macOS, or WSL:

```sh
git fetch origin main
make dev-setup   # install the pinned linter and the local pre-commit hook
make fmt         # format the Go source tree
make lint        # check formatting and new static-analysis findings
go test -count=1 -p 4 ./...
```

`make lint` installs the official golangci-lint binary pinned in
`.golangci-version`, verifies its release checksum, and uses `.golangci.yml`.
The enabled checks are `errcheck`, `govet`, `ineffassign`, `staticcheck`, and
`unused`. The executable lives in the ignored `.tools/bin` directory, so a
different globally installed linter does not change the rules.

## Formatting before commit

The tracked VS Code/Cursor settings enable Go formatting on save. Other editors
can use `.editorconfig` and their Go plugin's `gofmt` support. `make fmt` provides
the same formatting from the terminal.

The pre-commit hook checks the **staged** Go contents. It reports formatting and
syntax errors and leaves both the index and working copy unchanged. After
`make fmt`, review and stage the result yourself; partially staged changes are
never silently added to a commit. `make dev-setup` preserves existing hook
managers: if one is installed, add `bash scripts/go-format.sh staged` to its
pre-commit hook instead.

Hooks are local conveniences. The shared CI `lint` job is the server-side gate,
and the full Go job runs only after lint succeeds. Required checks on `main`
prevent merging failing changes even when a developer has not installed hooks.

## Scope of the checks

Formatting applies to the entire Go source tree, including new files. Static
analysis examines the Go packages but initially blocks findings on newly
changed lines, so existing lint debt does not force unrelated changes into a PR.
Use `make lint-all` to inspect the full backlog. This is an incremental rollout,
not a claim that the existing tree is lint-clean.

Locally, the comparison starts at the merge base with `origin/main` and includes
working-tree edits. Fetch before checking. For another target branch, use
`make lint LINT_BASE=origin/that-branch`. CI supplies the PR base or the previous
push commit explicitly. Missing base history is an error, never a skipped check.

The `lint`, `build and test`, SDK tests, signer verification, and secret scan
checks are required for changes to `main`, with no account or bot bypass.
Existing protection against force pushes and branch deletion also remains in
place.

## Signer pins

Changes to code linked into the WASM signer can change its digest. When the
signer check reports drift, run `make signer-pins` and include both
`sdk/sqc-sdk/config/sqc.networks.json` and
`sdk/sqc-sdk/src/config/sqc.networks.json` in the PR. The build uses the pinned
toolchain and computes the digests automatically. CI verifies the committed
pins; it no longer pushes an unchecked pin update directly to `main`.
