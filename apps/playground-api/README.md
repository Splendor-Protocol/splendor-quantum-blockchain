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
  - 5xx responses say only `Internal error` plus a `requestId`, which is also the `x-request-id` header and the log key.
  - Responses are `no-store` unless public, and carry `nosniff` and `DENY` framing headers.
- **Storage**:
  - One SQL store over `node:sqlite` (development) or Postgres (production), chosen by `DATABASE_URL`.
  - Ordered migrations run on startup.
  - A housekeeping timer purges expired sessions and nonces, and old audit rows.
- **Releases**:
  - Reads repo versions, changesets and workflow runs from GitHub, and dist-tags from npm, each call with a 10-second timeout.
  - Dispatches `release-packages.yml`. It holds a GitHub token that can start the workflow, never an npm token.

| Route | Who |
|-------|-----|
| `GET /health` (liveness), `GET /health/ready` (database), `GET /catalog`, `GET /share/:shareId` | anyone |
| `/auth/*` | anyone (30/min per IP) |
| `/projects`, `/projects/:id`, `POST /projects/:id/share` | signed-in owner |
| `PUT /admin/catalog/:id`, `/admin/releases`, `/admin/audit`, `/admin/admins` | admins |
| `/deployments` | signed-in, only with `HOSTING_ENABLED=1` (phase 2) |

Behind a reverse proxy, set `TRUST_PROXY` (e.g. `loopback`) so rate limits and the audit log see the
real client IP. The API is designed for a single instance; see
[Security model and limits](../playground/README.md#security-model-and-limits). Configuration,
deployment and the proxy setup are described in the [playground README](../playground/README.md#deploying).
For the release flow, see [Releasing packages](../../docs/RELEASING_PACKAGES.md).
