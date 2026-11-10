# @splendor/dapp-spec

Declarative description of a Splendor dApp (`DappSpec`), plus validation, migrations, starter
templates and a code generator that turns a spec into a ready-to-run **Vite** or **Next.js** project
using the published `@splendor/connect-react` and `@splendor/sqc-sdk` packages.

The Splendor dApp Playground and `create-splendor-dapp` are built on this package.

```bash
npm install @splendor/dapp-spec @splendor/connect-react
```

## A spec

```json
{
  "version": 2,
  "name": "Tip Jar",
  "network": "testnet",
  "theme": { "base": "splendor", "overrides": { "accent": "#7c5cff" } },
  "pages": [
    {
      "path": "/",
      "title": "Tip",
      "blocks": [
        { "id": "hero", "widget": "hero", "props": { "heading": "Buy me a coffee" } },
        { "id": "tips", "widget": "tip-jar", "props": { "recipient": "SQ:…", "amount": "0.1" } }
      ]
    }
  ]
}
```

A spec is data only (no code). `widget` is a catalog id: a drop-in widget from
`@splendor/connect-react/manifest` (`transfer`, `token-panel`, …) or a block from this package
(`hero`, `tip-jar`, `members-area`, `nft-drop`, `balance-summary`, `contract-call`, … — the full
list with prop schemas is `BLOCK_MANIFESTS`). `contract-call` reads or calls one function of a
deployed contract (`get()`, `set(uint256)`, …). Pages can also hold a layout tree (`root`:
section → row → column → block); `blocks` is the flat list kept in step with it.

## Validate, migrate, generate

```ts
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { createCatalog, generateProject, migrateSpec, validateSpec } from '@splendor/dapp-spec';

const catalog = createCatalog(WIDGET_MANIFESTS);
const result = validateSpec(migrateSpec(untrusted), { catalog });
if (!result.ok) throw new Error(result.issues.map((i) => `${i.path}: ${i.message}`).join('\n'));

const project = generateProject(result.spec, {
  framework: 'vite', // or 'next'
  catalog,
  versions: { sqcSdk: '3.0.0', connectReact: '3.0.0' },
});
// project.files: Record<relativePath, contents>
```

- `validateSpec` never throws and is the trust boundary for specs from storage, share links or an
  API: it checks shape, limits, known widgets, prop types and formats (SQ addresses, amounts,
  URLs) and keeps theme values to plain CSS tokens.
- `migrateSpec` upgrades older specs. Every format change adds a migration; old ones are never
  edited, so saved projects stay loadable.
- `generateProject` is pure: it returns file contents and never touches the disk. It runs
  `validateSpec` against `catalog` first and throws on an invalid spec; pass `validate: false` only
  for a spec you validated yourself.

Generated projects pin the signer WASM digests in production builds (Vite via `sqcWasmPlugin`,
Next.js via a `prebuild` script) and proxy public RPCs under `/rpc/<network>` on localhost.

## Templates

| Id | Kind | What you get |
|----|------|--------------|
| `token-project` | kit | Public landing page with live token data, plus a wallet-only dashboard |
| `contract-app` | kit | Landing page with live contract reads, plus a signed-in dashboard with a form per write function |
| `wallet-dashboard` | app | Account, balance, SPLD transfer, pending transactions |
| `token-launchpad` | app | Deploy a QRC-20 token, then manage it on a second page |
| `tip-jar` | app | Fixed-recipient tips with the real fee (setup: recipient) |
| `members-area` | app | Wallet sign-in that unlocks members-only content |
| `nft-drop` | app | Deploy or use a QRC-1155 collection; mint and show balances |
| `nft-membership` | app | Public QRC-721 gallery, plus a wallet-only members page |

`TEMPLATES` lists them. Templates with `setup` fields (the tip-jar recipient) validate only once
those are filled. Kits have `slots` (your token, your contract and ABI) that
`instantiateTemplate(t, answers)` fills across every page; without answers they use demo values.
Use `getTemplate(id)` to look one up: it also resolves retired ids in `TEMPLATE_ALIASES`
(`landing` → `token-project`) so old links keep working. `PAGE_TEMPLATES` are single-page
starters for adding a page to an existing spec; `emptySpec()` is a blank spec.

## Adding a block

1. Add `blocks/MyBlock.tsx` exporting `function MyBlock(props)` (it is typechecked against the
   published packages by `npm run typecheck:blocks`).
2. Add a `BLOCK_MANIFESTS` entry in `src/blocks.ts` with its prop schema and defaults.
3. Never rename an existing id — saved specs refer to it.
