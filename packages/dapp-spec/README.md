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
  "version": 1,
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
(`hero`, `tip-jar`, `members-area`, `nft-drop`, `balance-summary`).

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
- `generateProject` is pure: it returns file contents and never touches the disk.

Generated projects pin the signer WASM digests in production builds (Vite via `sqcWasmPlugin`,
Next.js via a `prebuild` script) and proxy public RPCs under `/rpc/<network>` on localhost.

## Templates

`TEMPLATES`: `wallet-dashboard`, `token-launchpad`, `tip-jar`, `members-area`, `nft-drop`.
Each lists `setup` fields the user must fill (for example the tip-jar recipient) before the spec
validates. Use `instantiateTemplate(t)` for an editable copy and `emptySpec()` for a blank one.

## Adding a block

1. Add `blocks/MyBlock.tsx` exporting `function MyBlock(props)` (it is typechecked against the
   published packages by `npm run typecheck:blocks`).
2. Add a `BLOCK_MANIFESTS` entry in `src/blocks.ts` with its prop schema and defaults.
3. Never rename an existing id — saved specs refer to it.
