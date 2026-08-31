# Splendor Quantum Migration dapp

Single Cloudflare Worker serving the **one-way SPLD/SPLV migration** page:
burn SPLD (native) or SPLV (ERC-20) on legacy Splendor mainnet (EVM chain
2691) and the bridge credits the same amount on the Splendor Quantum chain —
SPLD as the native coin, SPLV as a QRC-20. Minimum 1 token per burn.

Architecture mirrors `splendor-quantum-faucet` (no framework: `src/index.ts`
router + `src/page.ts` render + inline script), design language mirrors
`splendor-quantum-explorer` (ink / acid / violet / cyan, mono-uppercase data
register). Part of the quantum-migration-bridge plan
(`docs/plans/2026-08-31-quantum-migration-bridge.md`, §4).

## What the Worker does — and does not

- `GET /` — the page. All EVM interaction happens in the **user's injected
  wallet** (MetaMask); the page only fetches same-origin `/api/*`. CSP is
  `default-src 'self'` with a per-response script nonce; zero external assets.
- `GET /api/info` — network params, burn-contract + SPLV addresses, min burn,
  confirmation depth, and bridge stats (pool balance, claimed totals, paused)
  read from the quantum node's `GET /bridge/{addr}` with a 10 s cache. If the
  quantum node is unreachable, `stats` is `null` and the page shows dashes.
- `GET /api/status/{txhash}` — server-side `eth_getTransactionReceipt` +
  `eth_blockNumber` against the legacy RPC, decodes `Burned` events out of the
  receipt, then asks the quantum node's `GET /bridge/{addr}/claim/{id}` for
  each burn's idempotency slot. Per-IP KV cooldown (~2 s; the page polls every
  6 s).
- `GET /health`.

The Worker holds **no keys and submits nothing**. Burns are signed by the
user; claims are submitted by `cmd/bridge-relayer`. Everything here is
read-only.

Contract constants are precomputed keccak-256 values pinned in
`src/selectors.ts` and re-verified by `test/selectors.test.ts`:

| constant | signature | value |
|---|---|---|
| `SEL_BURN_SPLD` | `burnSpld(bytes32)` | `0xb4b27926` |
| `SEL_BURN_SPLV` | `burnSplv(uint256,bytes32)` | `0xe5e2a80a` |
| `SEL_APPROVE` | `approve(address,uint256)` | `0x095ea7b3` |
| `SEL_ALLOWANCE` | `allowance(address,address)` | `0xdd62ed3e` |
| `SEL_BALANCE_OF` | `balanceOf(address)` | `0x70a08231` |
| `BURNED_TOPIC0` | `Burned(uint256,address,uint8,uint256,bytes32)` | `0x18fb7d6af55f185ad81a39bf0537706c6c62595e260cf5aaeb3f491a4240cd58` |

Addresses are **never** hardcoded in the page — they come from `/api/info`.

## Environment

All configuration is public `vars` in `wrangler.jsonc` — there are no secrets.

| var | default | meaning |
|---|---|---|
| `EVM_RPC_URL` | `https://mainnet-rpc.splendor.org/` | legacy-chain JSON-RPC (server-side reads) |
| `EVM_CHAIN_ID_HEX` | `0xa83` (2691) | chain the wallet is switched/added to |
| `EVM_EXPLORER_URL` | `https://explorer.splendor.org` | burn-tx links |
| `BURN_CONTRACT_ADDRESS` | — **required** | deployed `QuantumMigrationBurn` |
| `SPLV_TOKEN_ADDRESS` | `0xd196c83A64cdf8d2763721D8c3D135A15B1Cb689` | SPLV ERC-20 on 2691 |
| `CONFIRMATIONS` | `120` | depth shown as "required" in the timeline |
| `QUANTUM_RPC_BASE` | — **required** | quantum node REST base (`/bridge/…`) |
| `QUANTUM_BRIDGE_ADDRESS` | — **required** | bridge module instance address |
| `QUANTUM_EXPLORER_URL` | `https://quantum-explorer.splendor.org` | credited links |
| `STATUS_COOLDOWN_MS` | `2000` | per-IP `/api/status` cooldown |

Until the three **required** vars are set, `/api/*` answers
`503 {code:"unconfigured"}` and the page says the bridge is not configured —
it never breaks. `CONFIRMATIONS` should match the relayer's configured depth,
or the timeline will disagree with reality.

KV: create the namespace and paste its id into `wrangler.jsonc`
(`MIGRATION_KV` bindings, top-level + per-env):

```sh
npx wrangler kv namespace create MIGRATION_KV
```

## Develop / test / deploy

```sh
npm install
npm test          # vitest: routes, selectors (keccak-verified), page, ratelimit
npm run typecheck
npm run dev       # wrangler dev (set the required vars in wrangler.jsonc or .dev.vars)
npm run deploy    # wrangler deploy --env production
```

Deploy checklist (per the rollout plan):

1. Deploy `QuantumMigrationBurn` on 2691 (from `wallet/contracts`), note the
   address.
2. `bridge.deploy` on the quantum chain, note the instance address; fund the
   pool; start the relayer.
3. Fill `BURN_CONTRACT_ADDRESS`, `QUANTUM_RPC_BASE`, `QUANTUM_BRIDGE_ADDRESS`
   in `wrangler.jsonc` (production env), create the KV namespace, then
   `npm run deploy`. The route `quantum-migration.splendor.org` is a Worker
   custom domain — Cloudflare manages the DNS record.

## Extract to its own repo before production deploy

This directory is scaffolded in-repo **for review only**. Per infra decision
0001 (one service, one repo), extract it to
`Splendor-Protocol/splendor-quantum-migration` before the production deploy
(target domain `quantum-migration.splendor.org`), the way
`splendor-quantum-faucet` lives standalone. The directory is self-contained —
copy it verbatim, `git init`, done.
