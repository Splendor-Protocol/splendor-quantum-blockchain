# Splendor dApp Playground API

Fastify service behind the [dApp Playground](../playground): wallet sign-in, saved and shared
projects, catalog flags, the package release dashboard, and the audit log.

```bash
cp .env.example .env.local      # put your address in ADMIN_ADDRESSES
npm run playground:api          # from the repo root: http://127.0.0.1:8787
```

- **Sign-in**: `GET /auth/nonce`, then `POST /auth/verify` with the signed message. The signature
  is checked with `verifySignIn` from `@splendor/sqc-sdk/auth` (ML-DSA-65), the domain must be in
  `PLAYGROUND_ORIGINS`, and each nonce works once. The session is an httpOnly cookie, and only its
  sha256 is stored.
- **CSRF**: state-changing requests must come from an allowed `Origin` and, apart from DELETE,
  carry a JSON body.
- **Storage**: one SQL store over `node:sqlite` (development) or Postgres (production), chosen by
  `DATABASE_URL`. Tables are created on startup.
- **Releases**: reads repo versions, changesets and workflow runs from GitHub and dist-tags from
  npm, and dispatches `release-packages.yml`. It holds a GitHub token that can start the workflow,
  never an npm token.

| Route | Who |
|-------|-----|
| `GET /health`, `GET /catalog`, `GET /share/:shareId` | anyone |
| `/auth/*` | anyone (rate limited) |
| `/projects`, `/projects/:id`, `POST /projects/:id/share` | signed-in owner |
| `PUT /admin/catalog/:id`, `/admin/releases`, `/admin/audit`, `/admin/admins` | admins |
| `/deployments` | signed-in, only with `HOSTING_ENABLED=1` (phase 2) |

Configuration, deployment and the reverse-proxy setup are described in the
[playground README](../playground/README.md#deploying). For the release flow, see
[Releasing packages](../../docs/RELEASING_PACKAGES.md).
