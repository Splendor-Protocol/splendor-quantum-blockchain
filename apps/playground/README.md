# Splendor dApp Playground

A web app for learning `@splendor/sqc-sdk` and `@splendor/connect-react` and building dApps
from them without writing code:

- **Learn**: the SDK guides, plus runnable recipes (network status, balances, token reads,
  message signing) against the network picked in the header.
- **Widgets**: every widget and block, previewed on its own with editable props, a theme and
  copy-paste code.
- **Build**: start from a template or a blank page, add blocks, edit their props and theme,
  and preview against a mock wallet (nothing is signed) or live with a real wallet. Export a
  Vite or Next.js project as a zip, open it in StackBlitz, or deploy it to Vercel or Netlify.
  Signed-in users save projects to their account and share read-only links that others can remix.
- **Admin**: catalog flags (hide, feature, override status, set a minimum version), templates
  and recipes, the package release dashboard, the audit log, and the admin list.

It has two parts: this static SPA, and [`apps/playground-api`](../playground-api), a small Fastify
service for sign-in, projects, catalog flags and releases. Without the API the playground still
works; it just has no accounts, sharing, flags or admin.

## How it fits together

```
@splendor/connect-react/manifest ─┐
@splendor/dapp-spec (blocks)      ├─► catalog ─► gallery, builder prop forms, validation
admin flags (API /catalog)       ─┘
                       DappSpec (JSON) ─► preview (iframe, mock wallet) / live (real provider)
                                       └► generateProject() ─► zip / StackBlitz / create-splendor-dapp
```

- A dApp is a `DappSpec` (`@splendor/dapp-spec`): name, network, theme, pages, and blocks of
  `{ widget, props }`. `validateSpec` runs everywhere a spec arrives (the editor, the preview
  iframe, and the API), so a hand-edited or shared spec is treated as untrusted input.
- The mock preview runs in a same-origin iframe at `/preview`. It only accepts `postMessage` from
  its parent window, validates the spec again, and renders it inside `MockSplendorProvider`. Wallet
  calls are listed under **Mock wallet activity**.
- Export uses `generateProject`, the same code generator as `npm create splendor-dapp`. The
  generated project pins the connect-react and sqc-sdk versions bundled into this playground,
  so an export behaves like its preview.

### Extending it

| To add | Do this |
|--------|---------|
| A widget | Export it from connect-react and add an entry to `WIDGET_MANIFESTS` (`sdk/connect-react/src/manifest.ts`). It shows up in the gallery and builder with a generated prop form. |
| A block (composed from widget parts) | Add `packages/dapp-spec/blocks/<Name>.tsx` and its manifest in `BLOCK_MANIFESTS`. Its source is embedded in exported projects. |
| A widget from another package | Add its manifest list to `createCatalog` and its component module to `PACKAGES` in `src/preview/componentMap.ts`. |
| A template | Add it to `TEMPLATES` in `packages/dapp-spec/src/templates.ts`. `create-splendor-dapp` gets it too. |
| A Learn recipe | Add a `SdkRecipeManifest` to `src/registry/recipes.ts`. |

Admins can hide, feature or re-badge any of these at runtime without a deploy.

## Local development

From the repository root:

```bash
bash sdk/sqc-sdk/wasm/build.sh   # once: the signer the SDK loads (needs Go)
npm install
npm run playground               # builds the packages, then Vite on http://localhost:5180
npm run playground:api           # optional, second terminal: API on http://127.0.0.1:8787
```

The dev server proxies `/api` to the API (`PLAYGROUND_API_PROXY`) and each public network to
`/rpc/<id>`, because the public RPCs do not send CORS headers for localhost. To sign in to the
admin panel locally, copy `apps/playground-api/.env.example` to `.env.local` and put your wallet
address in `ADMIN_ADDRESSES`.

## Tests

| Command (repo root) | What it covers |
|---------|----------------|
| `npm run test:playground` | Builder state (undo/redo) and API unit tests (auth, CSRF, projects, flags, releases, audit). |
| `npm run test:e2e` | Playwright: every template in the builder with the mock wallet (transfer, tip, sign-in, token deploy), zip export for both frameworks, save, share and remix, plus the admin flows (catalog flags, release dry run, audit) against a fake GitHub and npm. It also checks the CSP and that the preview iframe is isolated. It builds the playground and serves it with `vite preview` under the deploy headers, alongside its own API (in-memory SQLite) and a fake GitHub, on ports 5181, 8797 and 8798. `E2E_DEV=1` uses the dev server instead (faster, but no CSP and a same-origin preview). Run `npx playwright install chromium` in this folder first. |
| `npm run test:codegen` | Generates every template for Vite and Next.js, installs the SDKs from `npm pack` tarballs, and runs each production build. |
| `E2E_LIVE=1 E2E_LIVE_NETWORK=devnet npx playwright test e2e/live` | Read-only recipes against a real network (the nightly workflow runs this). |

## Deploying

### 1. Build the SPA

```bash
bash sdk/sqc-sdk/wasm/build.sh
npm ci
npm run playground:build         # → apps/playground/dist
```

`dist/` is static. The build copies the signer into it and pins its sha256 (`sqcWasmPlugin`),
so the live wallet refuses a swapped binary. Serve it from any static host or CDN with:

- **SPA fallback**: unknown paths (`/build`, `/widgets/transfer`, `/s/<id>`, `/admin/...`,
  `/preview`) must serve `index.html`.
- `sqc_signer.wasm` served as `application/wasm`.

Build-time variables (`apps/playground/.env.production.local` or the CI environment):

| Variable | Default | Purpose |
|----------|---------|---------|
| `VITE_PLAYGROUND_API_URL` | `/api` | API base URL. Keep `/api` when you reverse-proxy it (recommended). |
| `VITE_PLAYGROUND_DEFAULT_NETWORK` | `testnet` | Network selected on first visit. |
| `VITE_SQC_<ID>_RPC_URL` | from `sqc.networks.json` | Override a network's RPC (`<ID>` = `TESTNET`, `DEVNET`, `MAINNET`, `LOCAL`). Also `_CHAIN_ID` and `_EXPLORER_URL`. |

Live mode and Learn recipes call the RPC from the visitor's browser. Off localhost there is no
dev proxy, so the RPC must allow your playground origin via CORS. If it does not, proxy it on your
own domain and point the playground at it with a full URL, e.g.
`VITE_SQC_TESTNET_RPC_URL=https://playground.example.com/rpc/testnet`.

### 2. Run the API

The API imports the workspace packages, so deploy it from a built checkout of this repository:

```bash
bash sdk/sqc-sdk/wasm/build.sh   # sign-in verification loads the signer in Node
npm ci
npm run build:packages
npm run build -w @splendor/playground-api
cd apps/playground-api && NODE_ENV=production node dist/server.js
```

It needs Node 22.13 or newer. On startup it applies any pending schema migrations, in order, each
in one transaction, and records them in `schema_migrations`. A fresh database needs no separate
step. Every 10 minutes it deletes expired sessions and nonces, and audit rows older than
`AUDIT_RETENTION_DAYS`. `SIGTERM` drains in-flight requests for up to 10 seconds.

Point your process manager or load balancer at `GET /health` (liveness: the process answers) and
`GET /health/ready` (readiness: the database answers; 503 otherwise).

| Variable | Required | Notes |
|----------|----------|-------|
| `DATABASE_URL` | yes | `postgres://user:pass@host:5432/playground`. SQLite (`sqlite:./data/playground.db`) is for development and single-instance trials. |
| `PLAYGROUND_ORIGINS` | yes in production | Comma-separated bare origins (no path or trailing slash), e.g. `https://playground.example.com`. Used for CORS, the CSRF origin check, and as the allowed sign-in domains. The API refuses to start on anything else. |
| `ADMIN_ADDRESSES` | for admin | SQ: addresses that are always admins (any hex case). Further admins can be added in the UI and are stored in the database. |
| `TRUST_PROXY` | behind a proxy | Peers allowed to set `X-Forwarded-For`: addresses, CIDRs or `loopback` (e.g. `loopback` for nginx on the same host). Unset means the TCP peer is the client, which is correct when nothing sits in front. Never set it when the API is reachable directly, or clients can choose their own IP and bypass rate limits. |
| `PORT`, `HOST` | no | Default `8787` and `127.0.0.1`. Use `HOST=0.0.0.0` in a container. |
| `COOKIE_SECURE` | no | Defaults to on in production. A Secure cookie is named `__Host-sqc_pg_session`. |
| `COOKIE_SAMESITE` | no | `lax` (default). `none` only for the cross-site setup below, and only with `COOKIE_SECURE=1`. |
| `SESSION_TTL_SEC` | no | Default 7 days (5 minutes to 90 days). |
| `AUDIT_RETENTION_DAYS` | no | Default 365. |
| `SIGNIN_CHAIN_ID` | no | Reject sign-in messages for other chains. |
| `GITHUB_TOKEN`, `GITHUB_REPO`, `GITHUB_RELEASE_WORKFLOW`, `GITHUB_RELEASE_REF` | for releases | See [Releasing packages](../../docs/RELEASING_PACKAGES.md). Without a token the release page is read-only (npm versions only). |
| `HOSTING_ENABLED` | no | Phase-2 hosted publishing routes (`/deployments`). Leave off. |
| `ALLOW_TEST_LOGIN` | never | Test-only password-less login. The API refuses to start with it in production, or on any host other than loopback unless `NODE_ENV=test`. |

### 3. Connect them

**Same origin (recommended).** Serve the SPA and reverse-proxy `/api/` to the API with the prefix
stripped. The session cookie stays first-party and `SameSite=Lax` works. nginx example:

```nginx
server {
  server_name playground.example.com;
  root /srv/playground/dist;

  # Same values as src/securityHeaders.ts, public/_headers and vercel.json.
  add_header Content-Security-Policy "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' https://*.splendor.org wss://*.splendor.org; frame-src 'self'; frame-ancestors 'self'; object-src 'none'; base-uri 'none'; form-action 'self' https://stackblitz.com" always;
  add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
  add_header X-Content-Type-Options "nosniff" always;
  add_header Referrer-Policy "strict-origin-when-cross-origin" always;
  add_header Permissions-Policy "camera=(), microphone=(), geolocation=(), payment=()" always;
  add_header Cross-Origin-Opener-Policy "same-origin-allow-popups" always;

  location /api/ {
    proxy_pass http://127.0.0.1:8787/;      # trailing slash strips /api
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;   # overwrite, never append a client's value
    proxy_set_header X-Forwarded-Proto $scheme;
  }
  # The preview iframe has an opaque origin, so its module scripts and CSS are CORS requests.
  # (add_header in a location replaces the server-level ones, so assets get only these.)
  location /assets/ {
    add_header Access-Control-Allow-Origin "*" always;
    add_header Cache-Control "public, max-age=31536000, immutable" always;
    add_header X-Content-Type-Options "nosniff" always;
  }
  location ~* \.wasm$ { types { application/wasm wasm; } }
  location / { try_files $uri /index.html; }
}
```

Run the API with `TRUST_PROXY=loopback` behind this proxy, so rate limits and the audit log see
the real client IP.

**Netlify** picks up [`public/_headers`](public/_headers) and [`public/_redirects`](public/_redirects)
from the build output. **Vercel** uses [`vercel.json`](vercel.json). In both cases, add a rewrite
from `/api/:path*` to the API's URL. If you change the CSP, change it in
[`src/securityHeaders.ts`](src/securityHeaders.ts) and in both files; `npm test` fails when they
disagree.

**Different sites.** If the API must live on another site (e.g. `api.example.net`), build with
`VITE_PLAYGROUND_API_URL=https://api.example.net` and run the API with `COOKIE_SAMESITE=none` and
`COOKIE_SECURE=1`. Browsers that block third-party cookies will then drop the session, so prefer
the same-origin setup.

### Checklist

- [ ] `GET /api/health/ready` returns `{"ok":true}` through the proxy.
- [ ] The page response carries `Content-Security-Policy`, and `/assets/*` carries
      `Access-Control-Allow-Origin: *`. Without the latter, the builder preview stays blank in
      production, because the isolated iframe cannot load its scripts.
- [ ] Sign in on `/admin` with an address from `ADMIN_ADDRESSES`. "This wallet is not an admin"
      means the address is wrong; a sign-in error mentioning the domain means `PLAYGROUND_ORIGINS`
      does not list the origin in the browser's address bar.
- [ ] `/admin/releases` shows the repo versions (GitHub token works) and npm versions.
- [ ] A shared link (`/s/<id>`) opens in a private window.
- [ ] A database backup has been taken and restored once (see below).

## Security model and limits

**What is trusted.** The playground's code and the bundled docs. Everything else is input: specs
(from the editor, local drafts, the API and share links), sign-in messages, and API request bodies.

| Threat | Control |
|--------|---------|
| A shared spec carrying script or markup | Specs are data only. `validateSpec` checks every field against the widget manifests and returns a fresh copy with only known fields. Prototype keys are rejected, URLs must be http(s), theme values are plain CSS tokens. Generated code escapes every string. |
| A spec escaping its preview | In production the preview iframe has an opaque origin (no `allow-same-origin`). It cannot read the playground's DOM, storage or cookies, and messages are checked by window and origin on both sides. |
| Script injection on the playground origin | A CSP without inline scripts or `eval` (only `'wasm-unsafe-eval'` for the signer), `frame-ancestors 'self'`, `object-src 'none'` and `base-uri 'none'`. Rendered markdown is sanitized with DOMPurify. |
| Session theft or fixation | 256-bit session ids. Only their sha256 is stored. The cookie is `httpOnly`, `Secure` and `__Host-`, and is replaced at every sign-in. Sign-in is an ML-DSA-65 signature over a single-use, 5-minute nonce for an allowlisted domain. |
| Cross-site request forgery | State changes need JSON and an allowlisted `Origin`. A cookie-bearing request without `Origin` is accepted only with `Sec-Fetch-Site: same-origin`. CORS allows only `PLAYGROUND_ORIGINS`. |
| Brute force and floods | Per-IP rate limits (300/min overall, 30/min on sign-in, 5/min on release dispatch), per-user project caps, body size limits, strict request schemas. Failed sign-ins are audited at most 60 times a minute. |
| Leaking internals | 5xx responses carry only a generic message and a request id. Details go to the log under that id. Private responses are `Cache-Control: no-store`. |
| A compromised admin session publishing packages | The API can only dispatch the release workflow. Real publishes wait for the `npm-publish` environment's reviewers on GitHub, and the npm token lives only there. Every admin action is in the audit log. |

**Limits of this deployment.** The API is built for **one instance**. Rate-limit counters and the
housekeeping timer are in process memory. A second instance would double every limit, and each
instance would run the (idempotent) cleanup. Scaling out needs a shared rate-limit store (e.g. Redis
via `@fastify/rate-limit`'s `redis` option) and a single scheduler for housekeeping. SQLite is
single-node and blocks the event loop on each query; use Postgres for anything you need to keep.

**Backups.** With Postgres, schedule `pg_dump --format=custom` (daily, with retention), copy the dumps
off the host, and test a `pg_restore` into a scratch database. Projects and the admin list live
only there. With SQLite, back up the database file together with its `-wal` file, or run
`sqlite3 playground.db ".backup out.db"`.

**Secrets.** `GITHUB_TOKEN` (fine-grained, this repo only, Actions read/write and Contents read) and
`DATABASE_URL` are the only secrets the API holds. Rotate the GitHub token on a schedule and when
an admin leaves. Signing out every session is `DELETE FROM sessions`.

**Reporting.** See [SECURITY.md](../../SECURITY.md).
