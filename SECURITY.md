# Security Policy

Splendor Quantum Chain's trust model is cryptographic, not administrative:
blocks are ML-DSA-65 signed, P2P traffic rides ML-KEM-768 sealed channels,
and a node never applies anything it cannot authenticate against its pinned
validator key. That makes a security report about a signature forgery,
channel break, or validation bypass potentially consensus-critical, and we
treat it accordingly.

## Supported versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: (next release) |
| latest tagged release | :white_check_mark: |

Security fixes land on `main` and are backported to the latest release
tag as a patch release.

## Reporting a vulnerability

**Please do not open a public issue.**

Preferred channel — **GitHub private vulnerability reporting** (repo
**Security → Advisories → Report a vulnerability**). This keeps the report
confidential while we coordinate.

If that is unavailable, email `security@splendorprotocol.io` with
`[SQC SECURITY]` in the subject, ideally encrypted to a project maintainer
key; PGP keys can be provided on request.

Include in the report:

1. The affected component (chain node, SDK, signer WASM, protocol spec)
2. The affected version(s) and how the issue can be reached
3. A proof of concept or a precise description of the failure
4. Your disclosure preference (e.g. 90-day coordinated disclosure)

## What we promise

- Acknowledgement within **3 business days**
- A status update (confirmed / not affected / needs more info) within **14 days**
- A fix or a documented, reasoned refusal before the disclosure deadline
- Credit in the release notes unless you ask to stay anonymous

## Scope

In scope:

- `cmd/node`, `core/**`, `consensus/**`, `network/**`, `rpc/**`, `vm/**`
- The JavaScript/TypeScript SDK (`sdk/sqc-sdk`, `sdk/connect-react`) and the
  PQ-bridge signer WASM
- `@splendor/dapp-spec` (spec validation and the code it generates) and the
  `create-splendor-dapp` CLI
- The dApp Playground (`apps/playground`) and its API (`apps/playground-api`):
  for example, a shared spec that runs script or escapes the preview iframe, a
  sign-in or session bypass, CSRF, reading or changing another user's project,
  or reaching the admin panel or the release dispatch without being an admin.
  Its threat model is in `apps/playground/README.md` ("Security model and
  limits")
- The package release workflow (`.github/workflows/release-packages.yml`)
- The P2P protocol and SQCO wire formats as specified in `readme.md` and
  `docs/reports/**`

Out of scope (report elsewhere):

- Third-party deployments and infrastructure
- DoS via unauthenticated resource exhaustion alone (report it, but it is
  handled as a hardening issue, not a critical)
- Social engineering, physical access, and leaked operator keys (keys are
  the operator's responsibility; the network cannot recover a leaked
  validator key by design)

## Security model at a glance

- **Chain authentication:** every block is ML-DSA-65 signed; a node pins the
  genesis validator key (or genesis hash) and refuses anything else
- **Transport:** ML-KEM-768 hybrid sealed channel with GCM + authenticated
  timestamps (bounded replay window); plaintext P2P is opt-in only
- **Coded object transfer (SQCO):** RLNC/erasure-coded objects are signed by
  the node identity; every symbol proof is authenticated against its
  generation's codebook root before it reaches a decoder; all decode
  allocations are bounded by peer-supplied manifests
- **Fuzz + CI:** the SQCO wire decoders are fuzzed on every CI run, and
  reachable Go dependencies are scanned with govulncheck on every push
