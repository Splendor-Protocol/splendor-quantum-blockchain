# Changesets

Every pull request that changes a published package (`@splendor/sqc-sdk`,
`@splendor/connect-react`, `@splendor/dapp-spec`, `create-splendor-dapp`) adds a changeset:

```bash
npm run changeset
```

Pick the packages, the bump (patch / minor / major) and write one line for the changelog.
`sqc-sdk` and `connect-react` are linked, so they always release on the same version line.

The release workflow (`.github/workflows/release-packages.yml`) turns pending changesets into a
"Version packages" pull request; merging it publishes to npm. See
[`docs/RELEASING_PACKAGES.md`](../docs/RELEASING_PACKAGES.md).
