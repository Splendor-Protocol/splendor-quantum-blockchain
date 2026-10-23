# Splendor dApp Playground API

Fastify service behind the [dApp Playground](../playground): wallet sign-in, saved and shared
projects, catalog flags, the package release dashboard, and the audit log.

```bash
cp .env.example .env.local      # put your address in ADMIN_ADDRESSES
npm run playground:api          # from the repo root: http://127.0.0.1:8787
```

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
  - Responses are `no-store` unless public, and carry `nosniff` and `DENY` framing headers.
- **Saving projects**:
  - Every project has a `version` that goes up on each change.
  - `PUT /projects/:id` and `PATCH /projects/:id` accept `baseVersion`. When the project has moved past it (another tab or device saved first), the API answers `409` with code `CONFLICT` and the current project summary instead of overwriting it. Without `baseVersion` the save always applies.
- **Storage**:
  - One SQL store over `node:sqlite` (development) or Postgres (production), chosen by `DATABASE_URL`.
  - Ordered migrations run on startup.
  - A housekeeping timer purges expired sessions and nonces, and old audit rows.
- **Releases**:
  - Reads repo versions, changesets and workflow runs from GitHub, and dist-tags from npm, each call with a 10-second timeout.
  - Dispatches `release-packages.yml`. It holds a GitHub token that can start the workflow, never an npm token.

Every route is limited to 300 requests per minute per IP unless the table says otherwise.

| Route | Who |
|-------|-----|
| `GET /health` (liveness), `GET /health/ready` (database), `GET /catalog`, `GET /share/:shareId` | anyone |
| `POST /auth/nonce`, `POST /auth/verify` | anyone (30/min per IP) |
| `GET /auth/me`, `POST /auth/logout` | anyone (401 / no-op without a session) |
| `GET /workspaces`, `POST /workspaces`, `PATCH /workspaces/:id` (rename), `DELETE /workspaces/:id`, `GET /workspaces/:id/projects` | signed-in owner |
| `GET /projects?workspaceId=`, `POST /projects`, `GET /projects/:id`, `PUT /projects/:id` (spec), `PATCH /projects/:id` (name, workspace), `DELETE /projects/:id` | signed-in owner |
| `POST /projects/:id/duplicate`, `POST /projects/:id/share`, `DELETE /projects/:id/share` | signed-in owner |
| `PUT /admin/catalog/:id`, `GET /admin/releases`, `GET /admin/audit`, `GET /admin/admins`, `POST /admin/admins`, `DELETE /admin/admins/:address` | admins |
| `POST /admin/releases` | admins (5/min) |
| `POST /deployments`, `GET /deployments/:id` | signed-in, only with `HOSTING_ENABLED=1` (phase 2) |
| `POST /auth/test-login` | only with `ALLOW_TEST_LOGIN=1` (end-to-end tests) |

Behind a reverse proxy, set `TRUST_PROXY` (e.g. `loopback`) so rate limits and the audit log see the
real client IP. The API is designed for a single instance; see
[Security model and limits](../playground/README.md#security-model-and-limits). Configuration,
deployment and the proxy setup are described in the [playground README](../playground/README.md#deploying).
For the release flow, see [Releasing packages](../../docs/RELEASING_PACKAGES.md).
