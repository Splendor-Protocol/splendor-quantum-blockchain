# Releasing the npm packages

Runbook for the people who publish `@splendor/sqc-sdk`, `@splendor/connect-react`,
`@splendor/dapp-spec` and `create-splendor-dapp`. Releases run in GitHub Actions
([`.github/workflows/release-packages.yml`](../.github/workflows/release-packages.yml)) with
[changesets](https://github.com/changesets/changesets) and npm provenance. Nobody publishes
from a laptop.

The chain node has its own release process (`npm run deploy`, `scripts/release.mjs`); this
document is only about the npm packages.

## Packages

| Package | Folder | Notes |
|---------|--------|-------|
| `@splendor/sqc-sdk` | `sdk/sqc-sdk` | `prepack` rebuilds the signer and fails unless every network pin matches the built bytes. |
| `@splendor/connect-react` | `sdk/connect-react` | Linked with sqc-sdk: when both release, they get the same version. |
| `@splendor/dapp-spec` | `packages/dapp-spec` | Spec, validator, code generator, templates and block sources. |
| `create-splendor-dapp` | `packages/create-splendor-dapp` | `npm create splendor-dapp`. Pins the SDK versions in its own `devDependencies`. |

`@splendor/playground` and `@splendor/playground-api` are private and never published.

## One-time setup

1. **npm**: create an automation token for an account with publish rights on the `@splendor`
   scope and on `create-splendor-dapp`. Store it as the repository secret **`NPM_TOKEN`**. This
   is the only place the npm token exists.
2. **GitHub Actions**: in Settings → Actions → General, allow "Read and write permissions" and
   "Allow GitHub Actions to create and approve pull requests", so the workflow can open the
   version PR.
3. **Playground admin panel (optional)**: create a fine-grained personal access token limited to
   this repository with **Actions: Read and write** and **Contents: Read** and nothing else.
   Set it on the playground API as `GITHUB_TOKEN` (with `GITHUB_REPO`, and optionally
   `GITHUB_RELEASE_WORKFLOW` and `GITHUB_RELEASE_REF`). This token can only read files and start
   or read workflow runs. It cannot push, merge or publish. Without it the release page shows npm
   versions only.
4. **Admins**: list the wallet addresses allowed into the admin panel in the API's
   `ADMIN_ADDRESSES`. Admins can add further admins in the panel; every change is in the audit log.

## Day to day: changesets

Every pull request that changes a published package adds a changeset:

```bash
npm run changeset
```

Pick the packages, the bump and a one-line summary for the changelog. It writes a markdown file
under `.changeset/`; commit it with the change.

| Bump | When |
|------|------|
| patch | Bug fix, no API change. |
| minor | New export, prop, hook, widget or template; anything additive. |
| major | Removed or renamed export, changed behaviour callers rely on, raised peer minimum. |

**Include `create-splendor-dapp` whenever the SDKs are released.** Its pinned SDK versions are
`devDependencies`. Changesets updates those pins when the SDKs bump, but does not bump
`create-splendor-dapp` itself for a devDependency change. Without its own changeset, the CLI on
npm keeps creating projects against the previous SDK. A patch bump is enough.

Peer ranges such as dapp-spec's `@splendor/connect-react >=3.0.0` only force a bump of the
dependent package when the new version falls outside the range
(`onlyUpdatePeerDependentsWhenOutOfRange` in `.changeset/config.json`).

## Publishing a stable release (`latest`)

1. Merge pull requests with changesets into `main`. On each push, the workflow opens or updates a
   pull request named **"chore: version packages"**. It applies the bumps, writes the
   `CHANGELOG.md` entries and refreshes `package-lock.json`.
2. Review that PR like any other: versions, changelog text, and the pins in
   `packages/create-splendor-dapp/package.json`.
3. Merge it. The workflow runs on `main` again, builds, tests and verifies the signer pins, then
   publishes every package whose version is not yet on npm, under the `latest` tag with
   provenance, and tags the commit.

Running the workflow by hand with `channel=latest, dry_run=false` does the same as step 3. If
changesets are still pending, it updates the version PR instead of publishing. It never
republishes a version that is already on npm.

## Pre-releases (`next`)

Use these to let someone try an unreleased change: `npm i @splendor/connect-react@next`.

Start the workflow with `channel=next` from the Actions tab or from the playground admin panel
(**Admin → Releases → Start a release**). It turns the pending changesets into snapshot versions
such as `3.1.0-next-20261008120000` and publishes them under the `next` tag. Nothing is committed
or tagged, and `latest` is untouched. With no pending changesets it publishes nothing and says so.

## Dry runs

`dry_run=true` (the default in both the Actions form and the admin panel) installs, builds, tests,
checks the signer pins and runs `npm pack --dry-run` for every package, without publishing. Use it
before a real release to see exactly which files each tarball will contain.

Locally, `npm run release-packages:pack` prints the same file lists. `npm run test:codegen`
installs the packed tarballs into freshly generated projects and builds them.

## The admin panel

**Admin → Releases** on the playground shows:

- each package's version on `main` (read through the GitHub API) and its npm `latest` and `next`
  tags;
- the pending changesets and their summaries;
- the last ten runs of the release workflow.

Its **Start a release** form dispatches the workflow with a channel and the dry-run switch.
Every dispatch, successful or not, is written to **Admin → Audit log** with the admin's address.
The API never sees the npm token: it can start the workflow, and the workflow publishes.

## When something goes wrong

| Symptom | Cause and fix |
|---------|---------------|
| `check-wasm-dist` or sqc-sdk `prepack` fails | The committed signer pins do not match the bytes this commit builds. CI's `wasm-drift` job re-pins `main` itself; wait for it, or run `bash sdk/sqc-sdk/wasm/build.sh && node sdk/sqc-sdk/scripts/sync-config.mjs` and commit the two `sqc.networks.json` files. |
| `E403` / `ENEEDAUTH` on publish | `NPM_TOKEN` is missing, expired, or lacks publish rights on that package. |
| Provenance error | The job needs `id-token: write` (already set), and `repository.url` in the package must match this repository. |
| No version PR appears | No changesets are pending, or Actions may not create pull requests (one-time setup, step 2). |
| Admin panel: "GitHub is not configured" | `GITHUB_TOKEN` is not set on the API. |
| Admin panel: `GitHub /actions/...: 403` | The token lacks Actions: Read and write on this repository. |
| `next` run published nothing | No changesets were pending. |
| A bad version reached `latest` | Never unpublish. Publish a fixed patch. If you need to move users off it now, run `npm dist-tag add <pkg>@<good-version> latest` with an account that has publish rights, then fix forward. |
