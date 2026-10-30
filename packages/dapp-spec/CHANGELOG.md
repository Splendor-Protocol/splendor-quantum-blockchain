# @splendor/dapp-spec

## Unreleased

- Spec format is now **`DappSpec` v2** (`DAPP_SPEC_VERSION = 2`): every page has a layout tree, and
  `contracts` / `nav` are optional. `migrateSpec` upgrades v0 and v1 specs; run `validateSpec` on
  the result.
- `generateProject`: `vendorFiles` keys must be relative paths under `vendor/@splendor/` (no `..`,
  absolute paths, drive letters or backslashes); anything else throws.
- Codegen: unique page component names (`/` and `/home` no longer collide), Next exports get real
  route links via a shared `blocks/pageHref.ts`, `spec.nav` (brand, connect button, link order,
  footer) is honoured in both shells.
- Validation: one id namespace for containers and blocks, containers-only page roots, nav and
  contract checks, stricter CSS tokens, `integer` and `httpsOnly` prop formats (NFT ids, image and
  members `apiUrl`).
- `styleToCss(style, kind)`: column `width` becomes flex-basis, row `align` sets both axes.
- Blocks: newline-separated list props, no dead disabled CTA buttons, resynced form inputs,
  cancel-safe `ContractRead`, honest `TxStatusCard`, error states in `NftGallery`.

## 0.1.0

- First release: `DappSpec` v1, `validateSpec`, `migrateSpec`, `createCatalog`, five starter
  templates, blocks (`hero`, `balance-summary`, `tip-jar`, `members-area`, `nft-drop`) and
  `generateProject` for Vite and Next.js.
