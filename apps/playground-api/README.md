# Splendor dApp Playground API

Fastify service behind the [dApp Playground](../playground): wallet sign-in, saved and shared
projects, catalog flags, the package release dashboard, and the audit log.

```bash
cp .env.example .env.local      # put your address in ADMIN_ADDRESSES
npm run playground:api          # from the repo root: http://127.0.0.1:8787
```

Tests run on in-memory SQLite by default. Production runs on Postgres, so run them there too
before changing SQL (CI does, in the `playground-api-postgres` job):

```bash
docker run --rm -e POSTGRES_PASSWORD=pg -p 5432:5432 postgres:16
npm run test:playground-api:pg  # TEST_DATABASE_URL, default postgres://postgres:pg@127.0.0.1:5432/postgres
```

Each test case gets its own schema, created and dropped by the suite. The two SQLite file tests
(migration upgrades, tied timestamps) are skipped on Postgres.

- **Sign-in**:
  - Get a nonce from `POST /auth/nonce`, then send the signed message to `POST /auth/verify`.
  - The signature is checked with `verifySignIn` from `@splendor/sqc-sdk/auth` (ML-DSA-65). The domain must be in `PLAYGROUND_ORIGINS`, and each nonce works once, for 5 minutes.
  - The session is a `__Host-` httpOnly cookie (when Secure). Only its sha256 is stored, and each new sign-in replaces the previous session.
  - Addresses are stored canonically (`SQ:` + lowercase hex).
- **CSRF**:
  - State-changing requests must carry JSON (except DELETE) and an allowed `Origin`.
  - A request with the session cookie but no `Origin` must be `Sec-Fetch-Site: same-origin`.
- **Validation**:
  - Every route has a JSON schema that rejects unknown fields and never coerces types.
  - Specs then go through `validateSpec`, and only its normalized copy is stored.
- **Errors and headers**:
  - Errors are `{ error, code?, requestId, issues? }`. `requestId` is also the `x-request-id` header and the log key.
  - 5xx responses say only `Internal error`. Unknown routes are `404` with code `NOT_FOUND`; schema failures are `400` with code `INVALID_REQUEST` and `issues`.
  - Responses are `no-store` unless public, and carry `nosniff`, `DENY` framing, `Referrer-Policy: no-referrer` and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`. With Secure cookies they also carry `Strict-Transport-Security`.
  - A failed sign-in answers `401` with `{ error: 'Sign-in failed', code: 'AUTH_FAILED' }` whatever the cause; the reason is kept in the audit log.
- **Saving projects**:
  - Every project has a `version` that goes up on each change.
  - `PUT /projects/:id` and `PATCH /projects/:id` accept `baseVersion`. When the project has moved past it (another tab or device saved first), the API answers `409` with code `CONFLICT` and the current project summary instead of overwriting it. A `PUT` without `baseVersion` always applies (deprecated: the response carries `Deprecation: true`); a `PATCH` without it is checked against the version it read, since it rewrites the stored spec.
  - Each saved spec is kept as a revision (the last 20 per project). Restoring one saves it again as the newest version, on top of `baseVersion`.
- **Trash**:
  - Deleting a project, or a workspace with its projects, moves it to the trash for 30 days. Trashed projects are out of every list, their share links stop, and they do not count toward the 100-project cap.
  - A workspace restores with the projects deleted together with it; a project restored on its own leaves a workspace that is still in the trash.
- **Lists**: `GET /projects` and `GET /workspaces/:id/projects` take `q` (name contains), `sort` (`updated` or `name`), `limit` (1–100) and `cursor`, and return `{ projects, nextCursor }`.
- **Share links** are the live spec until revoked. `POST /projects/:id/share` takes an optional `expiresInDays` (1–365); summaries report `shareExpiresAt` and `shareViews`. An expired link answers `404` with code `EXPIRED`, and sharing again issues a new id.
- **Audit**: sign-in, logout, project and workspace create, update, delete, restore and purge, share and unshare, catalog flags, admins and releases. Autosaves are recorded once per project per 10 minutes.
- **Storage**:
  - One SQL store over `node:sqlite` (development) or Postgres (production), chosen by `DATABASE_URL`.
  - Ordered migrations run on startup.
  - A housekeeping timer purges expired sessions and nonces, old audit rows, and trash older than 30 days.
  - Routes live in `src/routes/` (auth, workspaces and trash, projects and revisions, share, admin, deployments); `src/app.ts` wires the store, hooks and limits.
- **Releases**:
  - Reads repo versions, changesets and workflow runs from GitHub, and dist-tags from npm, each call with a 10-second timeout.
  - Dispatches `release-packages.yml`. It holds a GitHub token that can start the workflow, never an npm token.

Every route is limited to 300 requests per minute per IP unless the table says otherwise.

| Route | Who |
|-------|-----|
| `GET /health` (liveness), `GET /health/ready` (database), `GET /catalog` | anyone |
| `GET /share/:shareId` | anyone (60/min per IP) |
| `POST /auth/nonce`, `POST /auth/verify` | anyone (30/min per IP) |
| `GET /auth/me`, `POST /auth/logout` | anyone (401 / no-op without a session) |
| `GET /workspaces`, `POST /workspaces`, `PATCH /workspaces/:id` (rename), `DELETE /workspaces/:id` (to trash), `GET /workspaces/:id/projects` | signed-in owner |
| `GET /projects?workspaceId=&q=&sort=&limit=&cursor=`, `POST /projects`, `GET /projects/:id`, `PUT /projects/:id` (spec), `PATCH /projects/:id` (name, workspace), `DELETE /projects/:id` (to trash) | signed-in owner |
| `POST /projects/:id/duplicate`, `POST /projects/:id/share`, `DELETE /projects/:id/share` | signed-in owner |
| `GET /projects/:id/revisions`, `GET /projects/:id/revisions/:version`, `POST /projects/:id/revisions/:version/restore` | signed-in owner |
| `GET /trash`, `POST /trash/projects/:id/restore`, `DELETE /trash/projects/:id`, `POST /trash/workspaces/:id/restore`, `DELETE /trash/workspaces/:id` | signed-in owner |
| `PUT /admin/catalog/:id`, `GET /admin/releases`, `GET /admin/audit`, `GET /admin/admins`, `POST /admin/admins`, `DELETE /admin/admins/:address` | admins |
| `POST /admin/releases` | admins (5/min) |
| `POST /deployments`, `GET /deployments/:id` | signed-in, only with `HOSTING_ENABLED=1` (phase 2) |
| `POST /auth/test-login` | only with `ALLOW_TEST_LOGIN=1` (end-to-end tests) |

Behind a reverse proxy, set `TRUST_PROXY` (e.g. `loopback`) so rate limits and the audit log see the
real client IP. `TRUST_PROXY=true` (trust every peer) is refused in production unless
`TRUST_PROXY_ALLOW_ALL=1` is also set. The project cap (100 live projects) is enforced atomically
in the store, so concurrent creates and restores cannot exceed it. The API is designed for a single instance; see
[Security model and limits](../playground/README.md#security-model-and-limits). Configuration,
deployment and the proxy setup are described in the [playground README](../playground/README.md#deploying).
For the release flow, see [Releasing packages](../../docs/RELEASING_PACKAGES.md).
